package model

import "time"

// APIToken represents an authorized programmatic API access token.
type APIToken struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"` // e.g. "upt_a1b2c3..."
	TokenHash   string     `json:"-"`            // SHA-256 hash of secret token
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
}

// CreatedAPIToken is returned only when generating a new token to expose the plaintext secret once.
type CreatedAPIToken struct {
	APIToken
	Token string `json:"token"`
}
