package db

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"uptime-monitor/internal/model"
)

type APITokenRepo struct {
	db *DB
}

func NewAPITokenRepo(database *DB) *APITokenRepo {
	return &APITokenRepo{db: database}
}

// HashToken computes a SHA-256 hex digest for secure storage and comparison.
func HashToken(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

// GenerateRawToken creates a cryptographically secure random token string with "upt_" prefix.
func GenerateRawToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return "upt_" + hex.EncodeToString(b), nil
}

// Create generates a new API token, stores its hash, and returns the full secret token once.
func (r *APITokenRepo) Create(name string) (*model.CreatedAPIToken, error) {
	rawToken, err := GenerateRawToken()
	if err != nil {
		return nil, err
	}

	id := uuid.New().String()
	tokenHash := HashToken(rawToken)
	prefix := rawToken
	if len(prefix) > 12 {
		prefix = prefix[:12] + "..."
	}
	now := time.Now().UTC()

	query := `INSERT INTO api_tokens (id, name, token_prefix, token_hash, created_at) VALUES (?, ?, ?, ?, ?)`
	if _, err := r.db.Exec(query, id, name, prefix, tokenHash, now); err != nil {
		return nil, fmt.Errorf("failed to insert api token: %w", err)
	}

	return &model.CreatedAPIToken{
		APIToken: model.APIToken{
			ID:          id,
			Name:        name,
			TokenPrefix: prefix,
			TokenHash:   tokenHash,
			CreatedAt:   now,
		},
		Token: rawToken,
	}, nil
}

// GetAll returns metadata for all active API tokens (hashes are excluded).
func (r *APITokenRepo) GetAll() ([]model.APIToken, error) {
	query := `SELECT id, name, token_prefix, created_at, last_used_at FROM api_tokens ORDER BY created_at DESC`
	rows, err := r.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to list api tokens: %w", err)
	}
	defer rows.Close()

	var tokens []model.APIToken
	for rows.Next() {
		var t model.APIToken
		var lastUsed sql.NullTime
		if err := rows.Scan(&t.ID, &t.Name, &t.TokenPrefix, &t.CreatedAt, &lastUsed); err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			t.LastUsedAt = &lastUsed.Time
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// GetByHash retrieves a token by its SHA-256 hash.
func (r *APITokenRepo) GetByHash(tokenHash string) (*model.APIToken, error) {
	query := `SELECT id, name, token_prefix, token_hash, created_at, last_used_at FROM api_tokens WHERE token_hash = ?`
	row := r.db.QueryRow(query, tokenHash)

	var t model.APIToken
	var lastUsed sql.NullTime
	if err := row.Scan(&t.ID, &t.Name, &t.TokenPrefix, &t.TokenHash, &t.CreatedAt, &lastUsed); err != nil {
		return nil, err
	}
	if lastUsed.Valid {
		t.LastUsedAt = &lastUsed.Time
	}
	return &t, nil
}

// TouchLastUsed updates the last_used_at timestamp for a token.
func (r *APITokenRepo) TouchLastUsed(id string) error {
	now := time.Now().UTC()
	query := `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`
	_, err := r.db.Exec(query, now, id)
	return err
}

// Delete permanently removes an API token.
func (r *APITokenRepo) Delete(id string) error {
	query := `DELETE FROM api_tokens WHERE id = ?`
	_, err := r.db.Exec(query, id)
	return err
}
