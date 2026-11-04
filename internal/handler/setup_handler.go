package handler

import (
	"net/http"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
)

type SetupHandler struct {
	adminRepo      *db.AdminRepo
	statusPageRepo *db.StatusPageRepo
	sessionManager *SessionManager
	renderer       *TemplateRenderer
	logger         *logger.Logger
}

func NewSetupHandler(
	aRepo *db.AdminRepo,
	spRepo *db.StatusPageRepo,
	sm *SessionManager,
	tr *TemplateRenderer,
	log *logger.Logger,
) *SetupHandler {
	return &SetupHandler{
		adminRepo:      aRepo,
		statusPageRepo: spRepo,
		sessionManager: sm,
		renderer:       tr,
		logger:         log,
	}
}

func (h *SetupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hasAdmin, err := h.adminRepo.HasAdmin()
	if err == nil && hasAdmin {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}

	if r.Method == http.MethodGet {
		_ = h.renderer.Render(w, "setup", map[string]any{
			"Title": "Initial Setup",
		})
		return
	}

	if r.Method == http.MethodPost {
		username := r.FormValue("username")
		password := r.FormValue("password")
		confirm := r.FormValue("confirm_password")

		if password != confirm {
			_ = h.renderer.Render(w, "setup", map[string]any{
				"Title": "Initial Setup",
				"Error": "Passwords do not match.",
			})
			return
		}

		admin, err := h.adminRepo.CreateAdmin(username, password)
		if err != nil {
			_ = h.renderer.Render(w, "setup", map[string]any{
				"Title": "Initial Setup",
				"Error": "Failed to create administrator: " + err.Error(),
			})
			return
		}

		h.logger.Info("auth", "Master administrator account created: "+admin.Username, nil)

		// Create default status page if none exists
		existingPage, _ := h.statusPageRepo.GetDefault()
		if existingPage == nil {
			_ = h.statusPageRepo.Create(&model.StatusPage{
				Slug:        "default",
				Name:        "System Status",
				Description: "Live availability and performance metrics for all monitored services.",
				Theme:       model.ThemeOpenStatusSlate,
				IsDefault:   true,
			})
		}

		// Set authenticated session cookie
		token := h.sessionManager.Create(admin.ID, admin.Username)
		http.SetCookie(w, &http.Cookie{
			Name:     "admin_session",
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   86400,
		})

		http.Redirect(w, r, "/admin", http.StatusFound)
	}
}
