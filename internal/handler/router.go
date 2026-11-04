package handler

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"uptime-monitor/internal/checker"
	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/web"
)

type Router struct {
	adminRepo      *db.AdminRepo
	blacklistRepo  *db.BlacklistRepo
	settingsRepo   *db.SettingsRepo
	statusPageRepo *db.StatusPageRepo
	sessionManager *SessionManager

	setupHandler         *SetupHandler
	authHandler          *AuthHandler
	statusPageHandler    *StatusPageHandler
	badgeHandler         *BadgeHandler
	adminMonitorsHandler *AdminMonitorsHandler
	adminPagesHandler    *AdminPagesHandler
	adminIncidentsHandler *AdminIncidentsHandler
	adminLogsHandler     *AdminLogsHandler
	adminWebhooksHandler *AdminWebhooksHandler
	adminBackupHandler   *AdminBackupHandler
	adminSettingsHandler *AdminSettingsHandler

	staticHandler http.Handler
}

func NewRouter(
	aRepo *db.AdminRepo,
	bRepo *db.BlacklistRepo,
	mRepo *db.MonitorRepo,
	cRepo *db.CheckRepo,
	spRepo *db.StatusPageRepo,
	incRepo *db.IncidentRepo,
	wRepo *db.WebhookRepo,
	lRepo *db.LogRepo,
	sched *checker.Scheduler,
	disp interface{}, // dispatcher
	log *logger.Logger,
	maxLoginAttempts ...int,
) (*Router, error) {
	var sessRepo *db.SessionRepo
	var settingsRepo *db.SettingsRepo
	if aRepo != nil && aRepo.DB() != nil {
		sessRepo = db.NewSessionRepo(aRepo.DB())
		settingsRepo = db.NewSettingsRepo(aRepo.DB())
	}
	sm := NewSessionManager(sessRepo)
	tr, err := NewTemplateRenderer()
	if err != nil {
		return nil, err
	}

	// Prepare static file server
	staticSub, err := fs.Sub(web.Files, "static")
	if err != nil {
		return nil, err
	}
	staticServer := http.StripPrefix("/static/", http.FileServer(http.FS(staticSub)))

	notifDispatcher := disp.(*checker.Coordinator) // or notifier.Dispatcher
	_ = notifDispatcher

	maxAttempts := 5
	if len(maxLoginAttempts) > 0 && maxLoginAttempts[0] > 0 {
		maxAttempts = maxLoginAttempts[0]
	}

	return &Router{
		adminRepo:            aRepo,
		blacklistRepo:        bRepo,
		settingsRepo:         settingsRepo,
		statusPageRepo:       spRepo,
		sessionManager:       sm,
		setupHandler:         NewSetupHandler(aRepo, spRepo, sm, tr, log),
		authHandler:          NewAuthHandler(aRepo, bRepo, sm, tr, log, maxAttempts),
		statusPageHandler:    NewStatusPageHandler(spRepo, mRepo, cRepo, incRepo, settingsRepo, tr),
		badgeHandler:         NewBadgeHandler(mRepo, cRepo),
		adminMonitorsHandler: NewAdminMonitorsHandler(mRepo, cRepo, spRepo, sched, tr, log),
		adminPagesHandler:    NewAdminPagesHandler(spRepo, mRepo, incRepo, tr, log),
		adminIncidentsHandler: nil, // set below
		adminLogsHandler:     NewAdminLogsHandler(lRepo, bRepo, tr, log),
		adminWebhooksHandler: nil, // set below
		adminBackupHandler:   NewAdminBackupHandler(mRepo, spRepo, wRepo, tr, log),
		adminSettingsHandler: NewAdminSettingsHandler(settingsRepo, tr, log),
		staticHandler:        staticServer,
	}, nil
}

