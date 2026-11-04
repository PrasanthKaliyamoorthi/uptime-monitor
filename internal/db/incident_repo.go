package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"uptime-monitor/internal/model"

	"github.com/google/uuid"
)

type IncidentRepo struct {
	db *sql.DB
}

func NewIncidentRepo(db *sql.DB) *IncidentRepo {
	return &IncidentRepo{db: db}
}

func (r *IncidentRepo) scanIncident(row interface{ Scan(...any) error }) (*model.Incident, error) {
	var inc model.Incident
	var spID sql.NullString
	var start, end, resolved sql.NullTime

	err := row.Scan(
		&inc.ID, &spID, &inc.Title, &inc.Status, &inc.Severity,
		&inc.IsMaintenance, &start, &end, &inc.CreatedAt, &resolved,
	)
	if err != nil {
		return nil, err
	}
	if spID.Valid {
		inc.StatusPageID = spID.String
	}
	if start.Valid {
		inc.ScheduledStart = &start.Time
	}
	if end.Valid {
		inc.ScheduledEnd = &end.Time
	}
	if resolved.Valid {
		inc.ResolvedAt = &resolved.Time
	}
	return &inc, nil
}

const incidentColumns = `
	id, status_page_id, title, status, severity,
	is_maintenance, scheduled_start, scheduled_end, created_at, resolved_at
`

