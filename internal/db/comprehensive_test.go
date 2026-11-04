package db

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"uptime-monitor/internal/model"
)

func setupTestDB(t *testing.T) (*MonitorRepo, *CheckRepo, *IncidentRepo, *WebhookRepo, *BlacklistRepo, *SettingsRepo, *LogRepo, func()) {
	t.Helper()
	database, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init test database: %v", err)
	}

	mRepo := NewMonitorRepo(database)
	cRepo := NewCheckRepo(database)
	iRepo := NewIncidentRepo(database)
	wRepo := NewWebhookRepo(database)
	bRepo := NewBlacklistRepo(database)
	sRepo := NewSettingsRepo(database)
	lRepo := NewLogRepo(database)

	cleanup := func() {
		database.Close()
	}

	return mRepo, cRepo, iRepo, wRepo, bRepo, sRepo, lRepo, cleanup
}

func TestMonitorRepoLifecycle(t *testing.T) {
	mRepo, cRepo, _, _, _, _, _, cleanup := setupTestDB(t)
	defer cleanup()

	// 1. Create Monitor
	mon := &model.Monitor{
		Name:         "Payment API",
		Protocol:     "https",
		Target:       "https://api.payment.example.com/health",
		Method:       "GET",
		IntervalSec:  30,
		TimeoutSec:   5,
		ExpectedCode: 200,
		Keyword:      "healthy",
		RetryCount:   3,
		IsActive:     true,
	}

	if err := mRepo.Create(mon); err != nil {
		t.Fatalf("failed to create monitor: %v", err)
	}
	if mon.ID == "" {
		t.Fatal("expected non-empty monitor ID after Create")
	}

	// 2. GetByID
	fetched, err := mRepo.GetByID(mon.ID)
	if err != nil {
		t.Fatalf("failed to get monitor by ID: %v", err)
	}
	if fetched.Name != "Payment API" || fetched.IntervalSec != 30 {
		t.Errorf("unexpected fetched monitor data: %+v", fetched)
	}

	// 3. UpdateStatus
	if err := mRepo.UpdateStatus(mon.ID, "down", 3, 142); err != nil {
		t.Fatalf("failed to update status: %v", err)
	}

	updated, _ := mRepo.GetByID(mon.ID)
	if updated.CurrentStatus != "down" || updated.ConsecutiveFailures != 3 || updated.LastLatencyMs != 142 {
		t.Errorf("unexpected status after update: %+v", updated)
	}

	// 4. GetActive
	activeList, err := mRepo.GetActive()
	if err != nil {
		t.Fatalf("failed to get active monitors: %v", err)
	}
	if len(activeList) != 1 {
		t.Fatalf("expected 1 active monitor, got %d", len(activeList))
	}

	// 5. Update Full Config
	updated.Name = "Payment Gateway v2"
	updated.IntervalSec = 60
	if err := mRepo.Update(updated); err != nil {
		t.Fatalf("failed to update monitor: %v", err)
	}

	// 6. Delete with cascade
	_ = cRepo.LogCheck(&model.CheckLog{MonitorID: mon.ID, LatencyMs: 50, IsSuccess: true})
	if err := mRepo.Delete(mon.ID); err != nil {
		t.Fatalf("failed to delete monitor: %v", err)
	}

	deleted, _ := mRepo.GetByID(mon.ID)
	if deleted != nil {
		t.Errorf("expected monitor to be deleted, got %+v", deleted)
	}
}

func TestCheckRepoLogsAndPruning(t *testing.T) {
	mRepo, cRepo, _, _, _, _, _, cleanup := setupTestDB(t)
	defer cleanup()

	mon := &model.Monitor{Name: "DB Test Target", Target: "http://localhost:8080", Protocol: "http"}
	_ = mRepo.Create(mon)

	now := time.Now().UTC()
	// Insert 3 check logs
	for i := 1; i <= 3; i++ {
		log := &model.CheckLog{
			MonitorID:  mon.ID,
			StatusCode: 200,
			LatencyMs:  int64(i * 10),
			IsSuccess:  true,
			CheckedAt:  now.Add(time.Duration(i) * time.Minute),
			FailureDetails: &model.DiagnosticDetail{
				ProbePhase: "HTTP_REQUEST",
			},
		}
		if err := cRepo.LogCheck(log); err != nil {
			t.Fatalf("failed to log check %d: %v", i, err)
		}
	}

	// Fetch recent logs
	recent, err := cRepo.GetRecentLogs(mon.ID, 10)
	if err != nil {
		t.Fatalf("failed to get recent logs: %v", err)
	}
	if len(recent) != 3 {
		t.Fatalf("expected 3 recent logs, got %d", len(recent))
	}
	if recent[0].FailureDetails == nil || recent[0].FailureDetails.ProbePhase != "HTTP_REQUEST" {
		t.Errorf("failure details was not restored properly: %+v", recent[0].FailureDetails)
	}

	// Get latest check
	latest, err := cRepo.GetLatestCheck(mon.ID)
	if err != nil {
		t.Fatalf("failed to get latest check: %v", err)
	}
	if latest == nil || latest.LatencyMs != 30 {
		t.Errorf("expected latest check latency 30ms, got %+v", latest)
	}

	// Prune logs older than now + 2 minutes
	pruned, err := cRepo.PruneOldLogs(now.Add(2 * time.Minute))
	if err != nil {
		t.Fatalf("failed to prune logs: %v", err)
	}
	if pruned != 1 {
		t.Errorf("expected 1 log pruned, got %d", pruned)
	}
}

