package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"uptime-monitor/internal/model"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type StatusPageRepo struct {
	db *sql.DB
}

func NewStatusPageRepo(db *sql.DB) *StatusPageRepo {
	return &StatusPageRepo{db: db}
}

func (r *StatusPageRepo) scanPage(row interface{ Scan(...any) error }) (*model.StatusPage, error) {
	var p model.StatusPage
	var domain, desc, logo, pass sql.NullString
	err := row.Scan(
		&p.ID, &p.Slug, &p.Name, &domain, &desc, &logo,
		&p.Theme, &p.AccentColor, &pass, &p.IsDefault, &p.HistoryDays,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if p.HistoryDays <= 0 {
		p.HistoryDays = 60
	}
	if domain.Valid {
		p.Domain = domain.String
	}
	if desc.Valid {
		p.Description = desc.String
	}
	if logo.Valid {
		p.LogoURL = logo.String
	}
	if pass.Valid && pass.String != "" {
		p.Password = pass.String
		p.HasPassword = true
	}
	return &p, nil
}

const pageColumns = `
	id, slug, name, domain, description, logo_url,
	theme, accent_color, password, is_default, history_days,
	created_at, updated_at
`

func (r *StatusPageRepo) Create(p *model.StatusPage) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	if p.Theme == "" {
		p.Theme = model.ThemeOpenStatusSlate
	}
	if p.AccentColor == "" {
		p.AccentColor = "#10b981"
	}
	if p.HistoryDays <= 0 {
		p.HistoryDays = 60
	}
	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now

	var domainVal, logoVal sql.NullString
	cleanDomain := strings.ToLower(strings.TrimSpace(p.Domain))
	if cleanDomain != "" {
		domainVal = sql.NullString{String: cleanDomain, Valid: true}
	}
	if cleanLogo := strings.TrimSpace(p.LogoURL); cleanLogo != "" {
		logoVal = sql.NullString{String: cleanLogo, Valid: true}
	}

	// If this is set as default, clear other defaults
	if p.IsDefault {
		r.db.Exec("UPDATE status_pages SET is_default = 0")
	}

	_, err := r.db.Exec(`
		INSERT INTO status_pages (
			id, slug, name, domain, description, logo_url,
			theme, accent_color, is_default, history_days,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		p.ID, p.Slug, p.Name, domainVal, p.Description, logoVal,
		p.Theme, p.AccentColor, p.IsDefault, p.HistoryDays,
		p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert status page: %w", err)
	}
	return nil
}

func (r *StatusPageRepo) GetByID(id string) (*model.StatusPage, error) {
	row := r.db.QueryRow("SELECT "+pageColumns+" FROM status_pages WHERE id = ?", id)
	p, err := r.scanPage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("status page not found")
		}
		return nil, err
	}
	return p, nil
}

func (r *StatusPageRepo) GetBySlug(slug string) (*model.StatusPage, error) {
	row := r.db.QueryRow("SELECT "+pageColumns+" FROM status_pages WHERE slug = ?", slug)
	p, err := r.scanPage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("status page not found")
		}
		return nil, err
	}
	return p, nil
}

func (r *StatusPageRepo) FindByDomain(domain string) (*model.StatusPage, error) {
	if domain == "" {
		return nil, nil
	}
	cleanDomain := strings.ToLower(strings.TrimSpace(domain))
	// strip port if present
	if idx := strings.Index(cleanDomain, ":"); idx != -1 {
		cleanDomain = cleanDomain[:idx]
	}

	row := r.db.QueryRow("SELECT "+pageColumns+" FROM status_pages WHERE LOWER(domain) = ?", cleanDomain)
	p, err := r.scanPage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return p, nil
}

func (r *StatusPageRepo) FindByIDOrSlug(val string) (*model.StatusPage, error) {
	row := r.db.QueryRow("SELECT "+pageColumns+" FROM status_pages WHERE id = ? OR slug = ?", val, val)
	p, err := r.scanPage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return p, nil
}

func (r *StatusPageRepo) GetDefault() (*model.StatusPage, error) {
	row := r.db.QueryRow("SELECT " + pageColumns + " FROM status_pages WHERE is_default = 1 LIMIT 1")
	p, err := r.scanPage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Fallback: return the first status page created if no default explicitly designated
			rowFirst := r.db.QueryRow("SELECT " + pageColumns + " FROM status_pages ORDER BY created_at ASC LIMIT 1")
			pFirst, errFirst := r.scanPage(rowFirst)
			if errFirst != nil {
				return nil, nil
			}
			return pFirst, nil
		}
		return nil, err
	}
	return p, nil
}

func (r *StatusPageRepo) GetAll() ([]model.StatusPage, error) {
	rows, err := r.db.Query("SELECT " + pageColumns + " FROM status_pages ORDER BY is_default DESC, created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.StatusPage
	for rows.Next() {
		p, err := r.scanPage(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *p)
	}
	return list, nil
}

func (r *StatusPageRepo) Update(p *model.StatusPage) error {
	p.UpdatedAt = time.Now().UTC()
	if p.HistoryDays <= 0 {
		p.HistoryDays = 60
	}

	var domainVal, logoVal sql.NullString
	cleanDomain := strings.ToLower(strings.TrimSpace(p.Domain))
	if cleanDomain != "" {
		domainVal = sql.NullString{String: cleanDomain, Valid: true}
	}
	if cleanLogo := strings.TrimSpace(p.LogoURL); cleanLogo != "" {
		logoVal = sql.NullString{String: cleanLogo, Valid: true}
	}

	if p.IsDefault {
		r.db.Exec("UPDATE status_pages SET is_default = 0 WHERE id != ?", p.ID)
	}

	_, err := r.db.Exec(`
		UPDATE status_pages SET
			slug = ?, name = ?, domain = ?, description = ?, logo_url = ?,
			theme = ?, accent_color = ?, is_default = ?, history_days = ?,
			updated_at = ?
		WHERE id = ?
	`,
		p.Slug, p.Name, domainVal, p.Description, logoVal,
		p.Theme, p.AccentColor, p.IsDefault, p.HistoryDays,
		p.UpdatedAt, p.ID,
	)
	return err
}

func (r *StatusPageRepo) SetPassword(id string, rawPassword string) error {
	var hashStr string
	if rawPassword != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(rawPassword), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		hashStr = string(hash)
	}
	_, err := r.db.Exec("UPDATE status_pages SET password = ?, updated_at = ? WHERE id = ?", hashStr, time.Now().UTC(), id)
	return err
}

func (r *StatusPageRepo) VerifyPassword(id string, password string) (bool, error) {
	var pass sql.NullString
	err := r.db.QueryRow("SELECT password FROM status_pages WHERE id = ?", id).Scan(&pass)
	if err != nil {
		return false, err
	}
	if !pass.Valid || pass.String == "" {
		return true, nil // No password required
	}
	err = bcrypt.CompareHashAndPassword([]byte(pass.String), []byte(password))
	return err == nil, nil
}

func (r *StatusPageRepo) Delete(id string) error {
	_, err := r.db.Exec("DELETE FROM status_pages WHERE id = ?", id)
	if err != nil {
		return err
	}
	// If no default exists, promote the first remaining page to default
	var count int
	_ = r.db.QueryRow("SELECT COUNT(*) FROM status_pages WHERE is_default = 1").Scan(&count)
	if count == 0 {
		_, _ = r.db.Exec("UPDATE status_pages SET is_default = 1 WHERE id = (SELECT id FROM status_pages ORDER BY created_at ASC LIMIT 1)")
	}
	return nil
}

// SetMonitors replaces the monitor list for a status page
func (r *StatusPageRepo) SetMonitors(statusPageID string, mappings []model.StatusPageMonitor) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM status_page_monitors WHERE status_page_id = ?", statusPageID); err != nil {
		return err
	}

	for _, m := range mappings {
		group := m.GroupName
		if group == "" {
			group = "Services"
		}
		_, err := tx.Exec(`
			INSERT INTO status_page_monitors (status_page_id, monitor_id, display_order, group_name)
			VALUES (?, ?, ?, ?)
		`, statusPageID, m.MonitorID, m.DisplayOrder, group)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *StatusPageRepo) GetMonitorsForPage(statusPageID string) ([]model.StatusPageMonitor, error) {
	rows, err := r.db.Query(`
		SELECT status_page_id, monitor_id, display_order, group_name
		FROM status_page_monitors
		WHERE status_page_id = ?
		ORDER BY display_order ASC
	`, statusPageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.StatusPageMonitor
	for rows.Next() {
		var m model.StatusPageMonitor
		if err := rows.Scan(&m.StatusPageID, &m.MonitorID, &m.DisplayOrder, &m.GroupName); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, nil
}
