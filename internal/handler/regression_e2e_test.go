package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"uptime-monitor/internal/checker"
	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
	"uptime-monitor/internal/notifier"
)

func TestComprehensiveRegressionEndToEnd(t *testing.T) {
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
	dispatcher := notifier.NewDispatcher(webhookRepo, sysLogger)
	rt.SetAuxHandlers(
		NewAdminIncidentsHandler(incidentRepo, dispatcher, tr, sysLogger),
		NewAdminWebhooksHandler(webhookRepo, dispatcher, tr, sysLogger),
	)

	// =========================================================================
	// Scenario 1: First-run setup & admin authentication
	// =========================================================================
	reqFirstRun := httptest.NewRequest("GET", "/", nil)
	recFirstRun := httptest.NewRecorder()
	rt.ServeHTTP(recFirstRun, reqFirstRun)
	if recFirstRun.Code != http.StatusFound || recFirstRun.Header().Get("Location") != "/setup" {
		t.Fatalf("expected redirect to /setup on empty db, got code %d, loc %s", recFirstRun.Code, recFirstRun.Header().Get("Location"))
	}

	// Create admin via setup
	setupForm := "username=admin&password=Password123!&confirm_password=Password123!"
	reqSetupPost := httptest.NewRequest("POST", "/setup", strings.NewReader(setupForm))
	reqSetupPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recSetupPost := httptest.NewRecorder()
	rt.ServeHTTP(recSetupPost, reqSetupPost)
	if recSetupPost.Code != http.StatusFound {
		t.Fatalf("expected 302 after setup, got %d", recSetupPost.Code)
	}

	adminCookie := recSetupPost.Result().Cookies()[0]
	if adminCookie.Name != "admin_session" {
		t.Fatalf("expected admin_session cookie, got %s", adminCookie.Name)
	}

	// =========================================================================
	// Scenario 2: Multi-Page Tenant Creation Across Different Scenarios
	// =========================================================================

	// Scenario 2A: Dedicated Custom Domain (status.git.local) with Catppuccin Mocha theme
	formPageA := "name=Git+Ecosystem&slug=git&domain=status.git.local&theme=catppuccin-mocha&description=Git+Source+and+CI"
	reqPageA := httptest.NewRequest("POST", "/admin/pages", strings.NewReader(formPageA))
	reqPageA.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPageA.AddCookie(adminCookie)
	recPageA := httptest.NewRecorder()
	rt.ServeHTTP(recPageA, reqPageA)
	if recPageA.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect to page A workspace, got %d", recPageA.Code)
	}
	pageA, err := statusPageRepo.GetBySlug("git")
	if err != nil || pageA == nil {
		t.Fatalf("failed to retrieve page A by slug: %v", err)
	}
	if pageA.Domain != "status.git.local" || pageA.Theme != "catppuccin-mocha" {
		t.Errorf("page A unexpected properties: domain=%s, theme=%s", pageA.Domain, pageA.Theme)
	}

	// Scenario 2B: Path-only tenant (no domain, slug: terminal) with Tokyo Night theme
	formPageB := "name=Terminal+Cloud&slug=terminal&domain=&theme=tokyo-night&description=CLI+and+Edge+runtimes"
	reqPageB := httptest.NewRequest("POST", "/admin/pages", strings.NewReader(formPageB))
	reqPageB.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPageB.AddCookie(adminCookie)
	recPageB := httptest.NewRecorder()
	rt.ServeHTTP(recPageB, reqPageB)
	if recPageB.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect to page B workspace, got %d", recPageB.Code)
	}
	pageB, err := statusPageRepo.GetBySlug("terminal")
	if err != nil || pageB == nil {
		t.Fatalf("failed to retrieve page B by slug: %v", err)
	}
	if pageB.Domain != "" {
		t.Errorf("expected empty domain for page B, got %s", pageB.Domain)
	}

	// Scenario 2C: Messy domain input (protocol, uppercase, port, trailing slash) -> auto-sanitized
	formPageC := "name=API+Platform&slug=api&domain=HTTPS://STATUS.API.INTERNAL:8443/&theme=nord&description=Public+APIs"
	reqPageC := httptest.NewRequest("POST", "/admin/pages", strings.NewReader(formPageC))
	reqPageC.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPageC.AddCookie(adminCookie)
	recPageC := httptest.NewRecorder()
	rt.ServeHTTP(recPageC, reqPageC)
	if recPageC.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect to page C workspace, got %d", recPageC.Code)
	}
	pageC, err := statusPageRepo.GetBySlug("api")
	if err != nil || pageC == nil {
		t.Fatalf("failed to retrieve page C: %v", err)
	}
	if pageC.Domain != "status.api.internal" {
		t.Errorf("expected sanitized domain 'status.api.internal', got '%s'", pageC.Domain)
	}

	// Scenario 2D: Fallback Default Page (is_default=true)
	formPageD := "name=Corporate+Cloud&slug=corp&domain=status.corp.internal&theme=dracula&is_default=true"
	reqPageD := httptest.NewRequest("POST", "/admin/pages", strings.NewReader(formPageD))
	reqPageD.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPageD.AddCookie(adminCookie)
	recPageD := httptest.NewRecorder()
	rt.ServeHTTP(recPageD, reqPageD)
	if recPageD.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect for default page, got %d", recPageD.Code)
	}
	pageD, err := statusPageRepo.GetBySlug("corp")
	if err != nil || pageD == nil || !pageD.IsDefault {
		t.Fatalf("expected page D to be designated default")
	}

	// =========================================================================
	// Scenario 3: Friendly Conflict & Duplicate Detection
	// =========================================================================

	// Test 3A: Attempt duplicate domain of Page A
	formDupDomain := "name=Duplicate+Git&slug=git-two&domain=status.git.local&theme=openstatus-slate"
	reqDupDomain := httptest.NewRequest("POST", "/admin/pages", strings.NewReader(formDupDomain))
	reqDupDomain.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqDupDomain.AddCookie(adminCookie)
	recDupDomain := httptest.NewRecorder()
	rt.ServeHTTP(recDupDomain, reqDupDomain)
	if recDupDomain.Code != http.StatusOK {
		t.Fatalf("expected 200 form re-render on duplicate domain, got %d", recDupDomain.Code)
	}
	bodyDupDomain := recDupDomain.Body.String()
	if !strings.Contains(bodyDupDomain, "already assigned to status page") || !strings.Contains(bodyDupDomain, "Git Ecosystem") {
		t.Errorf("expected duplicate domain friendly error message, got: %s", bodyDupDomain)
	}
	// Check inputs preserved
	if !strings.Contains(bodyDupDomain, `value="Duplicate Git"`) || !strings.Contains(bodyDupDomain, `value="git-two"`) {
		t.Errorf("expected form fields to be preserved upon domain conflict error")
	}

	// Test 3B: Attempt duplicate slug of Page B
	formDupSlug := "name=Another+Terminal&slug=terminal&domain=status.other.org&theme=openstatus-slate"
	reqDupSlug := httptest.NewRequest("POST", "/admin/pages", strings.NewReader(formDupSlug))
	reqDupSlug.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqDupSlug.AddCookie(adminCookie)
	recDupSlug := httptest.NewRecorder()
	rt.ServeHTTP(recDupSlug, reqDupSlug)
	if recDupSlug.Code != http.StatusOK {
		t.Fatalf("expected 200 form re-render on duplicate slug, got %d", recDupSlug.Code)
	}
	bodyDupSlug := recDupSlug.Body.String()
	if !strings.Contains(bodyDupSlug, "already in use by status page") || !strings.Contains(bodyDupSlug, "Terminal Cloud") {
		t.Errorf("expected duplicate slug friendly error message, got: %s", bodyDupSlug)
	}

	// =========================================================================
	// Scenario 4: Adding Monitored Endpoints to Specific Tenants
	// =========================================================================

	// Mock HTTP upstream for Git
	gitServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Env", "production")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy","service":"git"}`))
	}))
	defer gitServer.Close()

	// Add Endpoint to Tenant A (30 days history)
	formMonA := "status_page_id=" + pageA.ID + "&name=Git+HTTPS+Service&protocol=https&target=" + gitServer.URL + "&method=GET&expected_code=200&interval=60&timeout=10&max_days=30"
	reqMonA := httptest.NewRequest("POST", "/admin/monitors", strings.NewReader(formMonA))
	reqMonA.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqMonA.AddCookie(adminCookie)
	recMonA := httptest.NewRecorder()
	rt.ServeHTTP(recMonA, reqMonA)
	if recMonA.Code != http.StatusFound {
		t.Fatalf("expected 302 after monitor A creation, got %d", recMonA.Code)
	}

	monsA, _ := monitorRepo.GetByStatusPage(pageA.ID)
	if len(monsA) != 1 || monsA[0].Name != "Git HTTPS Service" {
		t.Fatalf("expected 1 monitor under page A, got %v", monsA)
	}
	monAID := monsA[0].ID

	// Mock Authenticated Terminal Server
	terminalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer secret-token-xyz" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("terminal ready"))
	}))
	defer terminalServer.Close()

	// Add Endpoint to Tenant B (Authenticated Bearer token, 15 days history)
	formMonB := "status_page_id=" + pageB.ID + "&name=Terminal+SSH+Gateway&protocol=http&target=" + terminalServer.URL + "&method=GET&expected_code=200&auth_type=bearer&auth_token=secret-token-xyz&interval=60&timeout=10&max_days=15"
	reqMonB := httptest.NewRequest("POST", "/admin/monitors", strings.NewReader(formMonB))
	reqMonB.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqMonB.AddCookie(adminCookie)
	recMonB := httptest.NewRecorder()
	rt.ServeHTTP(recMonB, reqMonB)
	if recMonB.Code != http.StatusFound {
		t.Fatalf("expected 302 after monitor B creation, got %d", recMonB.Code)
	}

	monsB, _ := monitorRepo.GetByStatusPage(pageB.ID)
	if len(monsB) != 1 || monsB[0].Name != "Terminal SSH Gateway" {
		t.Fatalf("expected 1 monitor under page B, got %v", monsB)
	}
	monBID := monsB[0].ID

	// Trigger Check Now on Monitor A
	reqCheckA := httptest.NewRequest("POST", "/admin/monitors/"+monAID+"/check", nil)
	reqCheckA.AddCookie(adminCookie)
	recCheckA := httptest.NewRecorder()
	rt.ServeHTTP(recCheckA, reqCheckA)
	if recCheckA.Code != http.StatusOK || !strings.Contains(recCheckA.Body.String(), "Check OK") {
		t.Errorf("expected Check OK for monitor A, got: %s", recCheckA.Body.String())
	}

	// Trigger Check Now on Monitor B
	reqCheckB := httptest.NewRequest("POST", "/admin/monitors/"+monBID+"/check", nil)
	reqCheckB.AddCookie(adminCookie)
	recCheckB := httptest.NewRecorder()
	rt.ServeHTTP(recCheckB, reqCheckB)
	if recCheckB.Code != http.StatusOK || !strings.Contains(recCheckB.Body.String(), "Check OK") {
		t.Errorf("expected Check OK for monitor B with bearer auth, got: %s", recCheckB.Body.String())
	}

	// Check Diagnostics Drawer on Monitor A
	reqDiag := httptest.NewRequest("GET", "/admin/monitors/"+monAID+"/diagnostics", nil)
	reqDiag.AddCookie(adminCookie)
	recDiag := httptest.NewRecorder()
	rt.ServeHTTP(recDiag, reqDiag)
	if recDiag.Code != http.StatusOK || !strings.Contains(recDiag.Body.String(), "Target Diagnostic Inspection") {
		t.Errorf("expected diagnostics waterfall for monitor A, got code %d", recDiag.Code)
	}

	// =========================================================================
	// Scenario 5: Multi-Stage Incident Lifecycle & Timelines
	// =========================================================================

	// Step 5A: Create Incident on Tenant A (investigating)
	formIncA := "status_page_id=" + pageA.ID + "&title=Git+Push+Degradation&status=investigating&severity=major&message=Investigating+elevated+timeouts+on+git+pushes"
	reqIncA := httptest.NewRequest("POST", "/admin/incidents", strings.NewReader(formIncA))
	reqIncA.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqIncA.AddCookie(adminCookie)
	recIncA := httptest.NewRecorder()
	rt.ServeHTTP(recIncA, reqIncA)
	if recIncA.Code != http.StatusFound {
		t.Fatalf("expected 302 after incident creation, got %d", recIncA.Code)
	}

	incsA, _ := incidentRepo.GetByPageID(pageA.ID)
	if len(incsA) != 1 {
		t.Fatalf("expected 1 incident under page A, got %d", len(incsA))
	}
	incAID := incsA[0].ID

	// Step 5B: Add Update 1 (identified)
	formUp1 := "status=identified&message=Identified+degraded+storage+volume+controller"
	reqUp1 := httptest.NewRequest("POST", "/admin/incidents/"+incAID+"/update", strings.NewReader(formUp1))
	reqUp1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqUp1.AddCookie(adminCookie)
	recUp1 := httptest.NewRecorder()
	rt.ServeHTTP(recUp1, reqUp1)

	// Step 5C: Add Update 2 (monitoring)
	formUp2 := "status=monitoring&message=Storage+failover+complete.+Monitoring+commit+queue"
	reqUp2 := httptest.NewRequest("POST", "/admin/incidents/"+incAID+"/update", strings.NewReader(formUp2))
	reqUp2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqUp2.AddCookie(adminCookie)
	recUp2 := httptest.NewRecorder()
	rt.ServeHTTP(recUp2, reqUp2)

	// Step 5D: Add Update 3 (resolved)
	formUp3 := "status=resolved&message=All+git+operations+operating+within+normal+SLAs"
	reqUp3 := httptest.NewRequest("POST", "/admin/incidents/"+incAID+"/update", strings.NewReader(formUp3))
	reqUp3.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqUp3.AddCookie(adminCookie)
	recUp3 := httptest.NewRecorder()
	rt.ServeHTTP(recUp3, reqUp3)

	// Verify all 4 updates exist on Incident A
	updatedIncA, err := incidentRepo.GetByID(incAID)
	if err != nil || len(updatedIncA.Updates) != 4 {
		t.Fatalf("expected 4 updates on incident A, got %d: %v", len(updatedIncA.Updates), err)
	}
	if updatedIncA.Status != model.IncidentStatusResolved {
		t.Errorf("expected incident A status 'resolved', got %s", updatedIncA.Status)
	}

	// Step 5E: Create Active Scheduled Maintenance on Tenant B
	formMaintB := "status_page_id=" + pageB.ID + "&title=SSH+Key+Rotation&status=monitoring&severity=maintenance&message=Scheduled+maintenance+for+host+key+rotation"
	reqMaintB := httptest.NewRequest("POST", "/admin/incidents", strings.NewReader(formMaintB))
	reqMaintB.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqMaintB.AddCookie(adminCookie)
	recMaintB := httptest.NewRecorder()
	rt.ServeHTTP(recMaintB, reqMaintB)
	if recMaintB.Code != http.StatusFound {
		t.Fatalf("expected 302 after maintenance creation, got %d", recMaintB.Code)
	}

	// =========================================================================
	// Scenario 6: Public Status Page Isolation & Past Incidents Timeline
	// =========================================================================

	// 6A: Query Tenant A via Host header (status.git.local)
	reqPublicA := httptest.NewRequest("GET", "/", nil)
	reqPublicA.Host = "status.git.local"
	recPublicA := httptest.NewRecorder()
	rt.ServeHTTP(recPublicA, reqPublicA)
	if recPublicA.Code != http.StatusOK {
		t.Fatalf("expected 200 for tenant A public view, got %d", recPublicA.Code)
	}
	htmlA := recPublicA.Body.String()

	// Tenant A Checks:
	if !strings.Contains(htmlA, "Git Ecosystem") {
		t.Errorf("tenant A must render title 'Git Ecosystem'")
	}
	if !strings.Contains(htmlA, "catppuccin-mocha") {
		t.Errorf("tenant A must apply theme 'catppuccin-mocha'")
	}
	if !strings.Contains(htmlA, "Git HTTPS Service") {
		t.Errorf("tenant A must display 'Git HTTPS Service' monitor")
	}
	// Past Incidents Timeline
	if !strings.Contains(htmlA, "Past Incidents") {
		t.Errorf("tenant A must render 'Past Incidents' timeline header")
	}
	if !strings.Contains(htmlA, "Git Push Degradation") {
		t.Errorf("tenant A must render resolved incident 'Git Push Degradation' in timeline")
	}
	if !strings.Contains(htmlA, "All git operations operating within normal SLAs") {
		t.Errorf("tenant A must render resolution update message")
	}
	if !strings.Contains(htmlA, "Identified degraded storage volume controller") {
		t.Errorf("tenant A must render progression update message")
	}
	// ISOLATION: Must NOT contain Tenant B's data!
	if strings.Contains(htmlA, "Terminal SSH Gateway") || strings.Contains(htmlA, "SSH Key Rotation") {
		t.Errorf("CRITICAL LEAK: Tenant A rendered Tenant B monitor or incident!")
	}

	// 6B: Query Tenant B via query parameter (?page=terminal)
	reqPublicB := httptest.NewRequest("GET", "/status?page=terminal", nil)
	recPublicB := httptest.NewRecorder()
	rt.ServeHTTP(recPublicB, reqPublicB)
	if recPublicB.Code != http.StatusOK {
		t.Fatalf("expected 200 for tenant B, got %d", recPublicB.Code)
	}
	htmlB := recPublicB.Body.String()

	if !strings.Contains(htmlB, "Terminal Cloud") {
		t.Errorf("tenant B must render 'Terminal Cloud'")
	}
	if !strings.Contains(htmlB, "tokyo-night") {
		t.Errorf("tenant B must apply theme 'tokyo-night'")
	}
	if !strings.Contains(htmlB, "Terminal SSH Gateway") {
		t.Errorf("tenant B must display 'Terminal SSH Gateway' monitor")
	}
	// Active Maintenance Banner
	if !strings.Contains(htmlB, "SSH Key Rotation") {
		t.Errorf("tenant B must display active maintenance banner 'SSH Key Rotation'")
	}
	// ISOLATION: Must NOT contain Tenant A's data!
	if strings.Contains(htmlB, "Git HTTPS Service") || strings.Contains(htmlB, "Git Push Degradation") {
		t.Errorf("CRITICAL LEAK: Tenant B rendered Tenant A monitor or incident!")
	}

	// 6C: Query Unknown Domain -> Verify fallback to default designated page (Corp)
	reqUnknown := httptest.NewRequest("GET", "/", nil)
	reqUnknown.Host = "random.nonexistent.domain.org"
	recUnknown := httptest.NewRecorder()
	rt.ServeHTTP(recUnknown, reqUnknown)
	if recUnknown.Code != http.StatusOK {
		t.Fatalf("expected 200 for fallback default page, got %d", recUnknown.Code)
	}
	if !strings.Contains(recUnknown.Body.String(), "Corporate Cloud") {
		t.Errorf("expected unknown host to route to default designated page 'Corporate Cloud'")
	}

	// 6D: RSS Feed per Tenant
	reqRSSA := httptest.NewRequest("GET", "/feed.xml", nil)
	reqRSSA.Host = "status.git.local"
	recRSSA := httptest.NewRecorder()
	rt.ServeHTTP(recRSSA, reqRSSA)
	if recRSSA.Code != http.StatusOK || !strings.Contains(recRSSA.Body.String(), "<rss") {
		t.Errorf("expected valid RSS XML for tenant A, got %s", recRSSA.Body.String())
	}
	if !strings.Contains(recRSSA.Body.String(), "Git Push Degradation") {
		t.Errorf("expected incident in tenant A RSS feed")
	}

	// 6E: Dynamic SVG Badges
	reqBadgeStatus := httptest.NewRequest("GET", "/api/badge/"+monAID+"/status", nil)
	recBadgeStatus := httptest.NewRecorder()
	rt.ServeHTTP(recBadgeStatus, reqBadgeStatus)
	if recBadgeStatus.Code != http.StatusOK || !strings.Contains(recBadgeStatus.Body.String(), "<svg") || !strings.Contains(recBadgeStatus.Body.String(), "operational") {
		t.Errorf("expected valid status SVG badge, got: %s", recBadgeStatus.Body.String())
	}

	reqBadgeUptime := httptest.NewRequest("GET", "/api/badge/"+monAID+"/uptime", nil)
	recBadgeUptime := httptest.NewRecorder()
	rt.ServeHTTP(recBadgeUptime, reqBadgeUptime)
	if recBadgeUptime.Code != http.StatusOK || !strings.Contains(recBadgeUptime.Body.String(), "<svg") || !strings.Contains(recBadgeUptime.Body.String(), "uptime") {
		t.Errorf("expected valid uptime SVG badge, got: %s", recBadgeUptime.Body.String())
	}

	// =========================================================================
	// Scenario 7: Per-Page Backup Export & Restore
	// =========================================================================

	// Export Tenant A
	reqExport := httptest.NewRequest("GET", "/admin/pages/"+pageA.ID+"/export", nil)
	reqExport.AddCookie(adminCookie)
	recExport := httptest.NewRecorder()
	rt.ServeHTTP(recExport, reqExport)
	if recExport.Code != http.StatusOK {
		t.Fatalf("expected 200 for backup export, got %d", recExport.Code)
	}

	var backupA model.PageBackupPayload
	if err := json.Unmarshal(recExport.Body.Bytes(), &backupA); err != nil {
		t.Fatalf("failed to decode exported JSON backup: %v", err)
	}
	if backupA.Version != 1 || backupA.StatusPage.Slug != "git" || len(backupA.Monitors) != 1 || len(backupA.Incidents) != 1 {
		t.Fatalf("unexpected backup payload content: %+v", backupA)
	}

	// Create fresh status page for restore test
	pageRestore := &model.StatusPage{
		Slug: "restore-target",
		Name: "Restore Target",
	}
	_ = statusPageRepo.Create(pageRestore)

	// Prepare multipart form with backup JSON
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	part, _ := w.CreateFormFile("backup_file", "backup.json")
	_, _ = part.Write(recExport.Body.Bytes())
	_ = w.Close()

	reqImport := httptest.NewRequest("POST", "/admin/pages/"+pageRestore.ID+"/import", &b)
	reqImport.Header.Set("Content-Type", w.FormDataContentType())
	reqImport.AddCookie(adminCookie)
	recImport := httptest.NewRecorder()
	rt.ServeHTTP(recImport, reqImport)
	if recImport.Code != http.StatusFound {
		t.Fatalf("expected 302 after import, got %d", recImport.Code)
	}

	restoredMons, _ := monitorRepo.GetByStatusPage(pageRestore.ID)
	if len(restoredMons) != 1 || restoredMons[0].Name != "Git HTTPS Service" {
		t.Errorf("expected 1 restored monitor under new page, got %v", restoredMons)
	}
	restoredIncs, _ := incidentRepo.GetByPageID(pageRestore.ID)
	if len(restoredIncs) != 1 || restoredIncs[0].Title != "Git Push Degradation" {
		t.Errorf("expected 1 restored incident under new page, got %v", restoredIncs)
	}

	// =========================================================================
	// Scenario 8: Central Admin Dashboard Overview & Integrity
	// =========================================================================
	reqDashboard := httptest.NewRequest("GET", "/admin", nil)
	reqDashboard.AddCookie(adminCookie)
	recDashboard := httptest.NewRecorder()
	rt.ServeHTTP(recDashboard, reqDashboard)
	if recDashboard.Code != http.StatusOK {
		t.Fatalf("expected 200 for dashboard, got %d", recDashboard.Code)
	}

	htmlDash := recDashboard.Body.String()
	// Must contain all created tenant names
	if !strings.Contains(htmlDash, "Git Ecosystem") {
		t.Errorf("dashboard missing tenant 'Git Ecosystem'")
	}
	if !strings.Contains(htmlDash, "Terminal Cloud") {
		t.Errorf("dashboard missing tenant 'Terminal Cloud'")
	}
	if !strings.Contains(htmlDash, "API Platform") {
		t.Errorf("dashboard missing tenant 'API Platform'")
	}
	if !strings.Contains(htmlDash, "Corporate Cloud") {
		t.Errorf("dashboard missing tenant 'Corporate Cloud'")
	}
	// Must show 0 down or operational summary without template crash
	if !strings.Contains(htmlDash, "All Clean") && !strings.Contains(htmlDash, "Issues & Outages") {
		t.Errorf("dashboard summary row missing or broken")
	}
	// Verify HTML closed properly
	if !strings.Contains(htmlDash, "</html>") {
		t.Errorf("dashboard HTML truncated prematurely")
	}
}

func TestIncidentLifecycleCustomDatesAndPageHistoryDays(t *testing.T) {
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
	dispatcher := notifier.NewDispatcher(webhookRepo, sysLogger)
	rt.SetAuxHandlers(
		NewAdminIncidentsHandler(incidentRepo, dispatcher, tr, sysLogger),
		NewAdminWebhooksHandler(webhookRepo, dispatcher, tr, sysLogger),
	)

	// 1. Setup admin
	setupForm := "username=admin&password=Password123!&confirm_password=Password123!"
	reqSetupPost := httptest.NewRequest("POST", "/setup", strings.NewReader(setupForm))
	reqSetupPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recSetupPost := httptest.NewRecorder()
	rt.ServeHTTP(recSetupPost, reqSetupPost)
	adminCookie := recSetupPost.Result().Cookies()[0]

	// 2. Create Status Page with history_days = 14
	formPage := "name=Infra+Core&slug=infra&domain=status.infra.local&theme=openstatus-slate&history_days=14"
	reqPage := httptest.NewRequest("POST", "/admin/pages", strings.NewReader(formPage))
	reqPage.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPage.AddCookie(adminCookie)
	recPage := httptest.NewRecorder()
	rt.ServeHTTP(recPage, reqPage)
	if recPage.Code != http.StatusFound {
		t.Fatalf("expected 302 after page creation, got %d", recPage.Code)
	}

	page, err := statusPageRepo.GetBySlug("infra")
	if err != nil || page == nil {
		t.Fatalf("failed to retrieve page: %v", err)
	}
	if page.HistoryDays != 14 {
		t.Errorf("expected page.HistoryDays = 14, got %d", page.HistoryDays)
	}

	// 3. Create Incident with backdated created_at
	formInc := "status_page_id=" + page.ID + "&title=Core+DB+Lag&status=investigating&severity=major&created_at=2026-11-01T10:00&message=Replication+lag+spiked+to+45s"
	reqInc := httptest.NewRequest("POST", "/admin/incidents", strings.NewReader(formInc))
	reqInc.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqInc.AddCookie(adminCookie)
	recInc := httptest.NewRecorder()
	rt.ServeHTTP(recInc, reqInc)
	if recInc.Code != http.StatusFound {
		t.Fatalf("expected 302 after incident creation, got %d", recInc.Code)
	}

	incs, _ := incidentRepo.GetByPageID(page.ID)
	if len(incs) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(incs))
	}
	inc := incs[0]
	if inc.CreatedAt.Format("2006-01-02T15:04") != "2026-11-01T10:00" {
		t.Errorf("expected backdated created_at '2026-11-01T10:00', got '%s'", inc.CreatedAt.Format("2006-01-02T15:04"))
	}
	if len(inc.Updates) != 1 || inc.Updates[0].CreatedAt.Format("2006-01-02T15:04") != "2026-11-01T10:00" {
		t.Errorf("expected initial update to inherit created_at, got updates: %+v", inc.Updates)
	}

	// 4. Add retroactive update with custom time
	formUp := "status=monitoring&message=Applied+index+patch;+replication+caught+up&created_at=2026-11-01T11:30"
	reqUp := httptest.NewRequest("POST", "/admin/incidents/"+inc.ID+"/update", strings.NewReader(formUp))
	reqUp.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqUp.AddCookie(adminCookie)
	recUp := httptest.NewRecorder()
	rt.ServeHTTP(recUp, reqUp)

	incWithUp, _ := incidentRepo.GetByID(inc.ID)
	if len(incWithUp.Updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(incWithUp.Updates))
	}
	if incWithUp.Updates[0].CreatedAt.Format("2006-01-02T15:04") != "2026-11-01T11:30" {
		t.Errorf("expected retroactive update time '2026-11-01T11:30', got '%s'", incWithUp.Updates[0].CreatedAt.Format("2006-01-02T15:04"))
	}

	// 5. Update overall incident later via /edit
	formEdit := "title=Core+DB+Replication+Lag+%26+Maintenance&severity=maintenance&status=resolved&created_at=2026-11-01T09:45&resolved_at=2026-11-01T12:00&scheduled_start=2026-11-01T09:30&scheduled_end=2026-11-01T12:15"
	reqEdit := httptest.NewRequest("POST", "/admin/incidents/"+inc.ID+"/edit", strings.NewReader(formEdit))
	reqEdit.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqEdit.AddCookie(adminCookie)
	recEdit := httptest.NewRecorder()
	rt.ServeHTTP(recEdit, reqEdit)
	if recEdit.Code != http.StatusFound {
		t.Fatalf("expected 302 after incident edit, got %d", recEdit.Code)
	}

	updatedInc, _ := incidentRepo.GetByID(inc.ID)
	if updatedInc.Title != "Core DB Replication Lag & Maintenance" {
		t.Errorf("unexpected updated title: %s", updatedInc.Title)
	}
	if updatedInc.Status != model.IncidentStatusResolved {
		t.Errorf("expected resolved status, got: %s", updatedInc.Status)
	}
	if !updatedInc.IsMaintenance {
		t.Errorf("expected is_maintenance = true")
	}
	if updatedInc.CreatedAt.Format("2006-01-02T15:04") != "2026-11-01T09:45" {
		t.Errorf("expected updated created_at '2026-11-01T09:45', got '%s'", updatedInc.CreatedAt.Format("2006-01-02T15:04"))
	}
	if updatedInc.ResolvedAt == nil || updatedInc.ResolvedAt.Format("2006-01-02T15:04") != "2026-11-01T12:00" {
		t.Errorf("expected resolved_at '2026-11-01T12:00', got '%v'", updatedInc.ResolvedAt)
	}

	// 6. Query public status page: verify 14 days and timeline resolution
	reqPublic := httptest.NewRequest("GET", "/status?page=infra", nil)
	recPublic := httptest.NewRecorder()
	rt.ServeHTTP(recPublic, reqPublic)
	if recPublic.Code != http.StatusOK {
		t.Fatalf("expected 200 for public page, got %d", recPublic.Code)
	}
	publicHTML := recPublic.Body.String()
	if !strings.Contains(publicHTML, "Past 14 Days") {
		t.Errorf("expected public page to show 'Past 14 Days', body: %s", publicHTML)
	}
	if !strings.Contains(publicHTML, "Past Incidents") || !strings.Contains(publicHTML, "Core DB Replication Lag &amp; Maintenance") {
		t.Errorf("expected past incident in timeline, body: %s", publicHTML)
	}

	// 7. Update status page history_days setting to 30
	formSettings := "name=Infra+Core&slug=infra&domain=status.infra.local&theme=openstatus-slate&history_days=30"
	reqSettings := httptest.NewRequest("POST", "/admin/pages/"+page.ID+"/settings", strings.NewReader(formSettings))
	reqSettings.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqSettings.AddCookie(adminCookie)
	recSettings := httptest.NewRecorder()
	rt.ServeHTTP(recSettings, reqSettings)

	pageAfter, _ := statusPageRepo.GetByID(page.ID)
	if pageAfter.HistoryDays != 30 {
		t.Errorf("expected updated page.HistoryDays = 30, got %d", pageAfter.HistoryDays)
	}

	// Query public status page again: verify now 30 days
	reqPublic30 := httptest.NewRequest("GET", "/status?page=infra", nil)
	recPublic30 := httptest.NewRecorder()
	rt.ServeHTTP(recPublic30, reqPublic30)
	publicHTML30 := recPublic30.Body.String()
	if !strings.Contains(publicHTML30, "Past 30 Days") {
		t.Errorf("expected public page to show 'Past 30 Days', body: %s", publicHTML30)
	}
}

func TestStatusPageLogoUploadAndDisplay(t *testing.T) {
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

	// 1. Setup admin
	setupForm := "username=admin&password=Password123!&confirm_password=Password123!"
	reqSetupPost := httptest.NewRequest("POST", "/setup", strings.NewReader(setupForm))
	reqSetupPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recSetupPost := httptest.NewRecorder()
	rt.ServeHTTP(recSetupPost, reqSetupPost)
	adminCookie := recSetupPost.Result().Cookies()[0]

	// 2. Create Status Page with Logo URL
	formPage := "name=SagaSoft+Status&slug=sagasoft&domain=status.sagasoft.xyz&theme=openstatus-slate&logo_url=https://sagasoft.xyz/assets/logo.png"
	reqPage := httptest.NewRequest("POST", "/admin/pages", strings.NewReader(formPage))
	reqPage.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPage.AddCookie(adminCookie)
	recPage := httptest.NewRecorder()
	rt.ServeHTTP(recPage, reqPage)
	if recPage.Code != http.StatusFound {
		t.Fatalf("expected 302 after page creation, got %d", recPage.Code)
	}

	page, err := statusPageRepo.GetBySlug("sagasoft")
	if err != nil || page == nil {
		t.Fatalf("failed to retrieve page: %v", err)
	}
	if page.LogoURL != "https://sagasoft.xyz/assets/logo.png" {
		t.Errorf("expected logo URL saved, got: %s", page.LogoURL)
	}

	// 3. Verify public status page renders the logo next to title
	reqPublic := httptest.NewRequest("GET", "/status?page=sagasoft", nil)
	recPublic := httptest.NewRecorder()
	rt.ServeHTTP(recPublic, reqPublic)
	html := recPublic.Body.String()
	if !strings.Contains(html, `<img src="https://sagasoft.xyz/assets/logo.png"`) {
		t.Errorf("expected public header to render logo img tag, got html: %s", html)
	}

	// 4. Update status page with uploaded image file via multipart form data
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	_ = w.WriteField("name", "SagaSoft Status")
	_ = w.WriteField("slug", "sagasoft")
	_ = w.WriteField("domain", "status.sagasoft.xyz")
	_ = w.WriteField("theme", "openstatus-slate")
	_ = w.WriteField("history_days", "60")

	// Create fake SVG file part
	fileWriter, _ := w.CreateFormFile("logo_file", "logo.svg")
	svgContent := `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><circle cx="50" cy="50" r="40" fill="green"/></svg>`
	_, _ = fileWriter.Write([]byte(svgContent))
	_ = w.Close()

	reqUpload := httptest.NewRequest("POST", "/admin/pages/"+page.ID+"/settings", &b)
	reqUpload.Header.Set("Content-Type", w.FormDataContentType())
	reqUpload.AddCookie(adminCookie)
	recUpload := httptest.NewRecorder()
	rt.ServeHTTP(recUpload, reqUpload)
	if recUpload.Code != http.StatusFound {
		t.Fatalf("expected 302 after settings update, got %d", recUpload.Code)
	}

	pageUpdated, _ := statusPageRepo.GetByID(page.ID)
	if !strings.HasPrefix(pageUpdated.LogoURL, "data:image/svg+xml;base64,") && !strings.HasPrefix(pageUpdated.LogoURL, "data:image/") {
		t.Errorf("expected data URL logo after file upload, got: %s", pageUpdated.LogoURL)
	}

	// Verify public status page renders uploaded base64 data URI
	reqPublic2 := httptest.NewRequest("GET", "/status?page=sagasoft", nil)
	recPublic2 := httptest.NewRecorder()
	rt.ServeHTTP(recPublic2, reqPublic2)
	if !strings.Contains(recPublic2.Body.String(), "PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmci") {
		t.Errorf("expected public header to render uploaded base64 logo")
	}

	// 5. Remove logo
	formRemove := "name=SagaSoft+Status&slug=sagasoft&domain=status.sagasoft.xyz&theme=openstatus-slate&history_days=60&remove_logo=true"
	reqRemove := httptest.NewRequest("POST", "/admin/pages/"+page.ID+"/settings", strings.NewReader(formRemove))
	reqRemove.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqRemove.AddCookie(adminCookie)
	recRemove := httptest.NewRecorder()
	rt.ServeHTTP(recRemove, reqRemove)

	pageCleared, _ := statusPageRepo.GetByID(page.ID)
	if pageCleared.LogoURL != "" {
		t.Errorf("expected empty logo after removal, got: %s", pageCleared.LogoURL)
	}

	// Verify public status page does not render an empty logo img
	recPublic3 := httptest.NewRecorder()
	rt.ServeHTTP(recPublic3, reqPublic)
	if strings.Contains(recPublic3.Body.String(), `<img src=""`) || strings.Contains(recPublic3.Body.String(), `alt="SagaSoft Status" class="h-14`) {
		t.Errorf("expected public header without logo img tag after removal")
	}
}
