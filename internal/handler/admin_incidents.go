package handler

import (
	"net/http"
	"strings"
	"time"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
	"uptime-monitor/internal/notifier"
)

type AdminIncidentsHandler struct {
	incidentRepo *db.IncidentRepo
	notifier     *notifier.Dispatcher
	renderer     *TemplateRenderer
	logger       *logger.Logger
}

func NewAdminIncidentsHandler(
	incRepo *db.IncidentRepo,
	notif *notifier.Dispatcher,
	tr *TemplateRenderer,
	log *logger.Logger,
) *AdminIncidentsHandler {
	return &AdminIncidentsHandler{
		incidentRepo: incRepo,
		notifier:     notif,
		renderer:     tr,
		logger:       log,
	}
}

func (h *AdminIncidentsHandler) ServeList(w http.ResponseWriter, r *http.Request) {
	incidents, _ := h.incidentRepo.GetAll()

	data := map[string]any{
		"Title":     "Incidents & Maintenance",
		"ActiveTab": "incidents",
		"Incidents": incidents,
	}
	_ = h.renderer.Render(w, "admin_incidents", data)
}

func (h *AdminIncidentsHandler) ServeCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	status := strings.TrimSpace(r.FormValue("status"))
	severity := strings.TrimSpace(r.FormValue("severity"))
	message := strings.TrimSpace(r.FormValue("message"))
	spID := strings.TrimSpace(r.FormValue("status_page_id"))
	isMaintenance := severity == "maintenance"

	inc := &model.Incident{
		StatusPageID:  spID,
		Title:         title,
		Status:        status,
		Severity:      severity,
		IsMaintenance: isMaintenance,
	}

	if customCreated, err := ParseDateTime(r.FormValue("created_at")); err == nil {
		inc.CreatedAt = customCreated
	}
	if start, err := ParseDateTime(r.FormValue("scheduled_start")); err == nil {
		inc.ScheduledStart = &start
	}
	if end, err := ParseDateTime(r.FormValue("scheduled_end")); err == nil {
		inc.ScheduledEnd = &end
	}
	if resolved, err := ParseDateTime(r.FormValue("resolved_at")); err == nil {
		inc.ResolvedAt = &resolved
	}

	if err := h.incidentRepo.Create(inc); err != nil {
		h.logger.Error("admin", "Failed to create incident: "+err.Error(), nil)
	} else {
		_ = h.incidentRepo.AddUpdateWithTime(inc.ID, status, message, inc.CreatedAt)
		h.logger.Info("admin", "Published incident: "+inc.Title, map[string]any{"id": inc.ID, "severity": inc.Severity})

		// Dispatch alert
		if h.notifier != nil {
			h.notifier.Notify(r.Context(), model.WebhookContext{
				Event:         "incident",
				IncidentTitle: inc.Title,
				Status:        inc.Status,
				ErrorMessage:  message,
			})
		}
	}

	redirectURL := "/admin"
	if spID != "" {
		redirectURL = "/admin/pages/" + spID
	} else if ref := r.Header.Get("Referer"); ref != "" {
		redirectURL = ref
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *AdminIncidentsHandler) ServeUpdate(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	incidentID := parts[2]

	status := strings.TrimSpace(r.FormValue("status"))
	message := strings.TrimSpace(r.FormValue("message"))

	inc, _ := h.incidentRepo.GetByID(incidentID)

	updateTime := time.Now().UTC()
	if customTime, err := ParseDateTime(r.FormValue("created_at")); err == nil {
		updateTime = customTime
	}

	if err := h.incidentRepo.AddUpdateWithTime(incidentID, status, message, updateTime); err != nil {
		h.logger.Error("admin", "Failed to add incident update: "+err.Error(), nil)
	} else {
		h.logger.Info("admin", "Updated incident timeline: "+incidentID, map[string]any{"status": status})
		if h.notifier != nil {
			h.notifier.Notify(r.Context(), model.WebhookContext{
				Event:        "incident",
				Status:       status,
				ErrorMessage: message,
			})
		}
	}

	redirectURL := "/admin"
	if inc != nil && inc.StatusPageID != "" {
		redirectURL = "/admin/pages/" + inc.StatusPageID
	} else if ref := r.Header.Get("Referer"); ref != "" {
		redirectURL = ref
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *AdminIncidentsHandler) ServeEdit(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	incidentID := parts[2]

	inc, err := h.incidentRepo.GetByID(incidentID)
	if err != nil || inc == nil {
		http.Error(w, "incident not found", http.StatusNotFound)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title != "" {
		inc.Title = title
	}
	status := strings.TrimSpace(r.FormValue("status"))
	if status != "" {
		inc.Status = status
	}
	severity := strings.TrimSpace(r.FormValue("severity"))
	if severity != "" {
		inc.Severity = severity
		inc.IsMaintenance = severity == "maintenance"
	}

	if customCreated, err := ParseDateTime(r.FormValue("created_at")); err == nil {
		inc.CreatedAt = customCreated
	}
	if start, err := ParseDateTime(r.FormValue("scheduled_start")); err == nil {
		inc.ScheduledStart = &start
	} else if r.FormValue("scheduled_start") == "" {
		inc.ScheduledStart = nil
	}
	if end, err := ParseDateTime(r.FormValue("scheduled_end")); err == nil {
		inc.ScheduledEnd = &end
	} else if r.FormValue("scheduled_end") == "" {
		inc.ScheduledEnd = nil
	}

	if inc.Status == model.IncidentStatusResolved {
		if resolved, err := ParseDateTime(r.FormValue("resolved_at")); err == nil {
			inc.ResolvedAt = &resolved
		} else if inc.ResolvedAt == nil {
			now := time.Now().UTC()
			inc.ResolvedAt = &now
		}
	} else {
		inc.ResolvedAt = nil
	}

	if err := h.incidentRepo.Update(inc); err != nil {
		h.logger.Error("admin", "Failed to update incident: "+err.Error(), nil)
	} else {
		h.logger.Info("admin", "Updated overall incident configuration: "+inc.ID, map[string]any{"title": inc.Title, "status": inc.Status})
	}

	redirectURL := "/admin"
	if inc.StatusPageID != "" {
		redirectURL = "/admin/pages/" + inc.StatusPageID
	} else if ref := r.Header.Get("Referer"); ref != "" {
		redirectURL = ref
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *AdminIncidentsHandler) ServeDelete(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	incidentID := parts[2]

	inc, _ := h.incidentRepo.GetByID(incidentID)
	_ = h.incidentRepo.Delete(incidentID)
	h.logger.Info("admin", "Deleted incident: "+incidentID, nil)

	redirectURL := "/admin"
	if inc != nil && inc.StatusPageID != "" {
		redirectURL = "/admin/pages/" + inc.StatusPageID
	} else if ref := r.Header.Get("Referer"); ref != "" {
		redirectURL = ref
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}
