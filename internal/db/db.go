package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// InitDB initializes SQLite, configures WAL mode, and runs schema migrations.
func InitDB(dbPath string) (*sql.DB, error) {
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("failed to create db directory %s: %w", dir, err)
			}
		}
	}

	// modernc.org/sqlite connection string
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", dbPath)
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Limit connection pool to avoid concurrency locks on SQLite
	database.SetMaxOpenConns(1)

	if err := database.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	if _, err := database.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("failed to execute schema migration: %w", err)
	}

	// Auto-migrate column if table existed previously without status_page_id
	_, _ = database.Exec("ALTER TABLE monitors ADD COLUMN status_page_id TEXT;")
	_, _ = database.Exec("ALTER TABLE status_pages ADD COLUMN history_days INTEGER NOT NULL DEFAULT 60;")
	_, _ = database.Exec("ALTER TABLE status_pages ADD COLUMN logo_url TEXT;")
	_, _ = database.Exec(`CREATE TABLE IF NOT EXISTS admin_blacklist (
		ip TEXT PRIMARY KEY,
		reason TEXT NOT NULL,
		failed_attempts INTEGER NOT NULL DEFAULT 1,
		blacklisted BOOLEAN NOT NULL DEFAULT 0,
		blacklisted_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`)
	_, _ = database.Exec(`CREATE TABLE IF NOT EXISTS admin_sessions (
		token TEXT PRIMARY KEY,
		admin_id TEXT NOT NULL,
		username TEXT NOT NULL,
		expires_at DATETIME NOT NULL
	);`)
	_, _ = database.Exec(`CREATE TABLE IF NOT EXISTS system_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`)

	// Link legacy unassociated monitors to the default status page
	_, _ = database.Exec(`
		UPDATE monitors 
		SET status_page_id = (SELECT id FROM status_pages ORDER BY is_default DESC, created_at ASC LIMIT 1) 
		WHERE (status_page_id IS NULL OR status_page_id = '') 
		  AND EXISTS (SELECT 1 FROM status_pages);
	`)
	_, _ = database.Exec(`
		INSERT OR IGNORE INTO status_page_monitors (status_page_id, monitor_id, display_order, group_name)
		SELECT status_page_id, id, 0, 'Services' 
		FROM monitors 
		WHERE status_page_id IS NOT NULL AND status_page_id != '';
	`)

	return database, nil
}
