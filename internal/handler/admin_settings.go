package handler

import (
	"net/http"
	"strings"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
)

type AdminSettingsHandler struct {
	settingsRepo *db.SettingsRepo
	renderer     *TemplateRenderer
	logger       *logger.Logger
}

func NewAdminSettingsHandler(sRepo *db.SettingsRepo, tr *TemplateRenderer, log *logger.Logger) *AdminSettingsHandler {
	return &AdminSettingsHandler{
		settingsRepo: sRepo,
		renderer:     tr,
		logger:       log,
	}
}

func (h *AdminSettingsHandler) ServeSettings(w http.ResponseWriter, r *http.Request) {
	domain, _ := h.settingsRepo.GetAdminAllowedDomain()
	currentHost := ExtractRequestHost(r)

	data := map[string]any{
		"Title":              "System Settings & Security",
		"ActiveTab":          "settings",
		"AdminAllowedDomain": domain,
		"CurrentHost":        currentHost,
		"Saved":              r.URL.Query().Get("saved") == "1",
	}
	_ = h.renderer.Render(w, "admin_settings", data)
}

func (h *AdminSettingsHandler) ServeUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	domain := strings.TrimSpace(strings.ToLower(r.FormValue("admin_allowed_domain")))
	// Strip scheme if user typed https://
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	domain = ExtractHost(domain) // strip port or path if entered

	if err := h.settingsRepo.SetAdminAllowedDomain(domain); err != nil {
		h.logger.Error("admin", "Failed to update admin allowed domain: "+err.Error(), nil)
		http.Error(w, "failed to save settings: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if domain != "" {
		h.logger.Info("admin", "Updated admin console allowed domain to: "+domain, nil)
	} else {
		h.logger.Info("admin", "Cleared admin console allowed domain (accessible from any host)", nil)
	}

	http.Redirect(w, r, "/admin/settings?saved=1", http.StatusFound)
}
