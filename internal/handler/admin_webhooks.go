package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
	"uptime-monitor/internal/notifier"
)

type AdminWebhooksHandler struct {
	webhookRepo *db.WebhookRepo
	dispatcher  *notifier.Dispatcher
	renderer    *TemplateRenderer
	logger      *logger.Logger
}

func NewAdminWebhooksHandler(
	wRepo *db.WebhookRepo,
	disp *notifier.Dispatcher,
	tr *TemplateRenderer,
	log *logger.Logger,
) *AdminWebhooksHandler {
	return &AdminWebhooksHandler{
		webhookRepo: wRepo,
		dispatcher:  disp,
		renderer:    tr,
		logger:      log,
	}
}

func (h *AdminWebhooksHandler) ServeList(w http.ResponseWriter, r *http.Request) {
	webhooks, _ := h.webhookRepo.GetAll()

	data := map[string]any{
		"Title":     "Alert Webhooks",
		"ActiveTab": "webhooks",
		"Webhooks":  webhooks,
	}
	_ = h.renderer.Render(w, "admin_webhooks", data)
}

func (h *AdminWebhooksHandler) ServeCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	url := strings.TrimSpace(r.FormValue("url"))
	method := strings.TrimSpace(r.FormValue("method"))
	payloadTmpl := strings.TrimSpace(r.FormValue("payload_template"))
	headersJSON := strings.TrimSpace(r.FormValue("headers_json"))

	down := r.FormValue("trigger_on_down") == "true"
	degraded := r.FormValue("trigger_on_degraded") == "true"
	up := r.FormValue("trigger_on_up") == "true"
	tlsWarn := r.FormValue("trigger_on_tls_warning") == "true"
	inc := r.FormValue("trigger_on_incident") == "true"

	webhook := &model.AlertWebhook{
		Name:                name,
		URL:                 url,
		Method:              method,
		HeadersJSON:         headersJSON,
		PayloadTemplate:     payloadTmpl,
		TriggerOnDown:       down,
		TriggerOnDegraded:   degraded,
		TriggerOnUp:         up,
		TriggerOnTLSWarning: tlsWarn,
		TriggerOnIncident:   inc,
		IsActive:            true,
	}

	if err := h.webhookRepo.Create(webhook); err != nil {
		h.logger.Error("admin", "Failed to create webhook: "+err.Error(), nil)
	} else {
		h.logger.Info("admin", "Created alert webhook: "+webhook.Name, map[string]any{"url": logger.SanitizeURL(webhook.URL)})
	}

	http.Redirect(w, r, "/admin/webhooks", http.StatusFound)
}

func (h *AdminWebhooksHandler) ServeTest(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	webhookID := parts[2]

	webhook, err := h.webhookRepo.GetByID(webhookID)
	if err != nil {
		http.Error(w, "webhook not found", http.StatusNotFound)
		return
	}

	sampleContext := model.WebhookContext{
		Event:          "test_ping",
		MonitorID:      "test-mon-001",
		MonitorName:    "Sample Service",
		Target:         "https://example.com/api",
		Protocol:       "https",
		Status:         "operational",
		PreviousStatus: "degraded",
		LatencyMs:      42,
		ErrorMessage:   "",
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	}

	payload, err := h.dispatcher.DispatchWithPayload(r.Context(), webhook, sampleContext)
	safeURL := logger.SanitizeURL(webhook.URL)
	logCtx := map[string]any{
		"url":     safeURL,
		"payload": payload,
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err != nil {
		h.logger.Warn("notifier", fmt.Sprintf("Test webhook delivery failed for %q (%s): %v", webhook.Name, safeURL, err), logCtx)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, "Test delivery failed: %v\n\nPayload sent:\n%s", err, payload)
		return
	}

	h.logger.Info("notifier", fmt.Sprintf("Successfully delivered test webhook %q to %s", webhook.Name, safeURL), logCtx)
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "Test notification sent successfully!\n\nPayload sent:\n%s", payload)
}

func (h *AdminWebhooksHandler) ServeDelete(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	webhookID := parts[2]

	_ = h.webhookRepo.Delete(webhookID)
	h.logger.Info("admin", "Deleted webhook: "+webhookID, nil)
	http.Redirect(w, r, "/admin/webhooks", http.StatusFound)
}

func (h *AdminWebhooksHandler) ServeGet(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	webhookID := parts[2]

	webhook, err := h.webhookRepo.GetByID(webhookID)
	if err != nil {
		http.Error(w, "webhook not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(webhook)
}

func (h *AdminWebhooksHandler) ServeUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	webhookID := parts[2]

	webhook, err := h.webhookRepo.GetByID(webhookID)
	if err != nil {
		http.Error(w, "webhook not found", http.StatusNotFound)
		return
	}

	webhook.Name = strings.TrimSpace(r.FormValue("name"))
	webhook.URL = strings.TrimSpace(r.FormValue("url"))
	webhook.Method = strings.TrimSpace(r.FormValue("method"))
	webhook.PayloadTemplate = strings.TrimSpace(r.FormValue("payload_template"))
	webhook.HeadersJSON = strings.TrimSpace(r.FormValue("headers_json"))

	webhook.TriggerOnDown = r.FormValue("trigger_on_down") == "true"
	webhook.TriggerOnDegraded = r.FormValue("trigger_on_degraded") == "true"
	webhook.TriggerOnUp = r.FormValue("trigger_on_up") == "true"
	webhook.TriggerOnTLSWarning = r.FormValue("trigger_on_tls_warning") == "true"
	webhook.TriggerOnIncident = r.FormValue("trigger_on_incident") == "true"
	webhook.IsActive = r.FormValue("is_active") == "true" || r.FormValue("is_active") == "on"

	if err := h.webhookRepo.Update(webhook); err != nil {
		h.logger.Error("admin", "Failed to update webhook: "+err.Error(), nil)
	} else {
		h.logger.Info("admin", "Updated alert webhook: "+webhook.Name, map[string]any{"id": webhook.ID, "url": logger.SanitizeURL(webhook.URL)})
	}

	http.Redirect(w, r, "/admin/webhooks", http.StatusFound)
}
