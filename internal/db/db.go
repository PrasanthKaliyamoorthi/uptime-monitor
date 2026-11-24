package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

//go:embed schema_postgres.sql
var schemaPostgresSQL string

// DB wraps standard database/sql.DB to transparently handle dialect differences (e.g. parameter rebinding for PostgreSQL).
type DB struct {
	*sql.DB
	Driver string // "sqlite" or "postgres"
}

// Tx wraps standard database/sql.Tx to transparently handle parameter rebinding during transactions.
type Tx struct {
	*sql.Tx
	Driver string
}

// Rebind transforms SQL query placeholders (? to $1, $2, ...) for PostgreSQL while ignoring quotes.
func Rebind(query string, driver string) string {
	if driver != "postgres" {
		return query
	}

	var b strings.Builder
	b.Grow(len(query) + 16)
	paramIdx := 1
	inSingleQuote := false
	inDoubleQuote := false
	n := len(query)

	for i := 0; i < n; i++ {
		c := query[i]

		if c == '\'' && !inDoubleQuote {
			// Handle escaped single quote ''
			if inSingleQuote && i+1 < n && query[i+1] == '\'' {
				b.WriteByte(c)
				b.WriteByte(query[i+1])
				i++
				continue
			}
			inSingleQuote = !inSingleQuote
			b.WriteByte(c)
			continue
		}

		if c == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			b.WriteByte(c)
			continue
		}

		if c == '?' && !inSingleQuote && !inDoubleQuote {
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(paramIdx))
			paramIdx++
			continue
		}

		b.WriteByte(c)
	}

	return b.String()
}

func (db *DB) Rebind(query string) string {
	return Rebind(query, db.Driver)
}

func (db *DB) Query(query string, args ...any) (*sql.Rows, error) {
	return db.DB.Query(db.Rebind(query), args...)
}

func (db *DB) QueryRow(query string, args ...any) *sql.Row {
	return db.DB.QueryRow(db.Rebind(query), args...)
}

func (db *DB) Exec(query string, args ...any) (sql.Result, error) {
	return db.DB.Exec(db.Rebind(query), args...)
}

func (db *DB) Begin() (*Tx, error) {
	tx, err := db.DB.Begin()
	if err != nil {
		return nil, err
	}
	return &Tx{Tx: tx, Driver: db.Driver}, nil
}

func (tx *Tx) Query(query string, args ...any) (*sql.Rows, error) {
	return tx.Tx.Query(Rebind(query, tx.Driver), args...)
}

func (tx *Tx) QueryRow(query string, args ...any) *sql.Row {
	return tx.Tx.QueryRow(Rebind(query, tx.Driver), args...)
}

func (tx *Tx) Exec(query string, args ...any) (sql.Result, error) {
	return tx.Tx.Exec(Rebind(query, tx.Driver), args...)
}

// WrapDB wraps a raw *sql.DB instance into *DB with driver awareness.
func WrapDB(raw *sql.DB, driver ...string) *DB {
	d := "sqlite"
	if len(driver) > 0 && driver[0] != "" {
		d = driver[0]
	}
	return &DB{DB: raw, Driver: d}
}

// DetectDriver inspects connection string and environment variables to choose database driver.
func DetectDriver(dsn string) string {
	lower := strings.ToLower(strings.TrimSpace(dsn))
	if strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://") || strings.Contains(lower, "sslmode=") {
		return "postgres"
	}
	if envType := strings.ToLower(os.Getenv("DB_TYPE")); envType == "postgres" || envType == "postgresql" {
		return "postgres"
	}
	if envURL := strings.ToLower(os.Getenv("DATABASE_URL")); strings.HasPrefix(envURL, "postgres://") || strings.HasPrefix(envURL, "postgresql://") {
		return "postgres"
	}
	return "sqlite"
}

