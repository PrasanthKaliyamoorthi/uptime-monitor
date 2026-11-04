package logger

import (
	"testing"

	"uptime-monitor/internal/db"
)

func TestLoggerMethods(t *testing.T) {
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer database.Close()

	logRepo := db.NewLogRepo(database)
	l := New(logRepo)

	l.Info("system", "Test info message", map[string]string{"foo": "bar"})
	l.Warn("system", "Test warn message", nil)
	l.Error("system", "Test error message", map[string]int{"code": 500})

	l.MonitorInfo("mon-123", "checker", "Monitor is up", map[string]int{"latency": 42})
	l.MonitorWarn("mon-123", "checker", "Monitor is degraded", nil)
	l.MonitorError("mon-123", "checker", "Monitor is down", map[string]string{"err": "timeout"})

	// Also test nil repo safe logger
	nilLogger := New(nil)
	nilLogger.Info("test", "Message with nil repo", nil)
}
