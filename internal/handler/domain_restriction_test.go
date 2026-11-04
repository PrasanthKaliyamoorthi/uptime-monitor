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

func TestAdminDomainRestriction(t *testing.T) {
	dbFile := fmt.Sprintf("./test_domain_restriction_%d.db", os.Getpid())
	defer os.Remove(dbFile)

	database, err := db.InitDB(dbFile)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer database.Close()

	adminRepo := db.NewAdminRepo(database)
	blacklistRepo := db.NewBlacklistRepo(database)
	settingsRepo := db.NewSettingsRepo(database)
	monitorRepo := db.NewMonitorRepo(database)
	checkRepo := db.NewCheckRepo(database)
	statusPageRepo := db.NewStatusPageRepo(database)
	incidentRepo := db.NewIncidentRepo(database)
	webhookRepo := db.NewWebhookRepo(database)
	logRepo := db.NewLogRepo(database)
	sysLogger := logger.New(logRepo)

	coordinator := checker.NewCoordinator(monitorRepo, checkRepo, nil, sysLogger)
	scheduler := checker.NewScheduler(coordinator, monitorRepo, sysLogger, 5)

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
	)
	if err != nil {
		t.Fatalf("failed to build router: %v", err)
	}

	// 1. Initial Setup with localhost
	setupForm := url.Values{
		"username":         {"admin"},
		"password":         {"supersecret123"},
		"confirm_password": {"supersecret123"},
		"title":            {"SagaSoft Status"},
	}
	reqSetup := httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(setupForm.Encode()))
	reqSetup.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqSetup.Host = "localhost:8080"
	recSetup := httptest.NewRecorder()
	rt.ServeHTTP(recSetup, reqSetup)

	if recSetup.Code != http.StatusFound {
		t.Fatalf("expected 302 on setup, got %d", recSetup.Code)
	}

	// 2. Configure admin allowed domain
	targetConsoleDomain := "console.status.sagasoft.xyz"
	if err := settingsRepo.SetAdminAllowedDomain(targetConsoleDomain); err != nil {
		t.Fatalf("failed to set admin allowed domain: %v", err)
	}

	// 3. Request /admin/login from the WRONG domain: status.sagasoft.xyz -> MUST BE 404 Not Found
	reqWrongDomain := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	reqWrongDomain.Host = "status.sagasoft.xyz"
	recWrongDomain := httptest.NewRecorder()
	rt.ServeHTTP(recWrongDomain, reqWrongDomain)

	if recWrongDomain.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found when accessing /admin/login from wrong domain %s, got %d", reqWrongDomain.Host, recWrongDomain.Code)
	}

	// 4. Request /admin from wrong domain with X-Forwarded-Host: status.sagasoft.xyz -> MUST BE 404 Not Found
	reqWrongProxy := httptest.NewRequest(http.MethodGet, "/admin", nil)
	reqWrongProxy.Header.Set("X-Forwarded-Host", "status.sagasoft.xyz")
	recWrongProxy := httptest.NewRecorder()
	rt.ServeHTTP(recWrongProxy, reqWrongProxy)

	if recWrongProxy.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found on proxy wrong host, got %d", recWrongProxy.Code)
	}

	// 5. Request public status page from status.sagasoft.xyz -> MUST REMAIN 200 OK without Admin Console button!
	reqPublic := httptest.NewRequest(http.MethodGet, "/status", nil)
	reqPublic.Host = "status.sagasoft.xyz"
	recPublic := httptest.NewRecorder()
	rt.ServeHTTP(recPublic, reqPublic)

	if recPublic.Code != http.StatusOK {
		t.Fatalf("expected public status page to return 200 OK on %s, got %d", reqPublic.Host, recPublic.Code)
	}
	publicBody := recPublic.Body.String()
	if strings.Contains(publicBody, "Admin Console") {
		t.Fatalf("expected Admin Console button to be hidden on unauthorized domain %s", reqPublic.Host)
	}

	// 5b. Request status page from target console domain: console.status.sagasoft.xyz -> Admin Console button MUST be visible!
	reqAdminStatus := httptest.NewRequest(http.MethodGet, "/status", nil)
	reqAdminStatus.Host = targetConsoleDomain
	recAdminStatus := httptest.NewRecorder()
	rt.ServeHTTP(recAdminStatus, reqAdminStatus)

	if recAdminStatus.Code != http.StatusOK {
		t.Fatalf("expected status page to return 200 OK on %s, got %d", targetConsoleDomain, recAdminStatus.Code)
	}
	adminStatusBody := recAdminStatus.Body.String()
	if !strings.Contains(adminStatusBody, "Admin Console") {
		t.Fatalf("expected Admin Console button to be visible on admin domain %s", targetConsoleDomain)
	}

	// 5c. Verify local Tailwind asset is served with 200 OK
	reqTailwind := httptest.NewRequest(http.MethodGet, "/static/js/tailwind.min.js", nil)
	recTailwind := httptest.NewRecorder()
	rt.ServeHTTP(recTailwind, reqTailwind)
	if recTailwind.Code != http.StatusOK {
		t.Fatalf("expected /static/js/tailwind.min.js to return 200 OK, got %d", recTailwind.Code)
	}

	// 6. Request /admin/login from the CORRECT domain: console.status.sagasoft.xyz -> MUST BE 200 OK
	reqCorrectDomain := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	reqCorrectDomain.Host = targetConsoleDomain
	recCorrectDomain := httptest.NewRecorder()
	rt.ServeHTTP(recCorrectDomain, reqCorrectDomain)

	if recCorrectDomain.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on correct domain %s, got %d", targetConsoleDomain, recCorrectDomain.Code)
	}

	// 7. Request via X-Forwarded-Host: console.status.sagasoft.xyz:443 -> MUST BE 200 OK
	reqCorrectProxy := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	reqCorrectProxy.Header.Set("X-Forwarded-Host", "console.status.sagasoft.xyz:443")
	recCorrectProxy := httptest.NewRecorder()
	rt.ServeHTTP(recCorrectProxy, reqCorrectProxy)

	if recCorrectProxy.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on proxy correct host, got %d", recCorrectProxy.Code)
	}

	// 8. Clear the restriction and verify access is restored for all domains
	if err := settingsRepo.SetAdminAllowedDomain(""); err != nil {
		t.Fatalf("failed to clear admin allowed domain: %v", err)
	}

	reqCleared := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	reqCleared.Host = "status.sagasoft.xyz"
	recCleared := httptest.NewRecorder()
	rt.ServeHTTP(recCleared, reqCleared)

	if recCleared.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on /admin/login after clearing domain restriction, got %d", recCleared.Code)
	}
}
