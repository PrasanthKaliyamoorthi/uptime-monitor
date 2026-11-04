package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"uptime-monitor/internal/checker"
	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
)

func TestAdminIPBlacklistAndCLIUnblock(t *testing.T) {
	dbFile := fmt.Sprintf("./test_blacklist_%d.db", os.Getpid())
	defer os.Remove(dbFile)

	database, err := db.InitDB(dbFile)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer database.Close()

	adminRepo := db.NewAdminRepo(database)
	blacklistRepo := db.NewBlacklistRepo(database)
	monitorRepo := db.NewMonitorRepo(database)
	checkRepo := db.NewCheckRepo(database)
	statusPageRepo := db.NewStatusPageRepo(database)
	incidentRepo := db.NewIncidentRepo(database)
	webhookRepo := db.NewWebhookRepo(database)
	logRepo := db.NewLogRepo(database)
	sysLogger := logger.New(logRepo)

	coordinator := checker.NewCoordinator(monitorRepo, checkRepo, nil, sysLogger)
	scheduler := checker.NewScheduler(coordinator, monitorRepo, sysLogger, 5)

	// Threshold set to 3 for testing
	rt, err := NewRouter(
		adminRepo,
		blacklistRepo,
		monitorRepo,
		checkRepo,
		statusPageRepo,
		incidentRepo,
		webhookRepo,
		logRepo,
		scheduler,
		coordinator,
		sysLogger,
		3,
	)
	if err != nil {
		t.Fatalf("failed to build router: %v", err)
	}

	tr, _ := NewTemplateRenderer()
	rt.SetAuxHandlers(
		NewAdminIncidentsHandler(incidentRepo, nil, tr, sysLogger),
		NewAdminWebhooksHandler(webhookRepo, nil, tr, sysLogger),
	)

	// 1. Initial Setup
	setupForm := url.Values{
		"username":         {"admin"},
		"password":         {"supersecret123"},
		"confirm_password": {"supersecret123"},
		"title":            {"Acme Status"},
		"default_days":     {"90"},
	}
	reqSetup := httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(setupForm.Encode()))
	reqSetup.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recSetup := httptest.NewRecorder()
	rt.ServeHTTP(recSetup, reqSetup)

	if recSetup.Code != http.StatusFound {
		t.Fatalf("expected 302 on setup, got %d", recSetup.Code)
	}

	attackerIP := "203.0.113.42"

	// 2. Attempt 1: Failed login
	loginFormBad := url.Values{
		"username": {"admin"},
		"password": {"wrongpassword1"},
	}
	req1 := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(loginFormBad.Encode()))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req1.Header.Set("X-Forwarded-For", attackerIP)
	rec1 := httptest.NewRecorder()
	rt.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("attempt 1: expected 200 OK (with error message), got %d", rec1.Code)
	}
	if !strings.Contains(rec1.Body.String(), "2 attempt(s) remaining before IP blacklist") {
		t.Errorf("expected attempts remaining message, got: %s", rec1.Body.String())
	}

	// Verify not yet blacklisted
	isBlocked, err := blacklistRepo.IsBlacklisted(attackerIP)
	if err != nil || isBlocked {
		t.Fatalf("expected attackerIP not blocked after 1 attempt")
	}

	// 3. Attempt 2: Failed login
	req2 := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(loginFormBad.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Header.Set("X-Forwarded-For", attackerIP)
	rec2 := httptest.NewRecorder()
	rt.ServeHTTP(rec2, req2)

	if !strings.Contains(rec2.Body.String(), "1 attempt(s) remaining before IP blacklist") {
		t.Errorf("expected 1 attempt remaining message, got: %s", rec2.Body.String())
	}

	// 4. Attempt 3: 3rd failed login (reaches threshold 3)
	req3 := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(loginFormBad.Encode()))
	req3.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req3.Header.Set("X-Forwarded-For", attackerIP)
	rec3 := httptest.NewRecorder()
	rt.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusForbidden {
		t.Fatalf("attempt 3: expected 403 Forbidden on reaching threshold, got %d", rec3.Code)
	}
	if !strings.Contains(rec3.Body.String(), "has been blacklisted") {
		t.Errorf("expected blacklisted message, got: %s", rec3.Body.String())
	}

	// Verify blacklisted in DB
	isBlocked, err = blacklistRepo.IsBlacklisted(attackerIP)
	if err != nil || !isBlocked {
		t.Fatalf("expected attackerIP to be blocked in database")
	}

	// 5. Subsequent request to /admin/login from blacklisted IP must be 403 Forbidden
	reqBlockedLogin := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	reqBlockedLogin.Header.Set("X-Forwarded-For", attackerIP)
	recBlockedLogin := httptest.NewRecorder()
	rt.ServeHTTP(recBlockedLogin, reqBlockedLogin)

	if recBlockedLogin.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on /admin/login for blacklisted IP, got %d", recBlockedLogin.Code)
	}

	// 6. Request to /admin from blacklisted IP must be 403 Forbidden
	reqBlockedAdmin := httptest.NewRequest(http.MethodGet, "/admin", nil)
	reqBlockedAdmin.Header.Set("X-Forwarded-For", attackerIP)
	recBlockedAdmin := httptest.NewRecorder()
	rt.ServeHTTP(recBlockedAdmin, reqBlockedAdmin)

	if recBlockedAdmin.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on /admin for blacklisted IP, got %d", recBlockedAdmin.Code)
	}
	if !strings.Contains(recBlockedAdmin.Body.String(), "-unblock-ip 203.0.113.42") {
		t.Errorf("expected unblock instructions in 403 body, got: %s", recBlockedAdmin.Body.String())
	}

	// 7. CRITICAL: Public status page MUST remain accessible to the blacklisted IP!
	reqPublic := httptest.NewRequest(http.MethodGet, "/status", nil)
	reqPublic.Header.Set("X-Forwarded-For", attackerIP)
	recPublic := httptest.NewRecorder()
	rt.ServeHTTP(recPublic, reqPublic)

	if recPublic.Code != http.StatusOK {
		t.Fatalf("expected public status page to return 200 OK for blacklisted IP, got %d", recPublic.Code)
	}
	if !strings.Contains(recPublic.Body.String(), "System Status") {
		t.Errorf("expected status page content for blacklisted IP")
	}

	// 8. CLI Unblock simulation: blacklistRepo.Remove(attackerIP)
	if err := blacklistRepo.Remove(attackerIP); err != nil {
		t.Fatalf("failed to unblock IP: %v", err)
	}

	// Verify no longer blocked
	isBlocked, err = blacklistRepo.IsBlacklisted(attackerIP)
	if err != nil || isBlocked {
		t.Fatalf("expected attackerIP to be unblocked after Remove")
	}

	// 9. Verify admin login is now accessible again from attackerIP
	reqUnblockedLogin := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	reqUnblockedLogin.Header.Set("X-Forwarded-For", attackerIP)
	recUnblockedLogin := httptest.NewRecorder()
	rt.ServeHTTP(recUnblockedLogin, reqUnblockedLogin)

	if recUnblockedLogin.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on /admin/login after unblocking, got %d", recUnblockedLogin.Code)
	}

	// 10. Successful login clears failed attempt history
	loginFormGood := url.Values{
		"username": {"admin"},
		"password": {"supersecret123"},
	}
	reqGood := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(loginFormGood.Encode()))
	reqGood.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqGood.Header.Set("X-Forwarded-For", attackerIP)
	recGood := httptest.NewRecorder()
	rt.ServeHTTP(recGood, reqGood)

	if recGood.Code != http.StatusFound {
		t.Fatalf("expected 302 on successful login, got %d", recGood.Code)
	}

	// Check entry in DB is deleted or attempts reset
	entry, _ := blacklistRepo.GetEntry(attackerIP)
	if entry != nil && entry.Blacklisted {
		t.Fatalf("expected entry not to be blacklisted after good login")
	}
}
