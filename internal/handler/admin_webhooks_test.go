package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
)

func TestAdminWebhookLifecycleAndEdit(t *testing.T) {
	dbFile := fmt.Sprintf("./test_webhooks_edit_%d.db", os.Getpid())
	defer os.Remove(dbFile)

	database, err := db.InitDB(dbFile)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer database.Close()

	webhookRepo := db.NewWebhookRepo(database)
	logRepo := db.NewLogRepo(database)
	sysLogger := logger.New(logRepo)
	tr, err := NewTemplateRenderer()
	if err != nil {
		t.Fatalf("failed to create template renderer: %v", err)
	}

	handler := NewAdminWebhooksHandler(webhookRepo, nil, tr, sysLogger)

	// 1. Create a webhook
	createForm := url.Values{
		"name":                {"Initial Discord Webhook"},
		"url":                 {"https://discord.com/api/webhooks/old"},
		"method":              {"POST"},
		"headers_json":        {`{"X-Old": "true"}`},
		"payload_template":    {`{"text": "old"}`},
		"trigger_on_down":     {"true"},
		"trigger_on_degraded": {"false"},
		"trigger_on_up":       {"false"},
	}

	reqCreate := httptest.NewRequest(http.MethodPost, "/admin/webhooks", strings.NewReader(createForm.Encode()))
	reqCreate.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recCreate := httptest.NewRecorder()
	handler.ServeCreate(recCreate, reqCreate)

	if recCreate.Code != http.StatusFound {
		t.Fatalf("expected 302 on create, got %d", recCreate.Code)
	}

	all, err := webhookRepo.GetAll()
	if err != nil || len(all) != 1 {
		t.Fatalf("expected 1 webhook, got %d (err: %v)", len(all), err)
	}
	whID := all[0].ID

	// 2. Fetch webhook details via ServeGet (GET /admin/webhooks/{id})
	reqGet := httptest.NewRequest(http.MethodGet, "/admin/webhooks/"+whID, nil)
	recGet := httptest.NewRecorder()
	handler.ServeGet(recGet, reqGet)

	if recGet.Code != http.StatusOK {
		t.Fatalf("expected 200 on ServeGet, got %d", recGet.Code)
	}

	var fetched model.AlertWebhook
	if err := json.Unmarshal(recGet.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("failed to decode JSON from ServeGet: %v", err)
	}
	if fetched.Name != "Initial Discord Webhook" || fetched.URL != "https://discord.com/api/webhooks/old" {
		t.Fatalf("unexpected fetched values: %+v", fetched)
	}

	// 3. Edit the webhook via ServeUpdate (POST /admin/webhooks/{id}/edit)
	editForm := url.Values{
		"name":                   {"Updated Slack Notification"},
		"url":                    {"https://hooks.slack.com/services/updated"},
		"method":                 {"PUT"},
		"headers_json":           {`{"Authorization": "Bearer secret123"}`},
		"payload_template":       {`{"content": "new {{.MonitorName}}"}`},
		"trigger_on_down":        {"true"},
		"trigger_on_degraded":    {"true"},
		"trigger_on_up":          {"true"},
		"trigger_on_tls_warning": {"true"},
		"trigger_on_incident":    {"true"},
		"is_active":              {"true"},
	}

	reqEdit := httptest.NewRequest(http.MethodPost, "/admin/webhooks/"+whID+"/edit", strings.NewReader(editForm.Encode()))
	reqEdit.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recEdit := httptest.NewRecorder()
	handler.ServeUpdate(recEdit, reqEdit)

	if recEdit.Code != http.StatusFound {
		t.Fatalf("expected 302 on update, got %d", recEdit.Code)
	}

	// 4. Verify updated values in DB
	updated, err := webhookRepo.GetByID(whID)
	if err != nil {
		t.Fatalf("failed to fetch updated webhook: %v", err)
	}

	if updated.Name != "Updated Slack Notification" {
		t.Errorf("expected name 'Updated Slack Notification', got %q", updated.Name)
	}
	if updated.URL != "https://hooks.slack.com/services/updated" {
		t.Errorf("expected updated URL, got %q", updated.URL)
	}
	if updated.Method != "PUT" {
		t.Errorf("expected method PUT, got %q", updated.Method)
	}
	if updated.HeadersJSON != `{"Authorization": "Bearer secret123"}` {
		t.Errorf("expected updated headers, got %q", updated.HeadersJSON)
	}
	if updated.PayloadTemplate != `{"content": "new {{.MonitorName}}"}` {
		t.Errorf("expected updated payload template, got %q", updated.PayloadTemplate)
	}
	if !updated.TriggerOnDegraded || !updated.TriggerOnUp || !updated.TriggerOnTLSWarning || !updated.TriggerOnIncident {
		t.Errorf("expected all trigger flags true, got: %+v", updated)
	}
	if !updated.IsActive {
		t.Errorf("expected isActive true")
	}

	// 5. Verify template renders without error
	reqList := httptest.NewRequest(http.MethodGet, "/admin/webhooks", nil)
	recList := httptest.NewRecorder()
	handler.ServeList(recList, reqList)

	if recList.Code != http.StatusOK {
		t.Fatalf("expected 200 on ServeList, got %d", recList.Code)
	}
	listHTML := recList.Body.String()
	if !strings.Contains(listHTML, "Edit Alert Webhook") {
		t.Errorf("expected edit modal in rendered HTML")
	}
	if !strings.Contains(listHTML, "wh-data-"+whID) {
		t.Errorf("expected data script island for webhook %s", whID)
	}
}