// InitDB initializes SQLite or PostgreSQL, configures connection pooling, and executes migrations.
func InitDB(dbPathOrDSN string, driverOverride ...string) (*DB, error) {
	driver := DetectDriver(dbPathOrDSN)
	if len(driverOverride) > 0 && driverOverride[0] != "" {
		driver = strings.ToLower(driverOverride[0])
	}

	if driver == "postgres" {
		dsn := dbPathOrDSN
		if !strings.HasPrefix(strings.ToLower(dsn), "postgres://") && !strings.HasPrefix(strings.ToLower(dsn), "postgresql://") && !strings.Contains(dsn, " ") {
			if envURL := os.Getenv("DATABASE_URL"); envURL != "" {
				dsn = envURL
			}
		}

		conn, err := sql.Open("postgres", dsn)
		if err != nil {
			return nil, fmt.Errorf("failed to open postgres database: %w", err)
		}

		conn.SetMaxOpenConns(25)
		conn.SetMaxIdleConns(5)
		conn.SetConnMaxLifetime(5 * time.Minute)

		if err := conn.Ping(); err != nil {
			return nil, fmt.Errorf("failed to ping postgres database: %w", err)
		}

		dbWrapped := &DB{DB: conn, Driver: "postgres"}

		// Execute schema migrations
		if _, err := dbWrapped.Exec(schemaPostgresSQL); err != nil {
			return nil, fmt.Errorf("failed to execute postgres schema migration: %w", err)
		}

		// Run incremental table/column migrations for PostgreSQL
		_, _ = dbWrapped.Exec("ALTER TABLE monitors ADD COLUMN IF NOT EXISTS status_page_id TEXT;")
		_, _ = dbWrapped.Exec("ALTER TABLE status_pages ADD COLUMN IF NOT EXISTS history_days INTEGER NOT NULL DEFAULT 60;")
		_, _ = dbWrapped.Exec("ALTER TABLE status_pages ADD COLUMN IF NOT EXISTS logo_url TEXT;")

		_, _ = dbWrapped.Exec(`
			UPDATE monitors 
			SET status_page_id = (SELECT id FROM status_pages ORDER BY is_default DESC, created_at ASC LIMIT 1) 
			WHERE (status_page_id IS NULL OR status_page_id = '') 
			  AND EXISTS (SELECT 1 FROM status_pages);
		`)
		_, _ = dbWrapped.Exec(`
			INSERT INTO status_page_monitors (status_page_id, monitor_id, display_order, group_name)
			SELECT status_page_id, id, 0, 'Services' 
			FROM monitors 
			WHERE status_page_id IS NOT NULL AND status_page_id != ''
			ON CONFLICT (status_page_id, monitor_id) DO NOTHING;
		`)

		return dbWrapped, nil
	}

	// SQLite Mode
	dbPath := dbPathOrDSN
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("failed to create db directory %s: %w", dir, err)
			}
		}
	}

	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", dbPath)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	conn.SetMaxOpenConns(1)

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	dbWrapped := &DB{DB: conn, Driver: "sqlite"}

	if _, err := dbWrapped.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("failed to execute schema migration: %w", err)
	}

	// Incremental migrations for SQLite
	_, _ = dbWrapped.Exec("ALTER TABLE monitors ADD COLUMN status_page_id TEXT;")
	_, _ = dbWrapped.Exec("ALTER TABLE status_pages ADD COLUMN history_days INTEGER NOT NULL DEFAULT 60;")
	_, _ = dbWrapped.Exec("ALTER TABLE status_pages ADD COLUMN logo_url TEXT;")
	_, _ = dbWrapped.Exec(`CREATE TABLE IF NOT EXISTS admin_blacklist (
		ip TEXT PRIMARY KEY,
		reason TEXT NOT NULL,
		failed_attempts INTEGER NOT NULL DEFAULT 1,
		blacklisted BOOLEAN NOT NULL DEFAULT 0,
		blacklisted_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`)
	_, _ = dbWrapped.Exec(`CREATE TABLE IF NOT EXISTS admin_sessions (
		token TEXT PRIMARY KEY,
		admin_id TEXT NOT NULL,
		username TEXT NOT NULL,
		expires_at DATETIME NOT NULL
	);`)
	_, _ = dbWrapped.Exec(`CREATE TABLE IF NOT EXISTS system_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`)
	_, _ = dbWrapped.Exec(`CREATE TABLE IF NOT EXISTS api_tokens (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		token_prefix TEXT NOT NULL,
		token_hash TEXT UNIQUE NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_used_at DATETIME
	);`)
	_, _ = dbWrapped.Exec(`CREATE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash);`)

	// Link legacy unassociated monitors to the default status page
	_, _ = dbWrapped.Exec(`
		UPDATE monitors 
		SET status_page_id = (SELECT id FROM status_pages ORDER BY is_default DESC, created_at ASC LIMIT 1) 
		WHERE (status_page_id IS NULL OR status_page_id = '') 
		  AND EXISTS (SELECT 1 FROM status_pages);
	`)
	_, _ = dbWrapped.Exec(`
		INSERT INTO status_page_monitors (status_page_id, monitor_id, display_order, group_name)
		SELECT status_page_id, id, 0, 'Services' 
		FROM monitors 
		WHERE status_page_id IS NOT NULL AND status_page_id != ''
		ON CONFLICT (status_page_id, monitor_id) DO NOTHING;
	`)

	return dbWrapped, nil
}