func TestBlacklistRepoThrottling(t *testing.T) {
	_, _, _, _, bRepo, _, _, cleanup := setupTestDB(t)
	defer cleanup()

	ip := "192.168.1.100"

	// 1. Initial state
	isBlocked, err := bRepo.IsBlacklisted(ip)
	if err != nil || isBlocked {
		t.Fatalf("expected not blacklisted initially, got %v, %v", isBlocked, err)
	}

	// 2. Failed attempts below threshold (threshold = 3)
	blocked, attempts, err := bRepo.RecordFailedAttempt(ip, 3, "Wrong password")
	if err != nil || blocked || attempts != 1 {
		t.Fatalf("attempt 1: expected blocked=false, attempts=1, got %v, %d, %v", blocked, attempts, err)
	}

	blocked, attempts, err = bRepo.RecordFailedAttempt(ip, 3, "Wrong password")
	if err != nil || blocked || attempts != 2 {
		t.Fatalf("attempt 2: expected blocked=false, attempts=2, got %v, %d, %v", blocked, attempts, err)
	}

	// 3. Third attempt reaches threshold -> blacklisted!
	blocked, attempts, err = bRepo.RecordFailedAttempt(ip, 3, "Wrong password")
	if err != nil || !blocked || attempts != 3 {
		t.Fatalf("attempt 3: expected blocked=true, attempts=3, got %v, %d, %v", blocked, attempts, err)
	}

	isBlocked, err = bRepo.IsBlacklisted(ip)
	if err != nil || !isBlocked {
		t.Fatalf("expected IsBlacklisted=true, got %v, %v", isBlocked, err)
	}

	// 4. Test IPv6 localhost normalization (::1 -> 127.0.0.1)
	_, _, _ = bRepo.RecordFailedAttempt("::1", 1, "Localhost test")
	normBlocked, _ := bRepo.IsBlacklisted("127.0.0.1")
	if !normBlocked {
		t.Error("expected 127.0.0.1 to be blacklisted after ::1 failed attempt")
	}

	// 5. Unblock / Remove IP
	if err := bRepo.Remove(ip); err != nil {
		t.Fatalf("failed to remove IP: %v", err)
	}
	isBlocked, _ = bRepo.IsBlacklisted(ip)
	if isBlocked {
		t.Error("expected IP to be unblocked after Remove")
	}
}

func TestSettingsRepoLifecycle(t *testing.T) {
	_, _, _, _, _, sRepo, _, cleanup := setupTestDB(t)
	defer cleanup()

	// Initial empty
	domain, err := sRepo.GetAdminAllowedDomain()
	if err != nil || domain != "" {
		t.Fatalf("expected empty domain initially, got %q, %v", domain, err)
	}

	// Set domain
	if err := sRepo.SetAdminAllowedDomain("console.example.com"); err != nil {
		t.Fatalf("failed to set admin domain: %v", err)
	}

	domain, err = sRepo.GetAdminAllowedDomain()
	if err != nil || domain != "console.example.com" {
		t.Fatalf("expected console.example.com, got %q, %v", domain, err)
	}

	// Clear domain
	if err := sRepo.SetAdminAllowedDomain(""); err != nil {
		t.Fatalf("failed to clear admin domain: %v", err)
	}
	domain, _ = sRepo.GetAdminAllowedDomain()
	if domain != "" {
		t.Fatalf("expected empty domain after clearing, got %q", domain)
	}
}

