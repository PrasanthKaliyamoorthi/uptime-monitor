package db

import (
	"testing"
)

func TestRebind(t *testing.T) {
	tests := []struct {
		name     string
		driver   string
		query    string
		expected string
	}{
		{
			name:     "sqlite unchanged",
			driver:   "sqlite",
			query:    "SELECT * FROM monitors WHERE id = ? AND is_active = ?",
			expected: "SELECT * FROM monitors WHERE id = ? AND is_active = ?",
		},
		{
			name:     "postgres numbered params",
			driver:   "postgres",
			query:    "SELECT * FROM monitors WHERE id = ? AND is_active = ?",
			expected: "SELECT * FROM monitors WHERE id = $1 AND is_active = $2",
		},
		{
			name:     "postgres ignore question marks in single quotes",
			driver:   "postgres",
			query:    "SELECT * FROM monitors WHERE name = 'Is this working?' AND id = ?",
			expected: "SELECT * FROM monitors WHERE name = 'Is this working?' AND id = $1",
		},
		{
			name:     "postgres ignore question marks in double quotes",
			driver:   "postgres",
			query:    `SELECT "col?name" FROM monitors WHERE id = ? AND active = ?`,
			expected: `SELECT "col?name" FROM monitors WHERE id = $1 AND active = $2`,
		},
		{
			name:     "postgres insert with multiple placeholders",
			driver:   "postgres",
			query:    "INSERT INTO system_settings (key, value, updated_at) VALUES (?, ?, ?)",
			expected: "INSERT INTO system_settings (key, value, updated_at) VALUES ($1, $2, $3)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Rebind(tc.query, tc.driver)
			if got != tc.expected {
				t.Errorf("Rebind(%q, %q) = %q; expected %q", tc.query, tc.driver, got, tc.expected)
			}
		})
	}
}

func TestDetectDriver(t *testing.T) {
	if got := DetectDriver("uptime.db"); got != "sqlite" {
		t.Errorf("expected sqlite for uptime.db, got %s", got)
	}
	if got := DetectDriver(":memory:"); got != "sqlite" {
		t.Errorf("expected sqlite for :memory:, got %s", got)
	}
	if got := DetectDriver("postgres://user:pass@localhost:5432/uptime?sslmode=disable"); got != "postgres" {
		t.Errorf("expected postgres for postgres://, got %s", got)
	}
	if got := DetectDriver("postgresql://user:pass@localhost:5432/uptime"); got != "postgres" {
		t.Errorf("expected postgres for postgresql://, got %s", got)
	}
}
