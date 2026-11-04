package db

import (
	"database/sql"
	"errors"
	"time"
)

const (
	SettingAdminAllowedDomain = "admin_allowed_domain"
)

type SettingsRepo struct {
	db *sql.DB
}

func NewSettingsRepo(db *sql.DB) *SettingsRepo {
	return &SettingsRepo{db: db}
}

func (r *SettingsRepo) Get(key string) (string, error) {
	var val string
	err := r.db.QueryRow("SELECT value FROM system_settings WHERE key = ?", key).Scan(&val)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return val, nil
}

func (r *SettingsRepo) Set(key, val string) error {
	now := time.Now().UTC()
	_, err := r.db.Exec(`
		INSERT INTO system_settings (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, key, val, now)
	return err
}

func (r *SettingsRepo) GetAll() (map[string]string, error) {
	rows, err := r.db.Query("SELECT key, value FROM system_settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err == nil {
			res[k] = v
		}
	}
	return res, nil
}

// GetAdminAllowedDomain returns the configured domain allowed to access the admin console
func (r *SettingsRepo) GetAdminAllowedDomain() (string, error) {
	return r.Get(SettingAdminAllowedDomain)
}

// SetAdminAllowedDomain updates the configured domain allowed to access the admin console
func (r *SettingsRepo) SetAdminAllowedDomain(domain string) error {
	return r.Set(SettingAdminAllowedDomain, domain)
}
