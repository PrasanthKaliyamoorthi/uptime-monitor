package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"uptime-monitor/internal/model"

	"github.com/google/uuid"
)

type WebhookRepo struct {
	db *sql.DB
}

func NewWebhookRepo(db *sql.DB) *WebhookRepo {
	return &WebhookRepo{db: db}
}

func (r *WebhookRepo) scanWebhook(row interface{ Scan(...any) error }) (*model.AlertWebhook, error) {
	var w model.AlertWebhook
	var headers sql.NullString
	err := row.Scan(
		&w.ID, &w.Name, &w.URL, &w.Method, &headers, &w.PayloadTemplate,
		&w.TriggerOnDown, &w.TriggerOnDegraded, &w.TriggerOnUp,
		&w.TriggerOnTLSWarning, &w.TriggerOnIncident, &w.IsActive,
		&w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if headers.Valid {
		w.HeadersJSON = headers.String
	}
	return &w, nil
}

const webhookColumns = `
	id, name, url, method, headers_json, payload_template,
	trigger_on_down, trigger_on_degraded, trigger_on_up,
	trigger_on_tls_warning, trigger_on_incident, is_active,
	created_at, updated_at
`

func (r *WebhookRepo) Create(w *model.AlertWebhook) error {
	if w.ID == "" {
		w.ID = uuid.New().String()
	}
	if w.Method == "" {
		w.Method = "POST"
	}
	now := time.Now().UTC()
	w.CreatedAt = now
	w.UpdatedAt = now

	_, err := r.db.Exec(`
		INSERT INTO alert_webhooks (
			id, name, url, method, headers_json, payload_template,
			trigger_on_down, trigger_on_degraded, trigger_on_up,
			trigger_on_tls_warning, trigger_on_incident, is_active,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		w.ID, w.Name, w.URL, w.Method, w.HeadersJSON, w.PayloadTemplate,
		w.TriggerOnDown, w.TriggerOnDegraded, w.TriggerOnUp,
		w.TriggerOnTLSWarning, w.TriggerOnIncident, w.IsActive,
		w.CreatedAt, w.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert webhook: %w", err)
	}
	return nil
}

func (r *WebhookRepo) GetByID(id string) (*model.AlertWebhook, error) {
	row := r.db.QueryRow("SELECT "+webhookColumns+" FROM alert_webhooks WHERE id = ?", id)
	w, err := r.scanWebhook(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("webhook not found")
		}
		return nil, err
	}
	return w, nil
}

func (r *WebhookRepo) GetAll() ([]model.AlertWebhook, error) {
	rows, err := r.db.Query("SELECT " + webhookColumns + " FROM alert_webhooks ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.AlertWebhook
	for rows.Next() {
		w, err := r.scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *w)
	}
	return list, nil
}

func (r *WebhookRepo) GetActive() ([]model.AlertWebhook, error) {
	rows, err := r.db.Query("SELECT " + webhookColumns + " FROM alert_webhooks WHERE is_active = 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.AlertWebhook
	for rows.Next() {
		w, err := r.scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *w)
	}
	return list, nil
}

func (r *WebhookRepo) Update(w *model.AlertWebhook) error {
	w.UpdatedAt = time.Now().UTC()
	_, err := r.db.Exec(`
		UPDATE alert_webhooks SET
			name = ?, url = ?, method = ?, headers_json = ?, payload_template = ?,
			trigger_on_down = ?, trigger_on_degraded = ?, trigger_on_up = ?,
			trigger_on_tls_warning = ?, trigger_on_incident = ?, is_active = ?,
			updated_at = ?
		WHERE id = ?
	`,
		w.Name, w.URL, w.Method, w.HeadersJSON, w.PayloadTemplate,
		w.TriggerOnDown, w.TriggerOnDegraded, w.TriggerOnUp,
		w.TriggerOnTLSWarning, w.TriggerOnIncident, w.IsActive,
		w.UpdatedAt, w.ID,
	)
	return err
}

func (r *WebhookRepo) Delete(id string) error {
	_, err := r.db.Exec("DELETE FROM alert_webhooks WHERE id = ?", id)
	return err
}
