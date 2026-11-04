package model

import "time"

// Admin represents an administrative user with hashed credentials.
type Admin struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Session represents an authenticated admin session stored in memory or cookie.
type Session struct {
	Token     string
	AdminID   string
	Username  string
	ExpiresAt time.Time
}
