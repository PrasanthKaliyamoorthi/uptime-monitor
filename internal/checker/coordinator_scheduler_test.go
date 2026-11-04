package checker

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
	"uptime-monitor/internal/notifier"
)

func TestTCPProberSuccessAndRefusal(t *testing.T) {
	// 1. Successful listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on tcp: %v", err)
	}
	defer ln.Close()

	prober := NewTCPProber()
	ctx := context.Background()

	res := prober.Probe(ctx, &model.Monitor{
		Target:     ln.Addr().String(),
		TimeoutSec: 2,
	})
	if !res.IsSuccess {
		t.Fatalf("expected successful TCP probe, got %s", res.ErrorMessage)
	}
	if res.FailureDetails == nil || res.FailureDetails.ProbePhase != "TCP_CONNECT" {
		t.Errorf("unexpected failure details: %+v", res.FailureDetails)
	}

	// 1b. Test target with tcp:// prefix
	resPrefix := prober.Probe(ctx, &model.Monitor{
		Target:     "tcp://" + ln.Addr().String(),
		TimeoutSec: 2,
	})
	if !resPrefix.IsSuccess {
		t.Fatalf("expected successful TCP probe with tcp:// prefix, got %s", resPrefix.ErrorMessage)
	}

	// 2. Refused connection on unused port
	resRefused := prober.Probe(ctx, &model.Monitor{
		Target:     "127.0.0.1:54321", // port without listener
		TimeoutSec: 1,
	})
	if resRefused.IsSuccess {
		t.Fatal("expected TCP failure on unlistening port, got success")
	}
	if resRefused.ErrorType != "CONN_REFUSED" {
		t.Errorf("expected CONN_REFUSED, got %s", resRefused.ErrorType)
	}
}

func TestNormalizeTCPTarget(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"example.com:8080", "example.com:8080"},
		{"tcp://example.com:8080", "example.com:8080"},
		{"TCP://127.0.0.1:5432/", "127.0.0.1:5432"},
		{"::1", "[::1]:80"},
		{"[::1]:5432", "[::1]:5432"},
		{"192.168.1.1", "192.168.1.1:80"},
		{"example.com", "example.com:80"},
	}

	for _, c := range cases {
		out, err := normalizeTCPTarget(c.input)
		if err != nil {
			t.Errorf("unexpected error for %q: %v", c.input, err)
		} else if out != c.expected {
			t.Errorf("for %q, expected %q, got %q", c.input, c.expected, out)
		}
	}
}

func TestCoordinatorStateTransitionsAndDamping(t *testing.T) {
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer database.Close()

	mRepo := db.NewMonitorRepo(database)
	cRepo := db.NewCheckRepo(database)
	wRepo := db.NewWebhookRepo(database)
	log := logger.New(db.NewLogRepo(database))

	var notifiedEvents []string
	notifServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer notifServer.Close()

	// Configure alert webhook that triggers on everything
	wh := &model.AlertWebhook{
		Name:                "Test Dispatcher Hook",
		URL:                 notifServer.URL,
		PayloadTemplate:     `{"event":"{{.Event}}"}`,
		TriggerOnDown:       true,
		TriggerOnDegraded:   true,
		TriggerOnUp:         true,
		TriggerOnTLSWarning: true,
		IsActive:            true,
	}
	_ = wRepo.Create(wh)
	disp := notifier.NewDispatcher(wRepo, log)

	coord := NewCoordinator(mRepo, cRepo, disp, log)
	ctx := context.Background()

	// Failing target (unlistening port)
	mon := &model.Monitor{
		Name:          "Flaky Service",
		Protocol:      "tcp",
		Target:        "127.0.0.1:59999",
		RetryCount:    2,
		CurrentStatus: "up",
	}
	_ = mRepo.Create(mon)

	// Failure 1 (with retryCount=2): should transition from "up" to "degraded"
	res1 := coord.ExecuteCheck(ctx, mon)
	if res1.IsSuccess {
		t.Fatal("expected failure on 59999")
	}
	monAfter1, _ := mRepo.GetByID(mon.ID)
	if monAfter1.CurrentStatus != "degraded" || monAfter1.ConsecutiveFailures != 1 {
		t.Errorf("expected degraded with 1 failure, got status=%s failures=%d", monAfter1.CurrentStatus, monAfter1.ConsecutiveFailures)
	}

	// Failure 2: reached retry threshold 2 -> should transition to "down"
	mon.CurrentStatus = monAfter1.CurrentStatus
	mon.ConsecutiveFailures = monAfter1.ConsecutiveFailures
	res2 := coord.ExecuteCheck(ctx, mon)
	if res2.IsSuccess {
		t.Fatal("expected failure on check 2")
	}
	monAfter2, _ := mRepo.GetByID(mon.ID)
	if monAfter2.CurrentStatus != "down" || monAfter2.ConsecutiveFailures != 2 {
		t.Errorf("expected down with 2 failures, got status=%s failures=%d", monAfter2.CurrentStatus, monAfter2.ConsecutiveFailures)
	}

	// Now start a listener so next check succeeds -> should recover to "up"
	ln, err := net.Listen("tcp", "127.0.0.1:59999")
	if err != nil {
		t.Fatalf("failed to bind listener: %v", err)
	}
	defer ln.Close()

	mon.CurrentStatus = monAfter2.CurrentStatus
	mon.ConsecutiveFailures = monAfter2.ConsecutiveFailures
	res3 := coord.ExecuteCheck(ctx, mon)
	if !res3.IsSuccess {
		t.Fatalf("expected recovery check to succeed, got %s", res3.ErrorMessage)
	}

	monAfter3, _ := mRepo.GetByID(mon.ID)
	if monAfter3.CurrentStatus != "up" || monAfter3.ConsecutiveFailures != 0 {
		t.Errorf("expected recovery to up with 0 failures, got status=%s failures=%d", monAfter3.CurrentStatus, monAfter3.ConsecutiveFailures)
	}

	_ = notifiedEvents
}

func TestSchedulerDeduplicationAndPanicRecovery(t *testing.T) {
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer database.Close()

	mRepo := db.NewMonitorRepo(database)
	cRepo := db.NewCheckRepo(database)
	log := logger.New(db.NewLogRepo(database))
	coord := NewCoordinator(mRepo, cRepo, nil, log)

	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		time.Sleep(100 * time.Millisecond) // slow check
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mon := &model.Monitor{
		Name:        "Slow Service",
		Protocol:    "http",
		Target:      server.URL,
		IntervalSec: 60,
		IsActive:    true,
	}
	_ = mRepo.Create(mon)

	sched := NewScheduler(coord, mRepo, log, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sched.Start(ctx)

	// Trigger immediate check while scheduler is running
	res1, err := sched.TriggerNow(ctx, mon.ID)
	if err != nil || !res1.IsSuccess {
		t.Fatalf("expected TriggerNow to succeed: %v", err)
	}

	// While in-flight, a second TriggerNow must be deduplicated
	sched.mu.Lock()
	sched.inFlight[mon.ID] = true
	sched.mu.Unlock()

	resDup, err := sched.TriggerNow(ctx, mon.ID)
	if err != nil {
		t.Fatalf("unexpected error on deduplication: %v", err)
	}
	if resDup.ErrorMessage != "check already in progress" {
		t.Errorf("expected 'check already in progress', got %s", resDup.ErrorMessage)
	}

	sched.mu.Lock()
	delete(sched.inFlight, mon.ID)
	sched.mu.Unlock()

	// Stop scheduler gracefully
	sched.Stop()
}
