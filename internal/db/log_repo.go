package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"uptime-monitor/internal/model"
)

type LogRepo struct {
	db *sql.DB
}

func NewLogRepo(db *sql.DB) *LogRepo {
	return &LogRepo{db: db}
}

func (r *LogRepo) Insert(log *model.SystemLog) error {
	createdAt := log.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	var monID sql.NullString
	if log.MonitorID != nil && *log.MonitorID != "" {
		monID = sql.NullString{String: *log.MonitorID, Valid: true}
	}

	var ctxJSON sql.NullString
	if log.ContextJSON != "" {
		ctxJSON = sql.NullString{String: log.ContextJSON, Valid: true}
	}

	res, err := r.db.Exec(`
		INSERT INTO system_logs (level, source, monitor_id, message, context_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, log.Level, log.Source, monID, log.Message, ctxJSON, createdAt)
	if err != nil {
		return fmt.Errorf("failed to insert system log: %w", err)
	}

	id, err := res.LastInsertId()
	if err == nil {
		log.ID = id
	}
	return nil
}

func (r *LogRepo) GetLogs(level, source, query string, limit, offset int) ([]model.SystemLog, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var whereClauses []string
	var args []any

	if level != "" && level != "ALL" {
		whereClauses = append(whereClauses, "level = ?")
		args = append(args, strings.ToUpper(level))
	}
	if source != "" && source != "ALL" {
		whereClauses = append(whereClauses, "source = ?")
		args = append(args, strings.ToLower(source))
	}
	if query != "" {
		whereClauses = append(whereClauses, "(message LIKE ? OR context_json LIKE ?)")
		pattern := "%" + query + "%"
		args = append(args, pattern, pattern)
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	args = append(args, limit, offset)
	querySQL := fmt.Sprintf(`
		SELECT id, level, source, monitor_id, message, context_json, created_at
		FROM system_logs
		%s
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, whereSQL)

	rows, err := r.db.Query(querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.SystemLog
	for rows.Next() {
		var l model.SystemLog
		var monID, ctxJSON sql.NullString
		if err := rows.Scan(&l.ID, &l.Level, &l.Source, &monID, &l.Message, &ctxJSON, &l.CreatedAt); err != nil {
			return nil, err
		}
		if monID.Valid {
			l.MonitorID = &monID.String
		}
		if ctxJSON.Valid {
			l.ContextJSON = ctxJSON.String
		}
		list = append(list, l)
	}
	return list, nil
}

func (r *LogRepo) PruneOldLogs(olderThan time.Time) (int64, error) {
	res, err := r.db.Exec("DELETE FROM system_logs WHERE created_at < ?", olderThan)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
