package handler

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
)

type AdminSettingsHandler struct {
	settingsRepo *db.SettingsRepo
	tokenRepo    *db.APITokenRepo
	renderer     *TemplateRenderer
	logger       *logger.Logger
}

func NewAdminSettingsHandler(sRepo *db.SettingsRepo, tRepo *db.APITokenRepo, tr *TemplateRenderer, log *logger.Logger) *AdminSettingsHandler {
	return &AdminSettingsHandler{
		settingsRepo: sRepo,
		tokenRepo:    tRepo,
		renderer:     tr,
		logger:       log,
	}
}

func (h *AdminSettingsHandler) ServeSettings(w http.ResponseWriter, r *http.Request) {
	domain, _ := h.settingsRepo.GetAdminAllowedDomain()
	currentHost := ExtractRequestHost(r)

	var tokens []model.APIToken
	if h.tokenRepo != nil {
		if list, err := h.tokenRepo.GetAll(); err == nil {
			tokens = list
		}
	}

	data := map[string]any{
		"Title":              "System Settings & Security",
		"ActiveTab":          "settings",
		"AdminAllowedDomain": domain,
		"CurrentHost":        currentHost,
		"Saved":              r.URL.Query().Get("saved") == "1",
		"Tokens":             tokens,
		"NewToken":           r.URL.Query().Get("new_token"),
		"NewTokenName":       r.URL.Query().Get("new_token_name"),
		"TokenDeleted":       r.URL.Query().Get("token_deleted") == "1",
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

func (h *AdminSettingsHandler) ServeCreateToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = "API Token"
	}

	if h.tokenRepo == nil {
		http.Error(w, "api token repository unavailable", http.StatusInternalServerError)
		return
	}

	created, err := h.tokenRepo.Create(name)
	if err != nil {
		h.logger.Error("admin", "Failed to create API token: "+err.Error(), nil)
		http.Error(w, "failed to create token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.logger.Info("admin", fmt.Sprintf("Created new API token %q (id: %s, prefix: %s)", name, created.ID, created.TokenPrefix), nil)
	http.Redirect(w, r, "/admin/settings?new_token="+url.QueryEscape(created.Token)+"&new_token_name="+url.QueryEscape(name), http.StatusFound)
}

func (h *AdminSettingsHandler) ServeDeleteToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		// admin / settings / tokens / {id} / delete
		if len(parts) >= 4 {
			id = parts[3]
		}
	}

	if id == "" {
		http.Error(w, "missing token id", http.StatusBadRequest)
		return
	}

	if h.tokenRepo == nil {
		http.Error(w, "api token repository unavailable", http.StatusInternalServerError)
		return
	}

	if err := h.tokenRepo.Delete(id); err != nil {
		h.logger.Error("admin", fmt.Sprintf("Failed to delete API token %s: %v", id, err), nil)
		http.Error(w, "failed to delete token", http.StatusInternalServerError)
		return
	}

	h.logger.Info("admin", fmt.Sprintf("Deleted API token %s", id), nil)
	http.Redirect(w, r, "/admin/settings?token_deleted=1", http.StatusFound)
}