func (rt *Router) SetAuxHandlers(inc *AdminIncidentsHandler, webhooks *AdminWebhooksHandler) {
	rt.adminIncidentsHandler = inc
	rt.adminWebhooksHandler = webhooks
}

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// 1. Static file delivery
	if strings.HasPrefix(path, "/static/") {
		rt.staticHandler.ServeHTTP(w, r)
		return
	}

	// Admin Allowed Domain Gate:
	// If an admin allowed domain is configured, restrict admin console access strictly to that domain.
	// If accessed from any other domain/host, return 404 Not Found to avoid leaking admin existence or domain name.
	if strings.HasPrefix(path, "/admin") || path == "/setup" {
		if rt.settingsRepo != nil {
			allowedDomain, err := rt.settingsRepo.GetAdminAllowedDomain()
			if err == nil {
				allowedDomain = strings.TrimSpace(strings.ToLower(allowedDomain))
				if allowedDomain != "" {
					reqHost := strings.ToLower(ExtractRequestHost(r))
					if reqHost != allowedDomain {
						http.NotFound(w, r)
						return
					}
				}
			}
		}
	}

	// Admin IP Blacklist Gate:
	// Only admin console paths (/admin, /admin/*) are protected. Public status pages, badges, and RSS remain accessible.
	if strings.HasPrefix(path, "/admin") {
		clientIP := GetClientIP(r)
		if rt.blacklistRepo != nil {
			if isBlocked, err := rt.blacklistRepo.IsBlacklisted(clientIP); err == nil && isBlocked {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprintf(w, "Access Denied: Your IP (%s) has been blacklisted from the admin console due to excessive failed login attempts.\nTo unblock this IP, run:\n  ./uptime-monitor -unblock-ip %s\n", clientIP, clientIP)
				return
			}
		}
	}

	// 2. Initial Setup Guard
	hasAdmin, err := rt.adminRepo.HasAdmin()
	if err == nil && !hasAdmin {
		if path != "/setup" {
			http.Redirect(w, r, "/setup", http.StatusFound)
			return
		}
		rt.setupHandler.ServeHTTP(w, r)
		return
	}

	// 3. Setup route when admin already exists
	if path == "/setup" {
		rt.setupHandler.ServeHTTP(w, r)
		return
	}

	// 4. SVG Badges
	if strings.HasPrefix(path, "/api/badge/") {
		if strings.HasSuffix(path, "/status") {
			rt.badgeHandler.ServeStatusBadge(w, r)
			return
		}
		if strings.HasSuffix(path, "/uptime") {
			rt.badgeHandler.ServeUptimeBadge(w, r)
			return
		}
	}

	// 5. Admin Authentication routes
	if path == "/admin/login" {
		rt.authHandler.ServeLogin(w, r)
		return
	}
	if path == "/admin/logout" {
		rt.authHandler.ServeLogout(w, r)
		return
	}

	// 6. Admin Area Routing (Protected)
	if strings.HasPrefix(path, "/admin") {
		redirectToLogin := func() {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/admin/login")
				w.WriteHeader(http.StatusOK)
				return
			}
			http.Redirect(w, r, "/admin/login", http.StatusFound)
		}

		// Verify session
		c, err := r.Cookie("admin_session")
		if err != nil {
			redirectToLogin()
			return
		}
		if _, ok := rt.sessionManager.Get(c.Value); !ok {
			redirectToLogin()
			return
		}

		rt.routeAdmin(w, r, path)
		return
	}

	// 7. Status Page Domain & Query Resolution
	host := ExtractHost(r.Host)
	page, _ := rt.statusPageRepo.FindByDomain(host)
	if page == nil {
		if qPage := r.URL.Query().Get("page"); qPage != "" {
			page, _ = rt.statusPageRepo.FindByIDOrSlug(qPage)
		}
	}
	if page == nil {
		page, _ = rt.statusPageRepo.GetDefault()
	}

	if page != nil {
		if path == "/feed.xml" {
			rt.statusPageHandler.ServeRSSFeed(w, r, page)
			return
		}
		if path == "/" || path == "/status" {
			rt.statusPageHandler.ServeStatusPage(w, r, page)
			return
		}
	}

	// If no status page exists at all and path is /, redirect to admin
	if path == "/" {
		http.Redirect(w, r, "/admin", http.StatusFound)
		return
	}

	http.NotFound(w, r)
}

