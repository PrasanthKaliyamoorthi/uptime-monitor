package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"text/template"
	"time"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
)

// Standard Presets
const (
	PresetDiscord = `{"content": "🚨 **{{.MonitorName}}** is now **{{.Status}}** ({{.LatencyMs}}ms)\nTarget: {{.Target}}\nError: {{.ErrorMessage}}"}`
	PresetSlack   = `{"text": "🚨 *{{.MonitorName}}* is *{{.Status}}* | Latency: {{.LatencyMs}}ms\nTarget: {{.Target}}\nDetails: {{.ErrorMessage}}"}`
	PresetGeneric = `{"event": "{{.Event}}", "monitor_id": "{{.MonitorID}}", "name": "{{.MonitorName}}", "target": "{{.Target}}", "status": "{{.Status}}", "previous_status": "{{.PreviousStatus}}", "latency_ms": {{.LatencyMs}}, "error": "{{.ErrorMessage}}", "timestamp": "{{.Timestamp}}"}`
)

// WebhookDeliveryError classifies webhook transport or HTTP status failures
type WebhookDeliveryError struct {
	StatusCode int
	Err        error
}

func (e *WebhookDeliveryError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("webhook responded with status %d", e.StatusCode)
}

func (e *WebhookDeliveryError) Unwrap() error {
	return e.Err
}

func (e *WebhookDeliveryError) IsRetryable() bool {
	// 429 Too Many Requests or 5xx Server Errors are transient & retryable
	if e.StatusCode == http.StatusTooManyRequests || (e.StatusCode >= 500 && e.StatusCode <= 599) {
		return true
	}
	// Other 4xx client errors (400, 401, 403, 404, etc.) are non-retryable
	if e.StatusCode >= 400 && e.StatusCode < 500 {
		return false
	}
	// Transport / network errors (timeout, refused, conn reset, temporary DNS) are retryable
	if e.Err != nil {
		errStr := strings.ToLower(e.Err.Error())
		if strings.Contains(errStr, "invalid payload template") ||
			strings.Contains(errStr, "failed to render payload template") ||
			strings.Contains(errStr, "unsupported protocol scheme") {
			return false
		}
		return true
	}
	return false
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var delivErr *WebhookDeliveryError
	if errors.As(err, &delivErr) {
		return delivErr.IsRetryable()
	}
	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "invalid payload template") ||
		strings.Contains(errStr, "failed to render") ||
		strings.Contains(errStr, "unsupported protocol scheme") {
		return false
	}
	if strings.Contains(errStr, "status 4") && !strings.Contains(errStr, "status 429") {
		return false
	}
	return true
}

type Dispatcher struct {
	repo        *db.WebhookRepo
	logger      *logger.Logger
	client      *http.Client
	retryDelays []time.Duration
}

func NewDispatcher(repo *db.WebhookRepo, log *logger.Logger) *Dispatcher {
	return &Dispatcher{
		repo:   repo,
		logger: log,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		retryDelays: []time.Duration{0, 2 * time.Second, 5 * time.Second},
	}
}

// SetRetryDelays overrides the backoff retry schedule (useful for fast unit testing)
func (d *Dispatcher) SetRetryDelays(delays []time.Duration) {
	d.retryDelays = delays
}

