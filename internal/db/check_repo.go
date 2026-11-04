package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"uptime-monitor/internal/model"
)

type CheckRepo struct {
	db *sql.DB
}

func NewCheckRepo(db *sql.DB) *CheckRepo {
	return &CheckRepo{db: db}
}

func (r *CheckRepo) LogCheck(log *model.CheckLog) error {
	var failureJSON sql.NullString
	if log.FailureDetails != nil {
		bytes, err := json.Marshal(log.FailureDetails)
		if err == nil {
			failureJSON = sql.NullString{String: string(bytes), Valid: true}
		}
	}

	checkedAt := log.CheckedAt
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}

	res, err := r.db.Exec(`
		INSERT INTO check_logs (
			monitor_id, status_code, latency_ms, is_success,
			error_type, error_message, failure_details, checked_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		log.MonitorID, log.StatusCode, log.LatencyMs, log.IsSuccess,
		log.ErrorType, log.ErrorMessage, failureJSON, checkedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert check log: %w", err)
	}

	id, err := res.LastInsertId()
	if err == nil {
		log.ID = id
	}
	return nil
}

func (r *CheckRepo) scanCheckLog(rows interface{ Scan(...any) error }) (*model.CheckLog, error) {
	var l model.CheckLog
	var failureJSON sql.NullString
	var errType, errMsg sql.NullString
	var statusCode sql.NullInt64

	err := rows.Scan(
		&l.ID, &l.MonitorID, &statusCode, &l.LatencyMs, &l.IsSuccess,
		&errType, &errMsg, &failureJSON, &l.CheckedAt,
	)
	if err != nil {
		return nil, err
	}

	if statusCode.Valid {
		l.StatusCode = int(statusCode.Int64)
	}
	if errType.Valid {
		l.ErrorType = errType.String
	}
	if errMsg.Valid {
		l.ErrorMessage = errMsg.String
	}
	if failureJSON.Valid && failureJSON.String != "" {
		l.FailureJSON = failureJSON.String
		var diag model.DiagnosticDetail
		if err := json.Unmarshal([]byte(failureJSON.String), &diag); err == nil {
			l.FailureDetails = &diag
		}
	}

	return &l, nil
}

const checkLogColumns = `
	id, monitor_id, status_code, latency_ms, is_success,
	error_type, error_message, failure_details, checked_at
`

func (r *CheckRepo) GetRecentLogs(monitorID string, limit int) ([]model.CheckLog, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.Query(`
		SELECT `+checkLogColumns+`
		FROM check_logs
		WHERE monitor_id = ?
		ORDER BY checked_at DESC
		LIMIT ?
	`, monitorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.CheckLog
	for rows.Next() {
		l, err := r.scanCheckLog(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *l)
	}
	return list, nil
}

func (r *CheckRepo) GetLogsForWindow(monitorID string, since time.Time) ([]model.CheckLog, error) {
	rows, err := r.db.Query(`
		SELECT `+checkLogColumns+`
		FROM check_logs
		WHERE monitor_id = ? AND checked_at >= ?
		ORDER BY checked_at ASC
	`, monitorID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.CheckLog
	for rows.Next() {
		l, err := r.scanCheckLog(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *l)
	}
	return list, nil
}

func (r *CheckRepo) GetLatestCheck(monitorID string) (*model.CheckLog, error) {
	row := r.db.QueryRow(`
		SELECT `+checkLogColumns+`
		FROM check_logs
		WHERE monitor_id = ?
		ORDER BY checked_at DESC
		LIMIT 1
	`, monitorID)

	l, err := r.scanCheckLog(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return l, nil
}

// PruneOldLogs removes checks older than the retention threshold.
func (r *CheckRepo) PruneOldLogs(olderThan time.Time) (int64, error) {
	res, err := r.db.Exec("DELETE FROM check_logs WHERE checked_at < ?", olderThan)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
