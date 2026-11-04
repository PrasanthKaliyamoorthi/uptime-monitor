package handler

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/model"
)

type BadgeHandler struct {
	monitorRepo *db.MonitorRepo
	checkRepo   *db.CheckRepo
}

func NewBadgeHandler(mRepo *db.MonitorRepo, cRepo *db.CheckRepo) *BadgeHandler {
	return &BadgeHandler{
		monitorRepo: mRepo,
		checkRepo:   cRepo,
	}
}

func renderSVGBadge(w http.ResponseWriter, label, value, colorHex string) {
	w.Header().Set("Content-Type", "image/svg+xml;charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	// Calculate widths roughly based on character counts
	labelWidth := len(label)*7 + 14
	valueWidth := len(value)*7 + 14
	totalWidth := labelWidth + valueWidth

	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s">
  <linearGradient id="b" x2="0" y2="100%%">
    <stop offset="0" stop-color="#bbb" stop-opacity=".1"/>
    <stop offset="1" stop-opacity=".1"/>
  </linearGradient>
  <mask id="a">
    <rect width="%d" height="20" rx="3" fill="#fff"/>
  </mask>
  <g mask="url(#a)">
    <rect width="%d" height="20" fill="#2d3748"/>
    <rect x="%d" width="%d" height="20" fill="%s"/>
    <rect width="%d" height="20" fill="url(#b)"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="DejaVu Sans,Verdana,Geneva,sans-serif" font-size="11">
    <text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text>
    <text x="%d" y="14">%s</text>
    <text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text>
    <text x="%d" y="14">%s</text>
  </g>
</svg>`,
		totalWidth, label, value,
		totalWidth,
		labelWidth,
		labelWidth, valueWidth, colorHex,
		totalWidth,
		labelWidth/2, label,
		labelWidth/2, label,
		labelWidth+valueWidth/2, value,
		labelWidth+valueWidth/2, value,
	)

	_, _ = w.Write([]byte(svg))
}

func (h *BadgeHandler) ServeStatusBadge(w http.ResponseWriter, r *http.Request) {
	// e.g. /api/badge/{id}/status
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, "invalid badge URL", http.StatusBadRequest)
		return
	}
	monitorID := parts[2]

	m, err := h.monitorRepo.GetByID(monitorID)
	if err != nil {
		renderSVGBadge(w, "status", "not found", "#e53e3e")
		return
	}

	val := m.CurrentStatus
	color := "#718096"
	switch val {
	case "up":
		val = "operational"
		color = "#10b981"
	case "degraded":
		val = "degraded"
		color = "#f59e0b"
	case "down":
		val = "down"
		color = "#ef4444"
	default:
		val = "pending"
		color = "#6b7280"
	}

	renderSVGBadge(w, "status", val, color)
}

func (h *BadgeHandler) ServeUptimeBadge(w http.ResponseWriter, r *http.Request) {
	// e.g. /api/badge/{id}/uptime
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, "invalid badge URL", http.StatusBadRequest)
		return
	}
	monitorID := parts[2]

	m, err := h.monitorRepo.GetByID(monitorID)
	if err != nil {
		renderSVGBadge(w, "uptime", "not found", "#e53e3e")
		return
	}

	// Calculate 30-day uptime percentage
	since := time.Now().UTC().AddDate(0, 0, -30)
	logs, _ := h.checkRepo.GetLogsForWindow(m.ID, since)
	dayBars := model.ComputeDayBars(logs, 30, m.IntervalSec)

	var totalRatio float64
	var counted int
	for _, b := range dayBars {
		if b.Status != "no_data" {
			totalRatio += b.UptimeRatio
			counted++
		}
	}

	uptimeStr := "100.00%"
	color := "#10b981"
	if counted > 0 {
		avg := totalRatio / float64(counted)
		uptimeStr = fmt.Sprintf("%.2f%%", avg)
		if avg < 95.0 {
			color = "#ef4444"
		} else if avg < 99.0 {
			color = "#f59e0b"
		}
	}

	renderSVGBadge(w, "uptime (30d)", uptimeStr, color)
}
