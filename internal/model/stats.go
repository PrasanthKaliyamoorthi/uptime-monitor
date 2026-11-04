package model

import (
	"fmt"
	"math"
	"time"
)

// DayStat represents a single daily tick in the 60-day status bar.
type DayStat struct {
	Date            string  `json:"date"`             // "2026-11-03"
	DisplayDate     string  `json:"display_date"`     // "Nov 03, 2026"
	Status          string  `json:"status"`           // "up", "degraded", "down", "no_data"
	CSSClass        string  `json:"css_class"`        // "tick-up", "tick-degraded-10", "tick-degraded-20", etc.
	UptimeRatio     float64 `json:"uptime_ratio"`     // 0.0 - 100.0%
	DowntimeMinutes int     `json:"downtime_minutes"` // Estimated minutes down
	TotalChecks     int     `json:"total_checks"`
	FailedChecks    int     `json:"failed_checks"`
	AvgLatencyMs    int64   `json:"avg_latency_ms"`
	Tooltip         string  `json:"tooltip"`
}

// MonitorSummary aggregates live and historical metrics for a monitor.
type MonitorSummary struct {
	Monitor       Monitor   `json:"monitor"`
	OverallUptime float64   `json:"overall_uptime"` // Overall percentage over configured window
	AvgLatencyMs  int64     `json:"avg_latency_ms"`
	DayBars       []DayStat `json:"day_bars"` // Up to 60 days
	LatestCheck   *CheckLog `json:"latest_check,omitempty"`
}

// ComputeDayBars calculates daily status buckets for the given monitor and logs.
// Window is up to maxDays (e.g. 60 days).
func ComputeDayBars(logs []CheckLog, days int, intervalSec int) []DayStat {
	if days <= 0 {
		days = 60
	}
	if days > 60 {
		days = 60
	}
	if intervalSec <= 0 {
		intervalSec = 60
	}

	// Group logs by calendar date (YYYY-MM-DD)
	logsByDate := make(map[string][]CheckLog)
	for _, l := range logs {
		dateStr := l.CheckedAt.UTC().Format("2006-01-02")
		logsByDate[dateStr] = append(logsByDate[dateStr], l)
	}

	now := time.Now().UTC()
	result := make([]DayStat, days)

	for i := 0; i < days; i++ {
		// days-1-i so oldest day is at index 0, today is at the end (index days-1)
		dayTime := now.AddDate(0, 0, -(days - 1 - i))
		dateStr := dayTime.Format("2006-01-02")
		displayDate := dayTime.Format("Jan 02, 2006")

		dayLogs, exists := logsByDate[dateStr]
		if !exists || len(dayLogs) == 0 {
			result[i] = DayStat{
				Date:        dateStr,
				DisplayDate: displayDate,
				Status:      "no_data",
				CSSClass:    "tick-nodata",
				Tooltip:     fmt.Sprintf("%s: No data recorded", displayDate),
			}
			continue
		}

		total := len(dayLogs)
		failed := 0
		var totalLatency int64
		for _, l := range dayLogs {
			if !l.IsSuccess {
				failed++
			}
			totalLatency += l.LatencyMs
		}

		avgLatency := totalLatency / int64(total)
		uptimeRatio := float64(total-failed) / float64(total) * 100.0

		// Estimate downtime in minutes: each failed check roughly represents intervalSec duration
		downtimeMinutes := int(math.Round(float64(failed*intervalSec) / 60.0))
		if downtimeMinutes > 1440 {
			downtimeMinutes = 1440
		}

		// Status & Visual Representation classification:
		// - >= 98.0% uptime: full green (tick-up)
		// - 90.0% to 97.9% uptime: partial green 90%, partial orange on top 10% (tick-degraded-10)
		// - 80.0% to 89.9% uptime: partial green 80%, partial orange on top 20% (tick-degraded-20)
		// - 70.0% to 79.9% uptime: partial green 70%, partial orange on top 30% (tick-degraded-30)
		// - < 70.0% uptime: serious outage (tick-down, solid red)
		var status string
		var cssClass string
		if uptimeRatio >= 98.0 {
			status = "up"
			cssClass = "tick-up"
		} else if uptimeRatio >= 70.0 {
			status = "degraded"
			if uptimeRatio >= 90.0 {
				cssClass = "tick-degraded-10"
			} else if uptimeRatio >= 80.0 {
				cssClass = "tick-degraded-20"
			} else {
				cssClass = "tick-degraded-30"
			}
		} else {
			status = "down"
			cssClass = "tick-down"
		}

		tooltip := fmt.Sprintf("%s: %.2f%% uptime", displayDate, uptimeRatio)
		if downtimeMinutes > 0 {
			tooltip += fmt.Sprintf(" (%d mins downtime)", downtimeMinutes)
		}
		if avgLatency > 0 {
			tooltip += fmt.Sprintf(" • avg %dms", avgLatency)
		}

		result[i] = DayStat{
			Date:            dateStr,
			DisplayDate:     displayDate,
			Status:          status,
			CSSClass:        cssClass,
			UptimeRatio:     uptimeRatio,
			DowntimeMinutes: downtimeMinutes,
			TotalChecks:     total,
			FailedChecks:    failed,
			AvgLatencyMs:    avgLatency,
			Tooltip:         tooltip,
		}
	}

	return result
}
