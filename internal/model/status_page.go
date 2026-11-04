package model

import "time"

// Supported built-in preset themes
const (
	ThemeCatppuccinMocha = "catppuccin-mocha"
	ThemeCatppuccinLatte = "catppuccin-latte"
	ThemeTokyoNight      = "tokyo-night"
	ThemeNord            = "nord"
	ThemeDracula         = "dracula"
	ThemeOpenStatusSlate = "openstatus-slate"
)

// StatusPage represents a public or private multi-tenant status page.
type StatusPage struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Domain      string    `json:"domain,omitempty"`      // e.g. "status.git.com", "status.terminal.in"
	Description string    `json:"description,omitempty"`
	LogoURL     string    `json:"logo_url,omitempty"`    // Image URL or base64 data URI
	Theme       string    `json:"theme"`                 // One of the 6 preset themes
	AccentColor string    `json:"accent_color"`          // Hex color e.g. "#10b981"
	Password    string    `json:"-"`                     // Optional bcrypt password hash for private pages
	HasPassword bool      `json:"has_password"`
	IsDefault   bool      `json:"is_default"`
	HistoryDays int       `json:"history_days"`          // Configured number of days to monitor & show (e.g. 7, 14, 30, 60, 90)
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// StatusPageMonitor represents a monitor mapped to a status page with group/ordering.
type StatusPageMonitor struct {
	StatusPageID string `json:"status_page_id"`
	MonitorID    string `json:"monitor_id"`
	DisplayOrder int    `json:"display_order"`
	GroupName    string `json:"group_name"`
}
