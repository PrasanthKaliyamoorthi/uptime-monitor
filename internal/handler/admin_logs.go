package handler

import (
	"net/http"
	"strings"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
)

type AdminLogsHandler struct {
	logRepo       *db.LogRepo
	blacklistRepo *db.BlacklistRepo
	renderer      *TemplateRenderer
	logger        *logger.Logger
}

func NewAdminLogsHandler(lRepo *db.LogRepo, bRepo *db.BlacklistRepo, tr *TemplateRenderer, log *logger.Logger) *AdminLogsHandler {
	return &AdminLogsHandler{
		logRepo:       lRepo,
		blacklistRepo: bRepo,
		renderer:      tr,
		logger:        log,
	}
}

func (h *AdminLogsHandler) ServeLogs(w http.ResponseWriter, r *http.Request) {
	level := r.URL.Query().Get("level")
	if level == "" {
		level = "ALL"
	}
	source := r.URL.Query().Get("source")
	if source == "" {
		source = "ALL"
	}
	query := r.URL.Query().Get("q")

	logs, _ := h.logRepo.GetLogs(level, source, query, 100, 0)

	var blacklist []any
	if h.blacklistRepo != nil {
		if bl, err := h.blacklistRepo.GetAll(); err == nil {
			for _, item := range bl {
				blacklist = append(blacklist, item)
			}
		}
	}

	data := map[string]any{
		"Title":     "Logs & Diagnostics",
		"ActiveTab": "logs",
		"Logs":      logs,
		"Blacklist": blacklist,
		"Level":     level,
		"Source":    source,
		"Query":     query,
		"Unblocked": r.URL.Query().Get("unblocked") == "1",
	}
	_ = h.renderer.Render(w, "admin_logs", data)
}

func (h *AdminLogsHandler) ServeUnblockIP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ip := strings.TrimSpace(r.FormValue("ip"))
	if ip == "" {
		// Also check URL pattern /admin/blacklist/{ip}/unblock
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 4 && parts[1] == "blacklist" && parts[3] == "unblock" {
			ip = parts[2]
		}
	}

	if ip != "" && h.blacklistRepo != nil {
		if err := h.blacklistRepo.Remove(ip); err == nil {
			if h.logger != nil {
				h.logger.Info("auth", "Admin manually unblocked IP: "+ip, nil)
			}
		}
	}

	http.Redirect(w, r, "/admin/logs?unblocked=1", http.StatusFound)
}
