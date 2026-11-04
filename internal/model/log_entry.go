package model

import "time"

// SystemLog represents a structured system or audit log entry.
type SystemLog struct {
	ID          int64     `json:"id"`
	Level       string    `json:"level"`  // "INFO", "WARN", "ERROR"
	Source      string    `json:"source"` // "checker", "scheduler", "auth", "admin", "notifier"
	MonitorID   *string   `json:"monitor_id,omitempty"`
	Message     string    `json:"message"`
	ContextJSON string    `json:"context_json,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}
