package model

import "time"

// AlertWebhook represents a fully customizable generic webhook.
type AlertWebhook struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	URL             string    `json:"url"`
	Method          string    `json:"method"` // "POST", "PUT", "GET"
	HeadersJSON     string    `json:"headers_json,omitempty"`
	PayloadTemplate string    `json:"payload_template"` // Templated string with {{.MonitorName}} etc.

	// Configurable Trigger Conditions
	TriggerOnDown       bool `json:"trigger_on_down"`
	TriggerOnDegraded   bool `json:"trigger_on_degraded"`
	TriggerOnUp         bool `json:"trigger_on_up"`
	TriggerOnTLSWarning bool `json:"trigger_on_tls_warning"`
	TriggerOnIncident   bool `json:"trigger_on_incident"`

	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WebhookContext provides variables available to payload templates.
type WebhookContext struct {
	Event          string `json:"event"` // "down", "degraded", "up", "tls_warning", "incident"
	MonitorID      string `json:"monitor_id,omitempty"`
	MonitorName    string `json:"monitor_name,omitempty"`
	Target         string `json:"target,omitempty"`
	Protocol       string `json:"protocol,omitempty"`
	Status         string `json:"status,omitempty"`
	PreviousStatus string `json:"previous_status,omitempty"`
	LatencyMs      int64  `json:"latency_ms,omitempty"`
	ErrorMessage   string `json:"error_message,omitempty"`
	Error          string `json:"error,omitempty"` // Shorthand alias for ErrorMessage
	CertExpiryDays int    `json:"cert_expiry_days,omitempty"`
	IncidentTitle  string `json:"incident_title,omitempty"`
	Timestamp      string `json:"timestamp"`
}

func (c WebhookContext) Name() string       { return c.MonitorName }
func (c WebhookContext) URL() string        { return c.Target }
func (c WebhookContext) Time() string       { return c.Timestamp }
func (c WebhookContext) Latency() int64     { return c.LatencyMs }
func (c WebhookContext) PrevStatus() string { return c.PreviousStatus }

func (c WebhookContext) ToMap() map[string]any {
	errMsg := c.ErrorMessage
	if errMsg == "" {
		errMsg = c.Error
	}
	return map[string]any{
		"Event":          c.Event,
		"MonitorID":      c.MonitorID,
		"MonitorName":    c.MonitorName,
		"Target":         c.Target,
		"Protocol":       c.Protocol,
		"Status":         c.Status,
		"PreviousStatus": c.PreviousStatus,
		"LatencyMs":      c.LatencyMs,
		"ErrorMessage":   errMsg,
		"Error":          errMsg,
		"CertExpiryDays": c.CertExpiryDays,
		"IncidentTitle":  c.IncidentTitle,
		"Timestamp":      c.Timestamp,

		// Shorthand aliases
		"Name":       c.MonitorName,
		"URL":        c.Target,
		"Time":       c.Timestamp,
		"Latency":    c.LatencyMs,
		"PrevStatus": c.PreviousStatus,

		// Lowercase / snake_case aliases
		"event":            c.Event,
		"monitor_id":       c.MonitorID,
		"monitor_name":     c.MonitorName,
		"name":             c.MonitorName,
		"target":           c.Target,
		"url":              c.Target,
		"protocol":         c.Protocol,
		"status":           c.Status,
		"previous_status":  c.PreviousStatus,
		"prev_status":      c.PreviousStatus,
		"latency_ms":       c.LatencyMs,
		"latency":          c.LatencyMs,
		"error_message":    errMsg,
		"error":            errMsg,
		"cert_expiry_days": c.CertExpiryDays,
		"incident_title":   c.IncidentTitle,
		"timestamp":        c.Timestamp,
		"time":             c.Timestamp,
	}
}
