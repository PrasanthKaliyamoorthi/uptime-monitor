package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
)

func TestAdminLogsDateAndTimeAndLocalTimestamp(t *testing.T) {
	dbFile := fmt.Sprintf("./test_admin_logs_%d.db", os.Getpid())
	defer os.Remove(dbFile)

	database, err := db.InitDB(dbFile)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer database.Close()

	logRepo := db.NewLogRepo(database)
	blacklistRepo := db.NewBlacklistRepo(database)
	sysLogger := logger.New(logRepo)
	tr, err := NewTemplateRenderer()
	if err != nil {
		t.Fatalf("failed to create template renderer: %v", err)
	}

	handler := NewAdminLogsHandler(logRepo, blacklistRepo, tr, sysLogger)

	// Create test log entry (nil MonitorID to avoid requiring a foreign key monitor record)
	testTime := time.Date(2026, 11, 5, 14, 30, 45, 123000000, time.UTC)
	if err := logRepo.Insert(&model.SystemLog{
		Level:     "ERROR",
		Source:    "checker",
		MonitorID: nil,
		Message:   "Gateway connection timeout",
		CreatedAt: testTime,
	}); err != nil {
		t.Fatalf("failed to insert test log: %v", err)
	}

	logs, err := logRepo.GetLogs("ALL", "ALL", "", 10, 0)
	if err != nil {
		t.Fatalf("failed to query logs: %v", err)
	}
	if len(logs) == 0 {
		t.Fatalf("expected logs in repo, got 0")
	}

	req := httptest.NewRequest("GET", "/admin/logs", nil)
	rec := httptest.NewRecorder()

	handler.ServeLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	body := rec.Body.String()

	// 1. Check Date & Time column header
	if !strings.Contains(body, "Date &amp; Time") && !strings.Contains(body, "Date & Time") {
		t.Errorf("expected 'Date & Time' column header in logs table")
	}

	// 2. Check that date and time (not just time) is present in fallback content
	if !strings.Contains(body, "2026-11-05 14:30:45.123") {
		t.Errorf("expected full date and time '2026-11-05 14:30:45.123' in body, got: %s", body)
	}

	// 3. Check that data-utc attribute exists for browser local timezone conversion
	if !strings.Contains(body, `class="local-timestamp" data-utc="2026-11-05T14:30:45.123Z"`) {
		t.Errorf("expected local-timestamp with data-utc ISO attribute, got: %s", body)
	}

	// 4. Check client-side formatting script is present
	if !strings.Contains(body, "formatLocalTimestamps") {
		t.Errorf("expected formatLocalTimestamps script in HTML")
	}
}
