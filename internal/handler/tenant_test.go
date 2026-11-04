package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"uptime-monitor/internal/checker"
	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
)

func TestIsolatedStatusPageTenantWorkflow(t *testing.T) {
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
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
		t.Fatalf("failed to build router: %v", err)
	}

	tr, _ := NewTemplateRenderer()
	rt.SetAuxHandlers(
		NewAdminIncidentsHandler(incidentRepo, nil, tr, sysLogger),
		NewAdminWebhooksHandler(webhookRepo, nil, tr, sysLogger),
	)

	// Create admin so setup check passes
	_, _ = adminRepo.CreateAdmin("admin", "secret123")

	// 1. Create Tenant A: Git Services
	pageA := &model.StatusPage{
		ID:     "page-a",
		Slug:   "git",
		Name:   "Git Services Status",
		Domain: "status.git.com",
		Theme:  model.ThemeTokyoNight,
	}
	_ = statusPageRepo.Create(pageA)

	// Create Tenant B: Terminal Services
	pageB := &model.StatusPage{
		ID:     "page-b",
		Slug:   "terminal",
		Name:   "Terminal Infrastructure Status",
		Domain: "status.terminal.in",
		Theme:  model.ThemeCatppuccinMocha,
	}
	_ = statusPageRepo.Create(pageB)

	// 2. Add Monitor to Tenant A
	monA := &model.Monitor{
		ID:            "mon-git-api",
		StatusPageID:  pageA.ID,
		Name:          "Git HTTPS Gateway",
		Protocol:      "https",
		Target:        "https://git.example.com",
		CurrentStatus: "up",
		IsActive:      true,
	}
	_ = monitorRepo.Create(monA)

	// Add Monitor to Tenant B
	monB := &model.Monitor{
		ID:            "mon-term-ssh",
		StatusPageID:  pageB.ID,
		Name:          "SSH Cluster Node 1",
		Protocol:      "tcp",
		Target:        "ssh.terminal.in:22",
		CurrentStatus: "up",
		IsActive:      true,
	}
	_ = monitorRepo.Create(monB)

	// 3. Add Incident specifically to Tenant A
	incA := &model.Incident{
		ID:           "inc-git-01",
		StatusPageID: pageA.ID,
		Title:        "Git SSH Connectivity Degradation",
		Status:       "investigating",
		Severity:     "major",
	}
	_ = incidentRepo.Create(incA)
	_ = incidentRepo.AddUpdate(incA.ID, "investigating", "Investigating upstream routing.")

	// 4. Test Public Request for Tenant A (status.git.com)
	reqA := httptest.NewRequest("GET", "/", nil)
	reqA.Host = "status.git.com"
	recA := httptest.NewRecorder()
	rt.ServeHTTP(recA, reqA)

	if recA.Code != http.StatusOK {
		t.Fatalf("expected 200 for tenant A, got %d", recA.Code)
	}
	bodyA := recA.Body.String()
	if !strings.Contains(bodyA, "Git HTTPS Gateway") {
		t.Errorf("tenant A should contain 'Git HTTPS Gateway'")
	}
	if strings.Contains(bodyA, "SSH Cluster Node 1") {
		t.Errorf("tenant A must NOT contain monitor from tenant B")
	}
	if !strings.Contains(bodyA, "Git SSH Connectivity Degradation") {
		t.Errorf("tenant A should display its incident banner")
	}

	// 5. Test Public Request for Tenant B (status.terminal.in)
	reqB := httptest.NewRequest("GET", "/", nil)
	reqB.Host = "status.terminal.in"
	recB := httptest.NewRecorder()
	rt.ServeHTTP(recB, reqB)

	if recB.Code != http.StatusOK {
		t.Fatalf("expected 200 for tenant B, got %d", recB.Code)
	}
	bodyB := recB.Body.String()
	if !strings.Contains(bodyB, "SSH Cluster Node 1") {
		t.Errorf("tenant B should contain 'SSH Cluster Node 1'")
	}
	if strings.Contains(bodyB, "Git HTTPS Gateway") {
		t.Errorf("tenant B must NOT contain monitor from tenant A")
	}
	if strings.Contains(bodyB, "Git SSH Connectivity Degradation") {
		t.Errorf("tenant B must NOT display tenant A incident banner")
	}

	// 6. Test Per-Page Export for Tenant A
	reqExport := httptest.NewRequest("GET", "/admin/pages/"+pageA.ID+"/export", nil)
	// Add admin cookie
	token := rt.sessionManager.Create("admin", "admin")
	reqExport.AddCookie(&http.Cookie{Name: "admin_session", Value: token})
	recExport := httptest.NewRecorder()
	rt.ServeHTTP(recExport, reqExport)

	if recExport.Code != http.StatusOK {
		t.Fatalf("expected 200 for per-page export, got %d", recExport.Code)
	}

	var backup model.PageBackupPayload
	if err := json.Unmarshal(recExport.Body.Bytes(), &backup); err != nil {
		t.Fatalf("failed to parse page backup JSON: %v", err)
	}
	if backup.StatusPage.Name != "Git Services Status" {
		t.Errorf("expected backup status page name 'Git Services Status', got %s", backup.StatusPage.Name)
	}
	if len(backup.Monitors) != 1 || backup.Monitors[0].Name != "Git HTTPS Gateway" {
		t.Errorf("expected 1 monitor 'Git HTTPS Gateway' in export, got %v", backup.Monitors)
	}
	if len(backup.Incidents) != 1 || backup.Incidents[0].Title != "Git SSH Connectivity Degradation" {
		t.Errorf("expected 1 incident in export, got %v", backup.Incidents)
	}

	// 7. Test Dedicated Create Status Page View (/admin/pages/new)
	reqNew := httptest.NewRequest("GET", "/admin/pages/new", nil)
	reqNew.AddCookie(&http.Cookie{Name: "admin_session", Value: token})
	recNew := httptest.NewRecorder()
	rt.ServeHTTP(recNew, reqNew)

	if recNew.Code != http.StatusOK {
		t.Fatalf("expected 200 for /admin/pages/new, got %d", recNew.Code)
	}
	if !strings.Contains(recNew.Body.String(), "Create Status Page Tenant") {
		t.Errorf("expected form with title 'Create Status Page Tenant' in response")
	}

	// 8. Test Duplicate Domain Validation (Human-friendly error, no crash)
	formDup := "name=Duplicate+Tenant&slug=dup-slug&domain=status.git.com" // status.git.com was already used by pageA
	reqDup := httptest.NewRequest("POST", "/admin/pages", strings.NewReader(formDup))
	reqDup.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqDup.AddCookie(&http.Cookie{Name: "admin_session", Value: token})
	recDup := httptest.NewRecorder()
	rt.ServeHTTP(recDup, reqDup)

	if recDup.Code != http.StatusOK {
		t.Fatalf("expected 200 re-rendering form on validation error, got %d", recDup.Code)
	}
	bodyDup := recDup.Body.String()
	if !strings.Contains(bodyDup, "already assigned to status page") {
		t.Errorf("expected duplicate domain friendly validation error message, got: %s", bodyDup)
	}
}
