package notifier

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
)

func TestWebhookDispatch(t *testing.T) {
	var receivedBody string
	var receivedContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		receivedContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dispatcher := NewDispatcher(nil, nil)
	ctx := context.Background()

	webhook := &model.AlertWebhook{
		Name:            "Test Webhook",
		URL:             server.URL,
		Method:          "POST",
		PayloadTemplate: `{"alert": "{{.MonitorName}} is {{.Status}}"}`,
	}

	data := model.WebhookContext{
		MonitorName: "Payment Gateway",
		Status:      "down",
	}

	err := dispatcher.Dispatch(ctx, webhook, data)
	if err != nil {
		t.Fatalf("expected successful dispatch, got %v", err)
	}

	expected := `{"alert": "Payment Gateway is down"}`
	if strings.TrimSpace(receivedBody) != expected {
		t.Errorf("expected payload %q, got %q", expected, receivedBody)
	}
	if receivedContentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", receivedContentType)
	}
}

func TestWebhookDispatchFormURLEncoded(t *testing.T) {
	var receivedBody string
	var receivedContentType string
	var parsedContent string
	var parsedTo string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		receivedContentType = r.Header.Get("Content-Type")

		// Parse the form as the receiving server (e.g. Zulip) would
		vals, err := url.ParseQuery(receivedBody)
		if err == nil {
			parsedContent = vals.Get("content")
			parsedTo = vals.Get("to")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dispatcher := NewDispatcher(nil, nil)
	ctx := context.Background()

	webhook := &model.AlertWebhook{
		Name:            "Zulip Test",
		URL:             server.URL,
		Method:          "POST",
		PayloadTemplate: `type=direct&to=["user@example.com"]&content="🚨 {{.MonitorName}} is now {{.Status}} ({{.LatencyMs}}ms)\nTarget: {{.Target}}\nERROR: {{.Error}}"`,
	}

	data := model.WebhookContext{
		MonitorName: "API Gateway",
		Status:      "operational",
		Target:      "https://example.com/api",
		LatencyMs:   42,
		Error:       "none",
	}

	payload, err := dispatcher.DispatchWithPayload(ctx, webhook, data)
	if err != nil {
		t.Fatalf("expected successful dispatch, got %v", err)
	}

	if receivedContentType != "application/x-www-form-urlencoded" {
		t.Errorf("expected Content-Type application/x-www-form-urlencoded, got %q", receivedContentType)
	}

	// Payload sent over HTTP should be properly URL-encoded
	if !strings.Contains(payload, "%0A") {
		t.Errorf("expected payload to contain URL-encoded newline %%0A, got %q", payload)
	}

	// Receiver should parse actual newlines and not see outer quotes or literal \n
	expectedContent := "🚨 API Gateway is now operational (42ms)\nTarget: https://example.com/api\nERROR: none"
	if parsedContent != expectedContent {
		t.Errorf("expected decoded content %q, got %q", expectedContent, parsedContent)
	}
	if parsedTo != `["user@example.com"]` {
		t.Errorf("expected to `[\"user@example.com\"]`, got %q", parsedTo)
	}
}

func TestWebhookDispatchNon2xxSnippet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"result":"error","msg":"Missing content argument"}`))
	}))
	defer server.Close()

	dispatcher := NewDispatcher(nil, nil)
	ctx := context.Background()

	webhook := &model.AlertWebhook{
		Name:            "Bad Webhook",
		URL:             server.URL,
		Method:          "POST",
		PayloadTemplate: `{"bad": true}`,
	}

	data := model.WebhookContext{}
	err := dispatcher.Dispatch(ctx, webhook, data)
	if err == nil {
		t.Fatalf("expected error on 400 response")
	}

	if !strings.Contains(err.Error(), "Missing content argument") {
		t.Errorf("expected error to contain server response snippet, got: %v", err)
	}
}

func TestWebhookDispatchAliasesAndErrorVariable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dispatcher := NewDispatcher(nil, nil)
	ctx := context.Background()

	wh := &model.AlertWebhook{
		Name: "Zulip with Error alias",
		URL:  server.URL,
		PayloadTemplate: `type=direct&to=["prasanth@sagasoft.io"]&content=🚨 {{.MonitorName}} status is now {{.Status}} ({{.LatencyMs}}ms)\nTarget: {{.Target}} \nERROR: {{.Error}} \nAlias: {{.Name}} {{.URL}} {{.error}}`,
	}

	data := model.WebhookContext{
		MonitorName:  "Mail Service",
		Status:       "down",
		LatencyMs:    45,
		Target:       "mx.sagasoft.xyz:25",
		ErrorMessage: "Connection refused on port 25",
	}

	payload, err := dispatcher.DispatchWithPayload(ctx, wh, data)
	if err != nil {
		t.Fatalf("unexpected error rendering payload: %v", err)
	}

	decodedPayload, err := url.QueryUnescape(payload)
	if err != nil {
		t.Fatalf("failed to unescape payload: %v", err)
	}

	if !strings.Contains(decodedPayload, `ERROR: Connection refused on port 25`) {
		t.Errorf("expected Error alias to resolve properly, got: %s", decodedPayload)
	}
	if !strings.Contains(decodedPayload, `Alias: Mail Service mx.sagasoft.xyz:25 Connection refused on port 25`) {
		t.Errorf("expected aliases to resolve properly, got: %s", decodedPayload)
	}
}

func TestLiveZulipDelivery(t *testing.T) {
	liveURL := strings.TrimSpace(os.Getenv("TEST_LIVE_ZULIP_URL"))
	if liveURL == "" {
		t.Skip("skipping live Zulip test; set TEST_LIVE_ZULIP_URL to run")
	}

	wh := &model.AlertWebhook{
		Name:            "bao",
		URL:             liveURL,
		Method:          "POST",
		PayloadTemplate: `type=direct&to=["prasanth@sagasoft.io"]&content="🚨 {{.MonitorName}} status is now {{.Status}} ({{.LatencyMs}}ms)\nTarget: {{.Target}} \nERROR: {{.Error}}"`,
	}

	data := model.WebhookContext{
		Event:        "up",
		MonitorName:  "Sample Service",
		Target:       "https://example.com/api",
		Status:       "operational",
		LatencyMs:    42,
		ErrorMessage: "",
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
	}

	disp := NewDispatcher(nil, nil)
	payload, err := disp.DispatchWithPayload(context.Background(), wh, data)
	if err != nil {
		t.Fatalf("live Zulip dispatch failed: %v", err)
	}
	t.Logf("Dispatched payload successfully: %s", payload)
}

func TestWebhookRetryClassifications(t *testing.T) {
	retryableErr := &WebhookDeliveryError{StatusCode: 503}
	if !retryableErr.IsRetryable() {
		t.Errorf("expected 503 to be retryable")
	}

	rateLimitErr := &WebhookDeliveryError{StatusCode: 429}
	if !rateLimitErr.IsRetryable() {
		t.Errorf("expected 429 to be retryable")
	}

	clientErr := &WebhookDeliveryError{StatusCode: 400}
	if clientErr.IsRetryable() {
		t.Errorf("expected 400 to NOT be retryable")
	}

	authErr := &WebhookDeliveryError{StatusCode: 401}
	if authErr.IsRetryable() {
		t.Errorf("expected 401 to NOT be retryable")
	}
}

func TestWebhookNotifyRetryLoop(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := attempts.Add(1)
		if att < 2 {
			w.WriteHeader(http.StatusServiceUnavailable) // 503 triggers retry
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dbFile := fmt.Sprintf("./test_wh_retry_%d.db", os.Getpid())
	defer os.Remove(dbFile)

	database, err := db.InitDB(dbFile)
	if err != nil {
		t.Fatalf("failed to init test db: %v", err)
	}
	defer database.Close()

	webhookRepo := db.NewWebhookRepo(database)
	wh := &model.AlertWebhook{
		Name:            "Retry Test",
		URL:             server.URL,
		Method:          "POST",
		TriggerOnDown:   true,
		IsActive:        true,
		PayloadTemplate: `{"alert": "{{.MonitorName}} is {{.Status}}"}`,
	}
	if err := webhookRepo.Create(wh); err != nil {
		t.Fatalf("failed to create webhook: %v", err)
	}

	logRepo := db.NewLogRepo(database)
	log := logger.New(logRepo)
	disp := NewDispatcher(webhookRepo, log)
	// Fast delays for testing
	disp.SetRetryDelays([]time.Duration{0, 10 * time.Millisecond, 20 * time.Millisecond})

	disp.Notify(context.Background(), model.WebhookContext{
		Event:       "down",
		MonitorName: "Flaky Service",
		Status:      "down",
	})

	// Wait briefly for background retry to complete
	time.Sleep(100 * time.Millisecond)

	if got := attempts.Load(); got != 2 {
		t.Errorf("expected exactly 2 attempts before success, got %d", got)
	}
}