// Notify checks active webhooks and dispatches events matching trigger conditions
func (d *Dispatcher) Notify(ctx context.Context, data model.WebhookContext) {
	if d.repo == nil {
		return
	}
	webhooks, err := d.repo.GetActive()
	if err != nil || len(webhooks) == 0 {
		return
	}

	if data.Timestamp == "" {
		data.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	for _, w := range webhooks {
		webhook := w
		// Evaluate trigger condition
		shouldTrigger := false
		switch data.Event {
		case "down":
			shouldTrigger = webhook.TriggerOnDown
		case "degraded":
			shouldTrigger = webhook.TriggerOnDegraded
		case "up":
			shouldTrigger = webhook.TriggerOnUp
		case "tls_warning":
			shouldTrigger = webhook.TriggerOnTLSWarning
		case "incident":
			shouldTrigger = webhook.TriggerOnIncident
		}

		if !shouldTrigger {
			continue
		}

		go func() {
			safeURL := logger.SanitizeURL(webhook.URL)
			delays := d.retryDelays
			if len(delays) == 0 {
				delays = []time.Duration{0}
			}

			for attempt, delay := range delays {
				if attempt > 0 && delay > 0 {
					time.Sleep(delay)
				}

				dispatchCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				payload, err := d.DispatchWithPayload(dispatchCtx, &webhook, data)
				cancel()

				logCtx := map[string]any{
					"url":     safeURL,
					"payload": payload,
					"attempt": attempt + 1,
					"max":     len(delays),
				}

				if err == nil {
					if d.logger != nil {
						if attempt > 0 {
							d.logger.Info("notifier", fmt.Sprintf("Successfully delivered webhook %q to %s on attempt %d/%d", webhook.Name, safeURL, attempt+1, len(delays)), logCtx)
						} else {
							d.logger.Info("notifier", fmt.Sprintf("Successfully delivered webhook %q to %s", webhook.Name, safeURL), logCtx)
						}
					}
					return
				}

				if !isRetryableError(err) {
					if d.logger != nil {
						d.logger.Warn("notifier", fmt.Sprintf("Non-retryable failure delivering webhook %q to %s: %v", webhook.Name, safeURL, err), logCtx)
					}
					return
				}

				if d.logger != nil {
					d.logger.Warn("notifier", fmt.Sprintf("Failed to deliver webhook %q to %s (attempt %d/%d): %v", webhook.Name, safeURL, attempt+1, len(delays), err), logCtx)
				}
			}
		}()
	}
}

// Dispatch executes an individual webhook with template rendering and custom headers
func (d *Dispatcher) Dispatch(ctx context.Context, w *model.AlertWebhook, data model.WebhookContext) error {
	_, err := d.DispatchWithPayload(ctx, w, data)
	return err
}

// DispatchWithPayload executes an individual webhook and returns the rendered payload that was sent
func (d *Dispatcher) DispatchWithPayload(ctx context.Context, w *model.AlertWebhook, data model.WebhookContext) (string, error) {
	tmplStr := w.PayloadTemplate
	if strings.TrimSpace(tmplStr) == "" {
		tmplStr = PresetGeneric
	}

	if data.Error == "" {
		data.Error = data.ErrorMessage
	}
	if data.ErrorMessage == "" {
		data.ErrorMessage = data.Error
	}

	tmpl, err := template.New("webhook").
		Option("missingkey=zero").
		Funcs(template.FuncMap{
			"json": func(v any) string {
				b, _ := json.Marshal(v)
				return string(b)
			},
			"urlquery": url.QueryEscape,
			"upper":    strings.ToUpper,
			"lower":    strings.ToLower,
			"eq": func(a, b any) bool {
				sa := fmt.Sprintf("%v", a)
				sb := fmt.Sprintf("%v", b)
				return sa == sb || strings.EqualFold(sa, sb)
			},
			"ne": func(a, b any) bool {
				sa := fmt.Sprintf("%v", a)
				sb := fmt.Sprintf("%v", b)
				return sa != sb && !strings.EqualFold(sa, sb)
			},
			"contains":  strings.Contains,
			"hasPrefix": strings.HasPrefix,
			"hasSuffix": strings.HasSuffix,
		}).
		Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("invalid payload template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data.ToMap()); err != nil {
		return "", fmt.Errorf("failed to render payload template: %w", err)
	}

	payloadStr := buf.String()

	method := strings.ToUpper(strings.TrimSpace(w.Method))
	if method == "" {
		method = "POST"
	}

	// Determine Content-Type: default to application/json, or application/x-www-form-urlencoded if form-formatted,
	// or text/plain if raw markdown / plain text
	bodyStr := strings.TrimSpace(payloadStr)
	contentType := "application/json"

	isJSON := strings.HasPrefix(bodyStr, "{") || strings.HasPrefix(bodyStr, "[")
	isForm := false
	if !isJSON {
		// Check if it's form-formatted like key=val or key1=val1&key2=val2
		if idx := strings.Index(bodyStr, "="); idx != -1 {
			firstKey := strings.TrimSpace(bodyStr[:idx])
			if isValidFormKey(firstKey) {
				isForm = true
			}
		}
	}

	if isJSON {
		contentType = "application/json"
	} else if isForm {
		contentType = "application/x-www-form-urlencoded"
	} else {
		// Raw markdown or plain text template (e.g. {{if eq .status ...}})
		// If sending to Discord or Slack, automatically wrap in their native JSON format:
		loweredURL := strings.ToLower(w.URL)
		if strings.Contains(loweredURL, "discord.com") || strings.Contains(loweredURL, "discordapp.com") || strings.Contains(loweredURL, "/api/webhooks/") {
			wrappedJSON, _ := json.Marshal(map[string]string{"content": payloadStr})
			payloadStr = string(wrappedJSON)
			buf.Reset()
			buf.WriteString(payloadStr)
			contentType = "application/json"
		} else if strings.Contains(loweredURL, "slack.com") {
			wrappedJSON, _ := json.Marshal(map[string]string{"text": payloadStr})
			payloadStr = string(wrappedJSON)
			buf.Reset()
			buf.WriteString(payloadStr)
			contentType = "application/json"
		} else {
			contentType = "text/plain; charset=utf-8"
		}
	}

	// Apply custom headers (can override Content-Type or add auth tokens)
	var customHeaders map[string]string
	if w.HeadersJSON != "" {
		_ = json.Unmarshal([]byte(w.HeadersJSON), &customHeaders)
		for k, v := range customHeaders {
			if strings.EqualFold(k, "Content-Type") {
				contentType = v
			}
		}
	}

	bodyBytes := buf.Bytes()
	finalPayloadStr := payloadStr

	// If form-urlencoded, format and URL-encode fields so that newlines (\n),
	// quotes, spaces, and special characters are transmitted safely and decoded as real line breaks.
	if strings.Contains(strings.ToLower(contentType), "application/x-www-form-urlencoded") {
		fields := parseFormPayload(payloadStr)
		encoded := encodeFormFields(fields)
		bodyBytes = []byte(encoded)
		finalPayloadStr = encoded
	}

	req, err := http.NewRequestWithContext(ctx, method, w.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return finalPayloadStr, fmt.Errorf("failed to build webhook request: %w", err)
	}

	req.Header.Set("Content-Type", contentType)
	for k, v := range customHeaders {
		req.Header.Set(k, v)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return finalPayloadStr, &WebhookDeliveryError{Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		snippet := strings.TrimSpace(string(respBody))
		var subErr error
		if snippet != "" {
			subErr = fmt.Errorf("webhook responded with status %d: %s", resp.StatusCode, snippet)
		} else {
			subErr = fmt.Errorf("webhook responded with non-2xx status: %d", resp.StatusCode)
		}
		return finalPayloadStr, &WebhookDeliveryError{
			StatusCode: resp.StatusCode,
			Err:        subErr,
		}
	}

	return finalPayloadStr, nil
}

type formField struct {
	key string
	val string
}

func unescapeFormValue(val string) string {
	val = strings.TrimSpace(val)
	// Strip surrounding double quotes if present
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	// Strip surrounding single quotes if present
	if len(val) >= 2 && val[0] == '\'' && val[len(val)-1] == '\'' {
		val = val[1 : len(val)-1]
	}
	// Convert literal escape sequences into actual characters
	val = strings.ReplaceAll(val, `\r\n`, "\n")
	val = strings.ReplaceAll(val, `\n`, "\n")
	val = strings.ReplaceAll(val, `\r`, "\n")
	val = strings.ReplaceAll(val, `\t`, "\t")
	val = strings.ReplaceAll(val, `\"`, "\"")
	return val
}

func isValidFormKey(k string) bool {
	if len(k) == 0 || len(k) > 64 {
		return false
	}
	if strings.ContainsAny(k, " \t\r\n*:/?#`<>~!") {
		return false
	}
	first := k[0]
	return (first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || first == '_'
}

func parseFormPayload(raw string) []formField {
	var fields []formField
	var current strings.Builder
	inQuotes := false
	quoteChar := byte(0)

	appendContinuation := func(text string, delim string) {
		if len(fields) > 0 {
			fields[len(fields)-1].val += delim + unescapeFormValue(text)
		} else {
			fields = append(fields, formField{key: text, val: ""})
		}
	}

	flushPart := func(part string, delim string) {
		part = strings.TrimSpace(part)
		if part == "" {
			return
		}
		idx := strings.Index(part, "=")
		if idx != -1 {
			k := strings.TrimSpace(part[:idx])
			if isValidFormKey(k) {
				v := strings.TrimSpace(part[idx+1:])
				fields = append(fields, formField{key: k, val: unescapeFormValue(v)})
				return
			}
		}
		appendContinuation(part, delim)
	}

	lastDelim := "\n"
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if (c == '"' || c == '\'') && (i == 0 || raw[i-1] != '\\') {
			if !inQuotes {
				inQuotes = true
				quoteChar = c
			} else if c == quoteChar {
				inQuotes = false
				quoteChar = 0
			}
			current.WriteByte(c)
		} else if (c == '&' || c == '\n') && !inQuotes {
			flushPart(current.String(), lastDelim)
			current.Reset()
			if c == '&' {
				lastDelim = "&"
			} else {
				lastDelim = "\n"
			}
		} else {
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		flushPart(current.String(), lastDelim)
	}
	return fields
}

func encodeFormFields(fields []formField) string {
	var parts []string
	for _, f := range fields {
		if f.val == "" {
			parts = append(parts, url.QueryEscape(f.key))
		} else {
			parts = append(parts, url.QueryEscape(f.key)+"="+url.QueryEscape(f.val))
		}
	}
	return strings.Join(parts, "&")
}
