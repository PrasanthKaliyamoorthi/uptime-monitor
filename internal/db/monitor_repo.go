package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"uptime-monitor/internal/model"

	"github.com/google/uuid"
)

type MonitorRepo struct {
	db *sql.DB
}

func NewMonitorRepo(db *sql.DB) *MonitorRepo {
	return &MonitorRepo{db: db}
}

func (r *MonitorRepo) Create(m *model.Monitor) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	m.CreatedAt = now
	m.UpdatedAt = now
	if m.IntervalSec <= 0 {
		m.IntervalSec = 60
	}
	if m.TimeoutSec <= 0 {
		m.TimeoutSec = 10
	}
	if m.ExpectedCode <= 0 {
		m.ExpectedCode = 200
	}
	if m.RetryCount <= 0 {
		m.RetryCount = 2
	}
	if m.MaxDays <= 0 || m.MaxDays > 60 {
		m.MaxDays = 60
	}
	if m.CurrentStatus == "" {
		m.CurrentStatus = "pending"
	}

	var spID sql.NullString
	if m.StatusPageID != "" {
		spID = sql.NullString{String: m.StatusPageID, Valid: true}
	}

	_, err := r.db.Exec(`
		INSERT INTO monitors (
			id, status_page_id, name, protocol, target, method,
			auth_type, auth_token, auth_user, auth_pass,
			headers_json, body, skip_tls_verify,
			interval_sec, timeout_sec, expected_code, keyword,
			retry_count, max_days, is_active,
			current_status, consecutive_failures, last_latency_ms,
			created_at, updated_at
		) VALUES (
			?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?,
			?, ?
		)
	`,
		m.ID, spID, m.Name, m.Protocol, m.Target, m.Method,
		m.AuthType, m.AuthToken, m.AuthUser, m.AuthPass,
		m.HeadersJSON, m.Body, m.SkipTLSVerify,
		m.IntervalSec, m.TimeoutSec, m.ExpectedCode, m.Keyword,
		m.RetryCount, m.MaxDays, m.IsActive,
		m.CurrentStatus, m.ConsecutiveFailures, m.LastLatencyMs,
		m.CreatedAt, m.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert monitor: %w", err)
	}
	return nil
}

func (r *MonitorRepo) scanMonitor(row interface{ Scan(...any) error }) (*model.Monitor, error) {
	var m model.Monitor
	var lastChecked sql.NullTime
	var spID sql.NullString
	err := row.Scan(
		&m.ID, &spID, &m.Name, &m.Protocol, &m.Target, &m.Method,
		&m.AuthType, &m.AuthToken, &m.AuthUser, &m.AuthPass,
		&m.HeadersJSON, &m.Body, &m.SkipTLSVerify,
		&m.IntervalSec, &m.TimeoutSec, &m.ExpectedCode, &m.Keyword,
		&m.RetryCount, &m.MaxDays, &m.IsActive,
		&m.CurrentStatus, &m.ConsecutiveFailures, &lastChecked, &m.LastLatencyMs,
		&m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if spID.Valid {
		m.StatusPageID = spID.String
	}
	if lastChecked.Valid {
		m.LastCheckedAt = &lastChecked.Time
	}
	return &m, nil
}

const monitorColumns = `
	id, status_page_id, name, protocol, target, method,
	auth_type, auth_token, auth_user, auth_pass,
	headers_json, body, skip_tls_verify,
	interval_sec, timeout_sec, expected_code, keyword,
	retry_count, max_days, is_active,
	current_status, consecutive_failures, last_checked_at, last_latency_ms,
	created_at, updated_at
`

func (r *MonitorRepo) GetByID(id string) (*model.Monitor, error) {
	row := r.db.QueryRow("SELECT "+monitorColumns+" FROM monitors WHERE id = ?", id)
	m, err := r.scanMonitor(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("monitor not found")
		}
		return nil, err
	}
	return m, nil
}

func (r *MonitorRepo) GetAll() ([]model.Monitor, error) {
	rows, err := r.db.Query("SELECT " + monitorColumns + " FROM monitors ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.Monitor
	for rows.Next() {
		m, err := r.scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *m)
	}
	return list, nil
}

func (r *MonitorRepo) GetByStatusPage(statusPageID string) ([]model.Monitor, error) {
	rows, err := r.db.Query("SELECT "+monitorColumns+" FROM monitors WHERE status_page_id = ? ORDER BY created_at ASC", statusPageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.Monitor
	for rows.Next() {
		m, err := r.scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *m)
	}
	return list, nil
}

func (r *MonitorRepo) GetActive() ([]model.Monitor, error) {
	rows, err := r.db.Query("SELECT " + monitorColumns + " FROM monitors WHERE is_active = 1 ORDER BY created_at ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.Monitor
	for rows.Next() {
		m, err := r.scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *m)
	}
	return list, nil
}

func (r *MonitorRepo) Update(m *model.Monitor) error {
	m.UpdatedAt = time.Now().UTC()
	_, err := r.db.Exec(`
		UPDATE monitors SET
			name = ?, protocol = ?, target = ?, method = ?,
			auth_type = ?, auth_token = ?, auth_user = ?, auth_pass = ?,
			headers_json = ?, body = ?, skip_tls_verify = ?,
			interval_sec = ?, timeout_sec = ?, expected_code = ?, keyword = ?,
			retry_count = ?, max_days = ?, is_active = ?,
			updated_at = ?
		WHERE id = ?
	`,
		m.Name, m.Protocol, m.Target, m.Method,
		m.AuthType, m.AuthToken, m.AuthUser, m.AuthPass,
		m.HeadersJSON, m.Body, m.SkipTLSVerify,
		m.IntervalSec, m.TimeoutSec, m.ExpectedCode, m.Keyword,
		m.RetryCount, m.MaxDays, m.IsActive,
		m.UpdatedAt, m.ID,
	)
	return err
}

func (r *MonitorRepo) UpdateStatus(id string, status string, failures int, latencyMs int64) error {
	now := time.Now().UTC()
	_, err := r.db.Exec(`
		UPDATE monitors SET
			current_status = ?,
			consecutive_failures = ?,
			last_checked_at = ?,
			last_latency_ms = ?,
			updated_at = ?
		WHERE id = ?
	`, status, failures, now, latencyMs, now, id)
	return err
}

func (r *MonitorRepo) ToggleActive(id string) (bool, error) {
	var current bool
	err := r.db.QueryRow("SELECT is_active FROM monitors WHERE id = ?", id).Scan(&current)
	if err != nil {
		return false, err
	}
	newVal := !current
	_, err = r.db.Exec("UPDATE monitors SET is_active = ?, updated_at = ? WHERE id = ?", newVal, time.Now().UTC(), id)
	return newVal, err
}

func (r *MonitorRepo) Delete(id string) error {
	_, err := r.db.Exec("DELETE FROM monitors WHERE id = ?", id)
	return err
}
