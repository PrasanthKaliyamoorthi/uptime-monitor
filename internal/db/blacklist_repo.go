package db

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"uptime-monitor/internal/model"
)

type BlacklistRepo struct {
	db *sql.DB
}

func NewBlacklistRepo(db *sql.DB) *BlacklistRepo {
	return &BlacklistRepo{db: db}
}

func cleanIP(ip string) string {
	clean := strings.TrimSpace(ip)
	// Normalize IPv6 localhost
	if clean == "::1" || clean == "[::1]" {
		return "127.0.0.1"
	}
	return clean
}

// IsBlacklisted returns true if the IP is currently blacklisted from the admin console
func (r *BlacklistRepo) IsBlacklisted(ip string) (bool, error) {
	ip = cleanIP(ip)
	if ip == "" {
		return false, nil
	}

	var blacklisted bool
	err := r.db.QueryRow("SELECT blacklisted FROM admin_blacklist WHERE ip = ?", ip).Scan(&blacklisted)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return blacklisted, nil
}

// GetEntry returns the blacklist and attempt tracking record for an IP
func (r *BlacklistRepo) GetEntry(ip string) (*model.BlacklistEntry, error) {
	ip = cleanIP(ip)
	var e model.BlacklistEntry
	var blacklistedAt sql.NullTime

	err := r.db.QueryRow(`
		SELECT ip, reason, failed_attempts, blacklisted, blacklisted_at, created_at, updated_at
		FROM admin_blacklist
		WHERE ip = ?
	`, ip).Scan(
		&e.IP, &e.Reason, &e.FailedAttempts, &e.Blacklisted,
		&blacklistedAt, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if blacklistedAt.Valid {
		e.BlacklistedAt = &blacklistedAt.Time
	}
	return &e, nil
}

// RecordFailedAttempt increments the failed attempt count for an IP atomically.
// If the count reaches or exceeds threshold, the IP is blacklisted.
// Returns (isBlacklisted, currentAttempts, error).
func (r *BlacklistRepo) RecordFailedAttempt(ip string, threshold int, reason string) (bool, int, error) {
	ip = cleanIP(ip)
	if ip == "" {
		return false, 0, errors.New("empty IP address")
	}
	if threshold <= 0 {
		threshold = 5
	}
	if reason == "" {
		reason = "Excessive failed login attempts"
	}

	now := time.Now().UTC()
	initiallyBlocked := 1 >= threshold

	var bTime *time.Time
	if initiallyBlocked {
		bTime = &now
	}

	query := `
		INSERT INTO admin_blacklist (
			ip, reason, failed_attempts, blacklisted, blacklisted_at, created_at, updated_at
		) VALUES (?, ?, 1, ?, ?, ?, ?)
		ON CONFLICT(ip) DO UPDATE SET
			failed_attempts = admin_blacklist.failed_attempts + 1,
			blacklisted = CASE 
				WHEN admin_blacklist.blacklisted = 1 THEN 1 
				WHEN (admin_blacklist.failed_attempts + 1) >= ? THEN 1 
				ELSE 0 
			END,
			blacklisted_at = CASE 
				WHEN admin_blacklist.blacklisted = 1 THEN admin_blacklist.blacklisted_at 
				WHEN (admin_blacklist.failed_attempts + 1) >= ? THEN ? 
				ELSE NULL 
			END,
			reason = excluded.reason,
			updated_at = excluded.updated_at
		RETURNING blacklisted, failed_attempts;
	`

	var isBlocked bool
	var newAttempts int
	err := r.db.QueryRow(query,
		ip, reason, initiallyBlocked, bTime, now, now,
		threshold, threshold, now,
	).Scan(&isBlocked, &newAttempts)

	if err != nil {
		return false, 0, err
	}

	return isBlocked, newAttempts, nil
}

// ResetAttempts clears the failed attempt counter for an IP if not blacklisted
func (r *BlacklistRepo) ResetAttempts(ip string) error {
	ip = cleanIP(ip)
	_, err := r.db.Exec("DELETE FROM admin_blacklist WHERE ip = ? AND blacklisted = 0", ip)
	return err
}

// Remove completely removes an IP from the admin blacklist and resets its attempts
func (r *BlacklistRepo) Remove(ip string) error {
	ip = cleanIP(ip)
	_, err := r.db.Exec("DELETE FROM admin_blacklist WHERE ip = ?", ip)
	return err
}

// GetAll returns all currently blacklisted IPs
func (r *BlacklistRepo) GetAll() ([]model.BlacklistEntry, error) {
	rows, err := r.db.Query(`
		SELECT ip, reason, failed_attempts, blacklisted, blacklisted_at, created_at, updated_at
		FROM admin_blacklist
		WHERE blacklisted = 1
		ORDER BY blacklisted_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.BlacklistEntry
	for rows.Next() {
		var e model.BlacklistEntry
		var bTime sql.NullTime
		if err := rows.Scan(
			&e.IP, &e.Reason, &e.FailedAttempts, &e.Blacklisted,
			&bTime, &e.CreatedAt, &e.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if bTime.Valid {
			e.BlacklistedAt = &bTime.Time
		}
		list = append(list, e)
	}
	return list, nil
}