func (r *IncidentRepo) Create(inc *model.Incident) error {
	if inc.ID == "" {
		inc.ID = uuid.New().String()
	}
	if inc.CreatedAt.IsZero() {
		inc.CreatedAt = time.Now().UTC()
	}
	if inc.Status == model.IncidentStatusResolved && inc.ResolvedAt == nil {
		inc.ResolvedAt = &inc.CreatedAt
	}

	var spID sql.NullString
	if inc.StatusPageID != "" {
		spID = sql.NullString{String: inc.StatusPageID, Valid: true}
	}

	_, err := r.db.Exec(`
		INSERT INTO incidents (
			id, status_page_id, title, status, severity,
			is_maintenance, scheduled_start, scheduled_end, created_at, resolved_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		inc.ID, spID, inc.Title, inc.Status, inc.Severity,
		inc.IsMaintenance, inc.ScheduledStart, inc.ScheduledEnd, inc.CreatedAt, inc.ResolvedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert incident: %w", err)
	}
	return nil
}

func (r *IncidentRepo) Update(inc *model.Incident) error {
	var spID sql.NullString
	if inc.StatusPageID != "" {
		spID = sql.NullString{String: inc.StatusPageID, Valid: true}
	}
	if inc.CreatedAt.IsZero() {
		inc.CreatedAt = time.Now().UTC()
	}
	if inc.Status == model.IncidentStatusResolved && inc.ResolvedAt == nil {
		now := time.Now().UTC()
		inc.ResolvedAt = &now
	} else if inc.Status != model.IncidentStatusResolved {
		inc.ResolvedAt = nil
	}

	_, err := r.db.Exec(`
		UPDATE incidents SET
			status_page_id = ?,
			title = ?,
			status = ?,
			severity = ?,
			is_maintenance = ?,
			scheduled_start = ?,
			scheduled_end = ?,
			created_at = ?,
			resolved_at = ?
		WHERE id = ?
	`,
		spID, inc.Title, inc.Status, inc.Severity,
		inc.IsMaintenance, inc.ScheduledStart, inc.ScheduledEnd,
		inc.CreatedAt, inc.ResolvedAt, inc.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update incident: %w", err)
	}
	return nil
}

func (r *IncidentRepo) GetByID(id string) (*model.Incident, error) {
	row := r.db.QueryRow("SELECT "+incidentColumns+" FROM incidents WHERE id = ?", id)
	inc, err := r.scanIncident(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("incident not found")
		}
		return nil, err
	}

	updates, err := r.GetUpdates(id)
	if err == nil {
		inc.Updates = updates
	}
	return inc, nil
}

func (r *IncidentRepo) GetUpdates(incidentID string) ([]model.IncidentUpdate, error) {
	rows, err := r.db.Query(`
		SELECT id, incident_id, status, message, created_at
		FROM incident_updates
		WHERE incident_id = ?
		ORDER BY created_at DESC
	`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.IncidentUpdate
	for rows.Next() {
		var u model.IncidentUpdate
		if err := rows.Scan(&u.ID, &u.IncidentID, &u.Status, &u.Message, &u.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, u)
	}
	return list, nil
}

func (r *IncidentRepo) AddUpdate(incidentID string, status string, message string) error {
	return r.AddUpdateWithTime(incidentID, status, message, time.Now().UTC())
}

func (r *IncidentRepo) AddUpdateWithTime(incidentID string, status string, message string, createdAt time.Time) error {
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := r.db.Exec(`
		INSERT INTO incident_updates (incident_id, status, message, created_at)
		VALUES (?, ?, ?, ?)
	`, incidentID, status, message, createdAt)
	if err != nil {
		return err
	}

	// Update main incident status as well
	var resolvedAt *time.Time
	if status == model.IncidentStatusResolved {
		resolvedAt = &createdAt
	}
	_, err = r.db.Exec("UPDATE incidents SET status = ?, resolved_at = ? WHERE id = ?", status, resolvedAt, incidentID)
	return err
}

func (r *IncidentRepo) GetAll() ([]model.Incident, error) {
	rows, err := r.db.Query("SELECT " + incidentColumns + " FROM incidents ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}

	var list []model.Incident
	for rows.Next() {
		inc, err := r.scanIncident(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, *inc)
	}
	rows.Close()

	for i := range list {
		updates, _ := r.GetUpdates(list[i].ID)
		list[i].Updates = updates
	}
	return list, nil
}

func (r *IncidentRepo) GetByPageID(statusPageID string) ([]model.Incident, error) {
	rows, err := r.db.Query("SELECT "+incidentColumns+" FROM incidents WHERE status_page_id = ? ORDER BY created_at DESC", statusPageID)
	if err != nil {
		return nil, err
	}

	var list []model.Incident
	for rows.Next() {
		inc, err := r.scanIncident(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, *inc)
	}
	rows.Close()

	for i := range list {
		updates, _ := r.GetUpdates(list[i].ID)
		list[i].Updates = updates
	}
	return list, nil
}

// GetActiveForStatusPage returns unresolved incidents and ongoing maintenance for a page
func (r *IncidentRepo) GetActiveForStatusPage(statusPageID string) ([]model.Incident, error) {
	rows, err := r.db.Query(`
		SELECT `+incidentColumns+`
		FROM incidents
		WHERE (status_page_id = ? OR status_page_id IS NULL OR status_page_id = '')
		  AND status != ?
		ORDER BY created_at DESC
	`, statusPageID, model.IncidentStatusResolved)
	if err != nil {
		return nil, err
	}

	var list []model.Incident
	for rows.Next() {
		inc, err := r.scanIncident(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, *inc)
	}
	rows.Close()

	for i := range list {
		updates, _ := r.GetUpdates(list[i].ID)
		list[i].Updates = updates
	}
	return list, nil
}

// GetPastForStatusPage returns resolved/past incidents within the given time window
func (r *IncidentRepo) GetPastForStatusPage(statusPageID string, since time.Time) ([]model.Incident, error) {
	rows, err := r.db.Query(`
		SELECT `+incidentColumns+`
		FROM incidents
		WHERE (status_page_id = ? OR status_page_id IS NULL OR status_page_id = '')
		  AND status = ?
		  AND (created_at >= ? OR (resolved_at IS NOT NULL AND resolved_at >= ?))
		ORDER BY created_at DESC
	`, statusPageID, model.IncidentStatusResolved, since, since)
	if err != nil {
		return nil, err
	}

	var list []model.Incident
	for rows.Next() {
		inc, err := r.scanIncident(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, *inc)
	}
	rows.Close()

	for i := range list {
		updates, _ := r.GetUpdates(list[i].ID)
		list[i].Updates = updates
	}
	return list, nil
}

func (r *IncidentRepo) Delete(id string) error {
	_, err := r.db.Exec("DELETE FROM incidents WHERE id = ?", id)
	return err
}
