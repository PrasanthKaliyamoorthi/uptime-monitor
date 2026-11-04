package model

import (
	"testing"
)

func TestWebhookContextToMapAndAliases(t *testing.T) {
	ctx := WebhookContext{
		Event:          "down",
		MonitorID:      "mon-001",
		MonitorName:    "Auth Service",
		Target:         "https://auth.example.com",
		Protocol:       "https",
		Status:         "down",
		PreviousStatus: "up",
		LatencyMs:      120,
		ErrorMessage:   "503 Service Unavailable",
		Timestamp:      "2026-11-04T10:00:00Z",
	}

	if ctx.Name() != "Auth Service" {
		t.Errorf("expected Name() 'Auth Service', got %q", ctx.Name())
	}
	if ctx.URL() != "https://auth.example.com" {
		t.Errorf("expected URL() 'https://auth.example.com', got %q", ctx.URL())
	}
	if ctx.Time() != "2026-11-04T10:00:00Z" {
		t.Errorf("expected Time() '2026-11-04T10:00:00Z', got %q", ctx.Time())
	}
	if ctx.Latency() != 120 {
		t.Errorf("expected Latency() 120, got %d", ctx.Latency())
	}
	if ctx.PrevStatus() != "up" {
		t.Errorf("expected PrevStatus() 'up', got %q", ctx.PrevStatus())
	}

	m := ctx.ToMap()
	if m["Error"] != "503 Service Unavailable" || m["error"] != "503 Service Unavailable" {
		t.Errorf("expected Error alias to match ErrorMessage, got %v", m["Error"])
	}
	if m["Name"] != "Auth Service" || m["name"] != "Auth Service" {
		t.Errorf("expected Name alias, got %v", m["Name"])
	}
	if m["Target"] != "https://auth.example.com" || m["target"] != "https://auth.example.com" {
		t.Errorf("expected Target alias, got %v", m["Target"])
	}
}