func TestIncidentRepoLifecycle(t *testing.T) {
	_, _, iRepo, _, _, _, _, cleanup := setupTestDB(t)
	defer cleanup()

	inc := &model.Incident{
		Title:    "Database High Latency",
		Status:   "investigating",
		Severity: "major",
	}

	if err := iRepo.Create(inc); err != nil {
		t.Fatalf("failed to create incident: %v", err)
	}
	if inc.ID == "" {
		t.Fatal("expected non-empty incident ID")
	}

	// Add update timeline entry
	if err := iRepo.AddUpdate(inc.ID, "identified", "Identified stuck analytical query on replica"); err != nil {
		t.Fatalf("failed to add incident update: %v", err)
	}

	// Resolve incident via AddUpdate
	if err := iRepo.AddUpdate(inc.ID, "resolved", "Stuck query terminated, replication lag normalized"); err != nil {
		t.Fatalf("failed to resolve incident: %v", err)
	}

	fetched, err := iRepo.GetByID(inc.ID)
	if err != nil {
		t.Fatalf("failed to get incident by ID: %v", err)
	}
	if fetched.Status != "resolved" || len(fetched.Updates) < 2 {
		t.Errorf("unexpected resolved incident state: status=%s updates=%d", fetched.Status, len(fetched.Updates))
	}
}

func TestWebhookRepoLifecycle(t *testing.T) {
	_, _, _, wRepo, _, _, _, cleanup := setupTestDB(t)
	defer cleanup()

	wh := &model.AlertWebhook{
		Name:            "Slack Alerts",
		URL:             "https://hooks.slack.com/services/test",
		Method:          "POST",
		PayloadTemplate: `{"text": "{{.MonitorName}} is {{.Status}}"}`,
		TriggerOnDown:   true,
		TriggerOnUp:     true,
		IsActive:        true,
	}

	if err := wRepo.Create(wh); err != nil {
		t.Fatalf("failed to create webhook: %v", err)
	}
	if wh.ID == "" {
		t.Fatal("expected non-empty webhook ID")
	}

	active, err := wRepo.GetActive()
	if err != nil || len(active) != 1 {
		t.Fatalf("expected 1 active webhook, got %d, %v", len(active), err)
	}

	wh.Name = "Slack Alerts v2"
	if err := wRepo.Update(wh); err != nil {
		t.Fatalf("failed to update webhook: %v", err)
	}

	fetched, err := wRepo.GetByID(wh.ID)
	if err != nil || fetched.Name != "Slack Alerts v2" {
		t.Errorf("expected updated name 'Slack Alerts v2', got %v, %v", fetched, err)
	}

	if err := wRepo.Delete(wh.ID); err != nil {
		t.Fatalf("failed to delete webhook: %v", err)
	}
	afterDelete, err := wRepo.GetByID(wh.ID)
	if err == nil || afterDelete != nil {
		t.Errorf("expected webhook to be deleted, got %+v, %v", afterDelete, err)
	}
}

// TestSQLiteConcurrencyStress simulates 50 concurrent writers and readers
// hammering the SQLite database in WAL mode to guarantee zero database-is-locked errors.
func TestSQLiteConcurrencyStress(t *testing.T) {
	mRepo, cRepo, _, _, bRepo, sRepo, lRepo, cleanup := setupTestDB(t)
	defer cleanup()

	mon := &model.Monitor{Name: "Stress Monitor", Target: "http://stress.local", Protocol: "http"}
	if err := mRepo.Create(mon); err != nil {
		t.Fatalf("failed to create monitor: %v", err)
	}

	const workerCount = 50
	var wg sync.WaitGroup
	errCh := make(chan error, workerCount*3)

	for i := 0; i < workerCount; i++ {
		workerID := i
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Concurrently write check log
			err := cRepo.LogCheck(&model.CheckLog{
				MonitorID:  mon.ID,
				StatusCode: 200,
				LatencyMs:  int64(10 + workerID),
				IsSuccess:  true,
				CheckedAt:  time.Now().UTC(),
			})
			if err != nil {
				errCh <- fmt.Errorf("worker %d LogCheck failed: %w", workerID, err)
				return
			}

			// Concurrently update monitor status
			if err := mRepo.UpdateStatus(mon.ID, "up", 0, int64(15+workerID)); err != nil {
				errCh <- fmt.Errorf("worker %d UpdateStatus failed: %w", workerID, err)
				return
			}

			// Concurrently write blacklist attempt
			ip := fmt.Sprintf("10.0.0.%d", workerID%10)
			if _, _, err := bRepo.RecordFailedAttempt(ip, 5, "Stress test"); err != nil {
				errCh <- fmt.Errorf("worker %d RecordFailedAttempt failed: %w", workerID, err)
				return
			}

			// Concurrently read settings and logs
			_, _ = sRepo.Get("admin_allowed_domain")
			_, _ = cRepo.GetRecentLogs(mon.ID, 10)
			_ = lRepo.Insert(&model.SystemLog{
				Level:     "INFO",
				Source:    "stress",
				MonitorID: &mon.ID,
				Message:   "worker log",
				CreatedAt: time.Now().UTC(),
			})
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrency error: %v", err)
	}
}
