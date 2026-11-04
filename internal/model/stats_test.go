package model

import (
	"testing"
	"time"
)

func TestComputeDayBars(t *testing.T) {
	now := time.Now().UTC()
	todayStr := now.Format("2006-01-02")
	yesterdayStr := now.AddDate(0, 0, -1).Format("2006-01-02")
	twoDaysAgoStr := now.AddDate(0, 0, -2).Format("2006-01-02")

	logs := []CheckLog{
		// Today: 10 successful checks (100% up)
		{MonitorID: "m1", IsSuccess: true, LatencyMs: 45, CheckedAt: now},
		{MonitorID: "m1", IsSuccess: true, LatencyMs: 50, CheckedAt: now.Add(-5 * time.Minute)},
		// Yesterday: 9 success, 1 failure (90% up, degraded)
		{MonitorID: "m1", IsSuccess: false, LatencyMs: 120, CheckedAt: now.AddDate(0, 0, -1)},
		{MonitorID: "m1", IsSuccess: true, LatencyMs: 50, CheckedAt: now.AddDate(0, 0, -1).Add(-1 * time.Minute)},
		{MonitorID: "m1", IsSuccess: true, LatencyMs: 50, CheckedAt: now.AddDate(0, 0, -1).Add(-2 * time.Minute)},
		{MonitorID: "m1", IsSuccess: true, LatencyMs: 50, CheckedAt: now.AddDate(0, 0, -1).Add(-3 * time.Minute)},
		{MonitorID: "m1", IsSuccess: true, LatencyMs: 50, CheckedAt: now.AddDate(0, 0, -1).Add(-4 * time.Minute)},
		{MonitorID: "m1", IsSuccess: true, LatencyMs: 50, CheckedAt: now.AddDate(0, 0, -1).Add(-5 * time.Minute)},
		{MonitorID: "m1", IsSuccess: true, LatencyMs: 50, CheckedAt: now.AddDate(0, 0, -1).Add(-6 * time.Minute)},
		{MonitorID: "m1", IsSuccess: true, LatencyMs: 50, CheckedAt: now.AddDate(0, 0, -1).Add(-7 * time.Minute)},
		{MonitorID: "m1", IsSuccess: true, LatencyMs: 50, CheckedAt: now.AddDate(0, 0, -1).Add(-8 * time.Minute)},
		{MonitorID: "m1", IsSuccess: true, LatencyMs: 50, CheckedAt: now.AddDate(0, 0, -1).Add(-9 * time.Minute)},
		// Two days ago: 5 failures, 0 success (0% up, down)
		{MonitorID: "m1", IsSuccess: false, LatencyMs: 500, CheckedAt: now.AddDate(0, 0, -2)},
	}

	days := 3
	intervalSec := 60
	bars := ComputeDayBars(logs, days, intervalSec)

	if len(bars) != 3 {
		t.Fatalf("expected 3 bars, got %d", len(bars))
	}

	// Two days ago (index 0)
	if bars[0].Date != twoDaysAgoStr {
		t.Errorf("expected date %s, got %s", twoDaysAgoStr, bars[0].Date)
	}
	if bars[0].Status != "down" || bars[0].CSSClass != "tick-down" {
		t.Errorf("expected status 'down' & 'tick-down' for two days ago, got %s / %s", bars[0].Status, bars[0].CSSClass)
	}

	// Yesterday (index 1) - 90% uptime -> degraded with 10% orange cap
	if bars[1].Date != yesterdayStr {
		t.Errorf("expected date %s, got %s", yesterdayStr, bars[1].Date)
	}
	if bars[1].Status != "degraded" || bars[1].CSSClass != "tick-degraded-10" {
		t.Errorf("expected status 'degraded' & 'tick-degraded-10' for yesterday, got %s / %s", bars[1].Status, bars[1].CSSClass)
	}

	// Today (index 2) - 100% uptime -> up & tick-up
	if bars[2].Date != todayStr {
		t.Errorf("expected date %s, got %s", todayStr, bars[2].Date)
	}
	if bars[2].Status != "up" || bars[2].CSSClass != "tick-up" {
		t.Errorf("expected status 'up' & 'tick-up' for today, got %s / %s", bars[2].Status, bars[2].CSSClass)
	}
	if bars[2].UptimeRatio != 100.0 {
		t.Errorf("expected 100.0 uptime ratio today, got %f", bars[2].UptimeRatio)
	}
}

func TestComputeDayBarsDegradedThresholds(t *testing.T) {
	now := time.Now().UTC()

	// 1. Test 98.78% uptime (e.g. 81 successful checks, 1 failure -> 81/82 = 98.78%) -> must be full green (tick-up)
	logsHigh := []CheckLog{
		{MonitorID: "m1", IsSuccess: false, CheckedAt: now},
	}
	for i := 0; i < 81; i++ {
		logsHigh = append(logsHigh, CheckLog{
			MonitorID: "m1",
			IsSuccess: true,
			CheckedAt: now.Add(-time.Duration(i+1) * time.Minute),
		})
	}
	barsHigh := ComputeDayBars(logsHigh, 1, 60)
	if barsHigh[0].Status != "up" || barsHigh[0].CSSClass != "tick-up" {
		t.Errorf("expected >=98.0%% uptime to be 'up' & 'tick-up', got %s / %s (ratio: %.2f)",
			barsHigh[0].Status, barsHigh[0].CSSClass, barsHigh[0].UptimeRatio)
	}

	// 2. Test 85% uptime (17 success, 3 failures -> 17/20 = 85.0%) -> tick-degraded-20 (80% green, 20% orange)
	logs85 := []CheckLog{
		{MonitorID: "m1", IsSuccess: false, CheckedAt: now},
		{MonitorID: "m1", IsSuccess: false, CheckedAt: now.Add(-1 * time.Minute)},
		{MonitorID: "m1", IsSuccess: false, CheckedAt: now.Add(-2 * time.Minute)},
	}
	for i := 0; i < 17; i++ {
		logs85 = append(logs85, CheckLog{
			MonitorID: "m1",
			IsSuccess: true,
			CheckedAt: now.Add(-time.Duration(i+3) * time.Minute),
		})
	}
	bars85 := ComputeDayBars(logs85, 1, 60)
	if bars85[0].Status != "degraded" || bars85[0].CSSClass != "tick-degraded-20" {
		t.Errorf("expected 85%% uptime to be 'degraded' & 'tick-degraded-20', got %s / %s (ratio: %.2f)",
			bars85[0].Status, bars85[0].CSSClass, bars85[0].UptimeRatio)
	}
}
