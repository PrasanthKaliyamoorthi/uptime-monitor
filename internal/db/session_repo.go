package db

import (
	"database/sql"
	"errors"
	"time"

	"uptime-monitor/internal/model"
)

type SessionRepo struct {
	db *sql.DB
}

func NewSessionRepo(db *sql.DB) *SessionRepo {
	return &SessionRepo{db: db}
}

func (r *SessionRepo) Create(token, adminID, username string, expiresAt time.Time) error {
	_, err := r.db.Exec(`
		INSERT INTO admin_sessions (token, admin_id, username, expires_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(token) DO UPDATE SET
			admin_id = excluded.admin_id,
			username = excluded.username,
			expires_at = excluded.expires_at
	`, token, adminID, username, expiresAt)
	return err
}

func (r *SessionRepo) Get(token string) (*model.Session, error) {
	var s model.Session
	err := r.db.QueryRow(`
		SELECT token, admin_id, username, expires_at
		FROM admin_sessions
		WHERE token = ?
	`, token).Scan(&s.Token, &s.AdminID, &s.Username, &s.ExpiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *SessionRepo) Delete(token string) error {
	_, err := r.db.Exec("DELETE FROM admin_sessions WHERE token = ?", token)
	return err
}

func (r *SessionRepo) DeleteExpired() error {
	_, err := r.db.Exec("DELETE FROM admin_sessions WHERE expires_at < ?", time.Now().UTC())
	return err
}
