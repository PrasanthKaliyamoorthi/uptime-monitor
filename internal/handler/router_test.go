package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"uptime-monitor/internal/checker"
	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
)

func TestRouterFirstRunAndDomainRouting(t *testing.T) {
	// Initialize in-memory SQLite database
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init in-memory db: %v", err)
	}
	defer database.Close()

	adminRepo := db.NewAdminRepo(database)
	monitorRepo := db.NewMonitorRepo(database)
	checkRepo := db.NewCheckRepo(database)
	statusPageRepo := db.NewStatusPageRepo(database)
	incidentRepo := db.NewIncidentRepo(database)
	webhookRepo := db.NewWebhookRepo(database)
	logRepo := db.NewLogRepo(database)
	blacklistRepo := db.NewBlacklistRepo(database)

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
		t.Fatalf("failed to create router: %v", err)
	}

	tr, _ := NewTemplateRenderer()
	rt.SetAuxHandlers(
		NewAdminIncidentsHandler(incidentRepo, nil, tr, sysLogger),
		NewAdminWebhooksHandler(webhookRepo, nil, tr, sysLogger),
	)

	// 1. Initial run: hitting / must redirect to /setup because 0 admins exist
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	rt.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Errorf("expected status 302 redirect on first run, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/setup" {
		t.Errorf("expected redirect location /setup, got %s", loc)
	}

	// 2. Perform /setup POST to create admin account
	setupForm := url.Values{
		"username":         {"admin"},
		"password":         {"supersecret123"},
		"confirm_password": {"supersecret123"},
	}
	setupReq := httptest.NewRequest("POST", "/setup", strings.NewReader(setupForm.Encode()))
	setupReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setupRec := httptest.NewRecorder()
	rt.ServeHTTP(setupRec, setupReq)

	if setupRec.Code != http.StatusFound {
		t.Fatalf("expected status 302 after setup POST, got %d", setupRec.Code)
	}
	adminCookie := setupRec.Result().Cookies()
	if len(adminCookie) == 0 {
		t.Fatalf("expected admin_session cookie to be set")
	}

	// Verify admin exists in DB now
	hasAdmin, err := adminRepo.HasAdmin()
	if err != nil || !hasAdmin {
		t.Fatalf("expected admin to exist in DB")
	}

	// 3. Create two status pages for domain testing:
	//    Page A: "status.git.com"
	//    Page B: "status.terminal.in"
	_ = statusPageRepo.Create(&model.StatusPage{
		Slug:        "git-status",
		Name:        "Git Infrastructure Status",
		Domain:      "status.git.com",
		Theme:       model.ThemeTokyoNight,
		IsDefault:   false,
	})
	_ = statusPageRepo.Create(&model.StatusPage{
		Slug:        "terminal-status",
		Name:        "Terminal Services Status",
		Domain:      "status.terminal.in",
		Theme:       model.ThemeCatppuccinMocha,
		IsDefault:   true,
	})

	// 4. Request with Host: status.git.com
	reqGit := httptest.NewRequest("GET", "/", nil)
	reqGit.Host = "status.git.com"
	recGit := httptest.NewRecorder()
	rt.ServeHTTP(recGit, reqGit)

	if recGit.Code != http.StatusOK {
		t.Errorf("expected 200 OK for status.git.com, got %d", recGit.Code)
	}
	gitBody := recGit.Body.String()
	if !strings.Contains(gitBody, "Git Infrastructure Status") {
		t.Errorf("expected page title 'Git Infrastructure Status' in body")
	}
	if !strings.Contains(gitBody, `data-theme="tokyo-night"`) {
		t.Errorf("expected theme 'tokyo-night' in html attribute")
	}

	// 5. Request with Host: status.terminal.in
	reqTerm := httptest.NewRequest("GET", "/", nil)
	reqTerm.Host = "status.terminal.in"
	recTerm := httptest.NewRecorder()
	rt.ServeHTTP(recTerm, reqTerm)

	if recTerm.Code != http.StatusOK {
		t.Errorf("expected 200 OK for status.terminal.in, got %d", recTerm.Code)
	}
	termBody := recTerm.Body.String()
	if !strings.Contains(termBody, "Terminal Services Status") {
		t.Errorf("expected page title 'Terminal Services Status' in body")
	}
	if !strings.Contains(termBody, `data-theme="catppuccin-mocha"`) {
		t.Errorf("expected theme 'catppuccin-mocha' in html attribute")
	}
	if !strings.Contains(termBody, `hx-get="/"`) {
		t.Errorf("expected hx-get to refresh against '/' for custom domain, got %s", termBody)
	}

	// 5b. Request with REST path /status/terminal
	reqPath := httptest.NewRequest("GET", "/status/terminal", nil)
	recPath := httptest.NewRecorder()
	rt.ServeHTTP(recPath, reqPath)
	if recPath.Code != http.StatusOK {
		t.Errorf("expected 200 OK for /status/terminal, got %d", recPath.Code)
	}
	if !strings.Contains(recPath.Body.String(), "Terminal Services Status") {
		t.Errorf("expected /status/terminal to resolve Terminal Services Status page")
	}

	// 6. Test SVG Badge endpoint
	_ = monitorRepo.Create(&model.Monitor{
		ID:            "mon-badge-test",
		Name:          "Auth Service",
		Protocol:      "https",
		Target:        "https://example.com",
		CurrentStatus: "up",
		IsActive:      true,
	})

	reqBadge := httptest.NewRequest("GET", "/api/badge/mon-badge-test/status", nil)
	recBadge := httptest.NewRecorder()
	rt.ServeHTTP(recBadge, reqBadge)

	if recBadge.Code != http.StatusOK {
		t.Errorf("expected 200 OK for badge, got %d", recBadge.Code)
	}
	if !strings.Contains(recBadge.Body.String(), "<svg") || !strings.Contains(recBadge.Body.String(), "operational") {
		t.Errorf("expected SVG badge with 'operational' text, got %s", recBadge.Body.String())
	}
}
