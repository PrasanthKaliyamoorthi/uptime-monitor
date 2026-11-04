package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"uptime-monitor/internal/model"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type AdminRepo struct {
	db *sql.DB
}

func NewAdminRepo(db *sql.DB) *AdminRepo {
	return &AdminRepo{db: db}
}

func (r *AdminRepo) DB() *sql.DB {
	return r.db
}

// HasAdmin checks if at least one admin account exists.
func (r *AdminRepo) HasAdmin() (bool, error) {
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM admins").Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// CreateAdmin creates the initial or new admin user.
func (r *AdminRepo) CreateAdmin(username, password string) (*model.Admin, error) {
	if username == "" || password == "" {
		return nil, errors.New("username and password cannot be empty")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	admin := &model.Admin{
		ID:           uuid.New().String(),
		Username:     username,
		PasswordHash: string(hash),
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}

	_, err = r.db.Exec(`
		INSERT INTO admins (id, username, password_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, admin.ID, admin.Username, admin.PasswordHash, admin.CreatedAt, admin.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to insert admin: %w", err)
	}

	return admin, nil
}

// Authenticate verifies an admin's username and password.
func (r *AdminRepo) Authenticate(username, password string) (*model.Admin, error) {
	var admin model.Admin
	err := r.db.QueryRow(`
		SELECT id, username, password_hash, created_at, updated_at
		FROM admins WHERE username = ?
	`, username).Scan(&admin.ID, &admin.Username, &admin.PasswordHash, &admin.CreatedAt, &admin.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("invalid credentials")
		}
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password)); err != nil {
		return nil, errors.New("invalid credentials")
	}

	return &admin, nil
}

// ResetPassword resets the password for a specific username or the sole admin if username is empty.
func (r *AdminRepo) ResetPassword(username, newPassword string) error {
	if newPassword == "" {
		return errors.New("new password cannot be empty")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	var res sql.Result
	if username != "" {
		res, err = r.db.Exec("UPDATE admins SET password_hash = ?, updated_at = ? WHERE username = ?", string(hash), time.Now().UTC(), username)
	} else {
		// Reset the first found admin if username was not specified
		res, err = r.db.Exec("UPDATE admins SET password_hash = ?, updated_at = ? WHERE id = (SELECT id FROM admins LIMIT 1)", string(hash), time.Now().UTC())
	}

	if err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("no matching admin account found to reset")
	}

	return nil
}
