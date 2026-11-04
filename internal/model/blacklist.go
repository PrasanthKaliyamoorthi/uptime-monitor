package model

import "time"

// BlacklistEntry represents an IP tracked for failed login attempts or blacklisted from the admin console.
type BlacklistEntry struct {
	IP             string     `json:"ip"`
	Reason         string     `json:"reason"`
	FailedAttempts int        `json:"failed_attempts"`
	Blacklisted    bool       `json:"blacklisted"`
	BlacklistedAt  *time.Time `json:"blacklisted_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
