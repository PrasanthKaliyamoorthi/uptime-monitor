package model

import "time"

// Monitor represents an endpoint to be periodically probed.
type Monitor struct {
	ID            string    `json:"id"`
	StatusPageID  string    `json:"status_page_id"` // Scoped to status page / tenant
	Name          string    `json:"name"`
	Protocol      string    `json:"protocol"` // "http", "https", "tcp", "smtp"
	Target        string    `json:"target"`   // URL (http/https) or host:port (tcp/smtp)
	Method        string    `json:"method"`   // "GET", "HEAD", "POST", "PUT"

	// Authentication & Custom Request Payload
	AuthType      string    `json:"auth_type"` // "none", "bearer", "basic", "header"
	AuthToken     string    `json:"auth_token,omitempty"`
	AuthUser      string    `json:"auth_user,omitempty"`
	AuthPass      string    `json:"auth_pass,omitempty"`
	HeadersJSON   string    `json:"headers_json,omitempty"`
	Body          string    `json:"body,omitempty"`
	SkipTLSVerify bool      `json:"skip_tls_verify"`

	// Timing, Assertions & Damping
	IntervalSec   int       `json:"interval_sec"`
	TimeoutSec    int       `json:"timeout_sec"`
	ExpectedCode  int       `json:"expected_code"`
	Keyword       string    `json:"keyword,omitempty"`
	RetryCount    int       `json:"retry_count"` // Retries before marking down
	MaxDays       int       `json:"max_days"`    // History display up to 60 days
	IsActive      bool      `json:"is_active"`

	// Live status state
	CurrentStatus       string     `json:"current_status"` // "up", "degraded", "down", "pending"
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastCheckedAt       *time.Time `json:"last_checked_at,omitempty"`
	LastLatencyMs       int64      `json:"last_latency_ms"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CheckLog represents a single heartbeat execution record.
type CheckLog struct {
	ID             int64             `json:"id"`
	MonitorID      string            `json:"monitor_id"`
	StatusCode     int               `json:"status_code"`
	LatencyMs      int64             `json:"latency_ms"`
	IsSuccess      bool              `json:"is_success"`
	ErrorType      string            `json:"error_type,omitempty"`
	ErrorMessage   string            `json:"error_message,omitempty"`
	FailureDetails *DiagnosticDetail `json:"failure_details,omitempty"`
	FailureJSON    string            `json:"-"`
	CheckedAt      time.Time         `json:"checked_at"`
}

// CheckResult is returned by probers after executing a health check.
type CheckResult struct {
	IsSuccess      bool
	StatusCode     int
	LatencyMs      int64
	ErrorType      string
	ErrorMessage   string
	FailureDetails *DiagnosticDetail
}
