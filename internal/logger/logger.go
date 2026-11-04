package logger

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/model"
)

type Logger struct {
	repo *db.LogRepo
}

func New(repo *db.LogRepo) *Logger {
	return &Logger{repo: repo}
}

func (l *Logger) log(level, source, message string, monitorID *string, ctxObj any) {
	var ctxJSON string
	if ctxObj != nil {
		if b, err := json.Marshal(ctxObj); err == nil {
			ctxJSON = string(b)
		}
	}

	entry := &model.SystemLog{
		Level:       level,
		Source:      source,
		MonitorID:   monitorID,
		Message:     message,
		ContextJSON: ctxJSON,
		CreatedAt:   time.Now().UTC(),
	}

	// Print to stdout
	monPrefix := ""
	if monitorID != nil {
		monPrefix = fmt.Sprintf("[%s] ", *monitorID)
	}
	log.Printf("[%s] [%s] %s%s %s", level, source, monPrefix, message, ctxJSON)

	// Persist to database
	if l.repo != nil {
		go func() {
			_ = l.repo.Insert(entry)
		}()
	}
}

func (l *Logger) Info(source, message string, ctxObj any) {
	l.log("INFO", source, message, nil, ctxObj)
}

func (l *Logger) Warn(source, message string, ctxObj any) {
	l.log("WARN", source, message, nil, ctxObj)
}

func (l *Logger) Error(source, message string, ctxObj any) {
	l.log("ERROR", source, message, nil, ctxObj)
}

func (l *Logger) MonitorInfo(monitorID, source, message string, ctxObj any) {
	l.log("INFO", source, message, &monitorID, ctxObj)
}

func (l *Logger) MonitorWarn(monitorID, source, message string, ctxObj any) {
	l.log("WARN", source, message, &monitorID, ctxObj)
}

func (l *Logger) MonitorError(monitorID, source, message string, ctxObj any) {
	l.log("ERROR", source, message, &monitorID, ctxObj)
}
