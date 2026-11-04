package model

import "time"

// Incident statuses
const (
	IncidentStatusInvestigating = "investigating"
	IncidentStatusIdentified    = "identified"
	IncidentStatusMonitoring    = "monitoring"
	IncidentStatusResolved      = "resolved"
)

// Incident severities
const (
	IncidentSeverityMinor       = "minor"
	IncidentSeverityMajor       = "major"
	IncidentSeverityCritical    = "critical"
	IncidentSeverityMaintenance = "maintenance"
)

// Incident represents an incident report or scheduled maintenance.
type Incident struct {
	ID             string           `json:"id"`
	StatusPageID   string           `json:"status_page_id,omitempty"`
	Title          string           `json:"title"`
	Status         string           `json:"status"`   // "investigating", "identified", "monitoring", "resolved"
	Severity       string           `json:"severity"` // "minor", "major", "critical", "maintenance"
	IsMaintenance  bool             `json:"is_maintenance"`
	ScheduledStart *time.Time       `json:"scheduled_start,omitempty"`
	ScheduledEnd   *time.Time       `json:"scheduled_end,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	ResolvedAt     *time.Time       `json:"resolved_at,omitempty"`
	Updates        []IncidentUpdate `json:"updates,omitempty"`
}

// IncidentUpdate represents a chronological message entry in an incident timeline.
type IncidentUpdate struct {
	ID         int64     `json:"id"`
	IncidentID string    `json:"incident_id"`
	Status     string    `json:"status"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
}