func (rt *Router) routeAdmin(w http.ResponseWriter, r *http.Request, path string) {
	// Dashboard: Overall status summary across all status pages / tenants
	if path == "/admin" {
		rt.adminPagesHandler.ServeDashboard(w, r)
		return
	}

	// Status Pages (Tenants)
	if path == "/admin/pages/new" {
		if r.Method == http.MethodPost {
			rt.adminPagesHandler.ServeCreate(w, r)
			return
		}
		rt.adminPagesHandler.ServeNew(w, r)
		return
	}
	if path == "/admin/pages" {
		if r.Method == http.MethodPost {
			rt.adminPagesHandler.ServeCreate(w, r)
			return
		}
		http.Redirect(w, r, "/admin", http.StatusFound)
		return
	}
	if strings.HasPrefix(path, "/admin/pages/") {
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) >= 3 {
			pageID := parts[2]
			if len(parts) >= 4 {
				action := parts[3]
				switch action {
				case "export":
					rt.adminPagesHandler.ServeExportPage(w, r, pageID)
					return
				case "import":
					if r.Method == http.MethodPost {
						rt.adminPagesHandler.ServeImportPage(w, r, pageID)
						return
					}
				case "settings":
					if r.Method == http.MethodPost {
						rt.adminPagesHandler.ServeUpdateSettings(w, r, pageID)
						return
					}
				case "delete":
					if r.Method == http.MethodPost {
						rt.adminPagesHandler.ServeDelete(w, r)
						return
					}
				}
			} else {
				// Workspace for this status page: /admin/pages/{id}
				rt.adminPagesHandler.ServePageWorkspace(w, r, pageID)
				return
			}
		}
	}

	// Monitors
	if path == "/admin/monitors" {
		if r.Method == http.MethodPost {
			rt.adminMonitorsHandler.ServeCreate(w, r)
			return
		}
		rt.adminMonitorsHandler.ServeList(w, r)
		return
	}
	if strings.HasPrefix(path, "/admin/monitors/") {
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) >= 3 {
			if len(parts) == 3 && r.Method == http.MethodGet {
				rt.adminMonitorsHandler.ServeGet(w, r)
				return
			}
			if len(parts) >= 4 {
				action := parts[3]
				switch action {
				case "check":
					if r.Method == http.MethodPost {
						rt.adminMonitorsHandler.ServeTriggerCheck(w, r)
						return
					}
				case "toggle":
					if r.Method == http.MethodPost {
						rt.adminMonitorsHandler.ServeToggle(w, r)
						return
					}
				case "edit":
					if r.Method == http.MethodPost {
						rt.adminMonitorsHandler.ServeUpdate(w, r)
						return
					}
				case "diagnostics":
					rt.adminMonitorsHandler.ServeDiagnostics(w, r)
					return
				case "delete":
					if r.Method == http.MethodPost {
						rt.adminMonitorsHandler.ServeDelete(w, r)
						return
					}
				}
			}
		}
	}

	// Incidents
	if path == "/admin/incidents" && rt.adminIncidentsHandler != nil {
		if r.Method == http.MethodPost {
			rt.adminIncidentsHandler.ServeCreate(w, r)
			return
		}
		http.Redirect(w, r, "/admin", http.StatusFound)
		return
	}
	if strings.HasPrefix(path, "/admin/incidents/") && rt.adminIncidentsHandler != nil {
		if strings.HasSuffix(path, "/update") && r.Method == http.MethodPost {
			rt.adminIncidentsHandler.ServeUpdate(w, r)
			return
		}
		if strings.HasSuffix(path, "/edit") && r.Method == http.MethodPost {
			rt.adminIncidentsHandler.ServeEdit(w, r)
			return
		}
		if strings.HasSuffix(path, "/delete") && r.Method == http.MethodPost {
			rt.adminIncidentsHandler.ServeDelete(w, r)
			return
		}
	}

	// Logs
	if path == "/admin/logs" {
		rt.adminLogsHandler.ServeLogs(w, r)
		return
	}
	// Blacklist unblock
	if path == "/admin/blacklist/unblock" || (strings.HasPrefix(path, "/admin/blacklist/") && strings.HasSuffix(path, "/unblock")) {
		rt.adminLogsHandler.ServeUnblockIP(w, r)
		return
	}

	// Webhooks
	if path == "/admin/webhooks" && rt.adminWebhooksHandler != nil {
		if r.Method == http.MethodPost {
			rt.adminWebhooksHandler.ServeCreate(w, r)
			return
		}
		rt.adminWebhooksHandler.ServeList(w, r)
		return
	}
	if strings.HasPrefix(path, "/admin/webhooks/") && rt.adminWebhooksHandler != nil {
		if strings.HasSuffix(path, "/test") && r.Method == http.MethodPost {
			rt.adminWebhooksHandler.ServeTest(w, r)
			return
		}
		if strings.HasSuffix(path, "/edit") && r.Method == http.MethodPost {
			rt.adminWebhooksHandler.ServeUpdate(w, r)
			return
		}
		if strings.HasSuffix(path, "/delete") && r.Method == http.MethodPost {
			rt.adminWebhooksHandler.ServeDelete(w, r)
			return
		}
		if r.Method == http.MethodGet {
			rt.adminWebhooksHandler.ServeGet(w, r)
			return
		}
	}

	// Backup
	if path == "/admin/backup" {
		rt.adminBackupHandler.ServePage(w, r)
		return
	}
	if path == "/admin/backup/export" {
		rt.adminBackupHandler.ServeExport(w, r)
		return
	}
	if path == "/admin/backup/import" && r.Method == http.MethodPost {
		rt.adminBackupHandler.ServeImport(w, r)
		return
	}

	// Settings
	if path == "/admin/settings" {
		if r.Method == http.MethodPost {
			rt.adminSettingsHandler.ServeUpdate(w, r)
			return
		}
		rt.adminSettingsHandler.ServeSettings(w, r)
		return
	}

	http.NotFound(w, r)
}
