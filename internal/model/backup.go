package model

import "time"

// PageBackupPayload represents a per-status-page backup bundle (v1).
type PageBackupPayload struct {
	Version    int          `json:"version"` // Schema version: 1
	ExportedAt time.Time    `json:"exported_at"`
	StatusPage StatusPage   `json:"status_page"`
	Monitors   []Monitor    `json:"monitors"`
	Incidents  []Incident   `json:"incidents"`
}

// BackupPayload represents a full backup bundle (v1).
type BackupPayload struct {
	Version            int                 `json:"version"` // Schema version, e.g. 1
	ExportedAt         time.Time           `json:"exported_at"`
	Monitors           []Monitor           `json:"monitors"`
	StatusPages        []StatusPage        `json:"status_pages"`
	StatusPageMonitors []StatusPageMonitor `json:"status_page_monitors"`
	AlertWebhooks      []AlertWebhook      `json:"alert_webhooks"`
}
