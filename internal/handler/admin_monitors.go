package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"uptime-monitor/internal/checker"
	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
)

type MonitorViewModel struct {
	model.Monitor
	DisplayStatus string           `json:"display_status"` // "up", "degraded", "down", "paused"
	Heartbeats    []model.CheckLog `json:"heartbeats"`     // 25 checks chronological (oldest -> newest)
	Uptime24h     float64          `json:"uptime_24h"`
	AvgLatency    int64            `json:"avg_latency"`
	CertDays      int              `json:"cert_days"`
	CertIssuer    string           `json:"cert_issuer"`
}

type AdminMonitorsHandler struct {
	monitorRepo    *db.MonitorRepo
	checkRepo      *db.CheckRepo
	statusPageRepo *db.StatusPageRepo
	scheduler      *checker.Scheduler
	renderer       *TemplateRenderer
	logger         *logger.Logger
}

func NewAdminMonitorsHandler(
	mRepo *db.MonitorRepo,
	cRepo *db.CheckRepo,
	spRepo *db.StatusPageRepo,
	sched *checker.Scheduler,
	tr *TemplateRenderer,
	log *logger.Logger,
) *AdminMonitorsHandler {
	return &AdminMonitorsHandler{
		monitorRepo:    mRepo,
		checkRepo:      cRepo,
		statusPageRepo: spRepo,
		scheduler:      sched,
		renderer:       tr,
		logger:         log,
	}
}

func (h *AdminMonitorsHandler) ServeList(w http.ResponseWriter, r *http.Request) {
	monitors, err := h.monitorRepo.GetAll()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pages, _ := h.statusPageRepo.GetAll()

	upCount := 0
	degradedCount := 0
	downCount := 0
	pausedCount := 0

	viewModels := make([]MonitorViewModel, 0, len(monitors))

	for _, m := range monitors {
		displayStatus := m.CurrentStatus
		if !m.IsActive {
			displayStatus = "paused"
			pausedCount++
		} else {
			switch m.CurrentStatus {
			case "up":
				upCount++
			case "degraded":
				degradedCount++
			case "down":
				downCount++
			}
		}

		// Fetch last 25 checks for visual heartbeat bar
		logs, _ := h.checkRepo.GetRecentLogs(m.ID, 25)
		var heartbeats []model.CheckLog
		var successCount int
		var latencySum int64
		certDays := 0
		certIssuer := ""

		if len(logs) > 0 {
			// Extract cert info from most recent check if available
			latest := logs[0]
			if latest.FailureDetails != nil && latest.FailureDetails.CertExpiryDays > 0 {
				certDays = latest.FailureDetails.CertExpiryDays
				certIssuer = latest.FailureDetails.CertIssuer
			}

			// Reverse logs so they read chronologically: oldest on left -> latest on right
			heartbeats = make([]model.CheckLog, len(logs))
			for i, l := range logs {
				heartbeats[len(logs)-1-i] = l
				if l.IsSuccess {
					successCount++
				}
				latencySum += l.LatencyMs
			}
		}

		uptimePct := 100.0
		var avgLatency int64
		if len(logs) > 0 {
			uptimePct = (float64(successCount) / float64(len(logs))) * 100.0
			avgLatency = latencySum / int64(len(logs))
		}

		viewModels = append(viewModels, MonitorViewModel{
			Monitor:       m,
			DisplayStatus: displayStatus,
			Heartbeats:    heartbeats,
			Uptime24h:     uptimePct,
			AvgLatency:    avgLatency,
			CertDays:      certDays,
			CertIssuer:    certIssuer,
		})
	}

	data := map[string]any{
		"Title":         "Monitors & Heartbeats",
		"ActiveTab":     "monitors",
		"Monitors":      viewModels,
		"StatusPages":   pages,
		"TotalCount":    len(monitors),
		"UpCount":       upCount,
		"DegradedCount": degradedCount,
		"DownCount":     downCount,
		"PausedCount":   pausedCount,
	}
	_ = h.renderer.Render(w, "admin_monitors", data)
}

func (h *AdminMonitorsHandler) ServeGet(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	monitorID := parts[2]

	m, err := h.monitorRepo.GetByID(monitorID)
	if err != nil || m == nil {
		http.Error(w, "monitor not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m)
}

func (h *AdminMonitorsHandler) ServeUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	monitorID := parts[2]

	m, err := h.monitorRepo.GetByID(monitorID)
	if err != nil || m == nil {
		http.Error(w, "monitor not found", http.StatusNotFound)
		return
	}

	interval, _ := strconv.Atoi(r.FormValue("interval_sec"))
	timeout, _ := strconv.Atoi(r.FormValue("timeout_sec"))
	expectedCode, _ := strconv.Atoi(r.FormValue("expected_code"))
	retryCount, _ := strconv.Atoi(r.FormValue("retry_count"))
	skipTLS := r.FormValue("skip_tls_verify") == "true" || r.FormValue("skip_tls_verify") == "1"

	name := strings.TrimSpace(r.FormValue("name"))
	if name != "" {
		m.Name = name
	}
	proto := strings.TrimSpace(r.FormValue("protocol"))
	if proto != "" {
		m.Protocol = proto
	}
	target := strings.TrimSpace(r.FormValue("target"))
	if target != "" {
		m.Target = target
	}
	m.Method = strings.TrimSpace(r.FormValue("method"))
	m.AuthType = r.FormValue("auth_type")
	m.AuthToken = strings.TrimSpace(r.FormValue("auth_token"))
	m.AuthUser = strings.TrimSpace(r.FormValue("auth_user"))
	m.AuthPass = strings.TrimSpace(r.FormValue("auth_pass"))
	m.HeadersJSON = strings.TrimSpace(r.FormValue("headers_json"))
	m.Body = strings.TrimSpace(r.FormValue("body"))
	m.SkipTLSVerify = skipTLS
	if interval > 0 {
		m.IntervalSec = interval
	}
	if timeout > 0 {
		m.TimeoutSec = timeout
	}
	if expectedCode > 0 {
		m.ExpectedCode = expectedCode
	}
	if retryCount > 0 {
		m.RetryCount = retryCount
	}
	m.Keyword = strings.TrimSpace(r.FormValue("keyword"))

	if err := h.monitorRepo.Update(m); err != nil {
		h.logger.Error("admin", "Failed to update monitor "+m.ID+": "+err.Error(), nil)
	} else {
		h.logger.Info("admin", "Updated monitor: "+m.Name, map[string]any{"id": m.ID, "protocol": m.Protocol})
	}

	redirectURL := "/admin/monitors"
	if ref := r.Header.Get("Referer"); ref != "" {
		redirectURL = ref
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *AdminMonitorsHandler) ServeToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	monitorID := parts[2]

	newActive, err := h.monitorRepo.ToggleActive(monitorID)
	if err != nil {
		http.Error(w, "failed to toggle monitor state", http.StatusInternalServerError)
		return
	}

	stateStr := "resumed"
	if !newActive {
		stateStr = "paused"
	}
	h.logger.Info("admin", fmt.Sprintf("Monitor %s was %s", monitorID, stateStr), map[string]any{"id": monitorID, "active": newActive})

	redirectURL := "/admin/monitors"
	if ref := r.Header.Get("Referer"); ref != "" {
		redirectURL = ref
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *AdminMonitorsHandler) ServeCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	interval, _ := strconv.Atoi(r.FormValue("interval_sec"))
	timeout, _ := strconv.Atoi(r.FormValue("timeout_sec"))
	expectedCode, _ := strconv.Atoi(r.FormValue("expected_code"))
	retryCount, _ := strconv.Atoi(r.FormValue("retry_count"))
	skipTLS := r.FormValue("skip_tls_verify") == "true"

	spID := strings.TrimSpace(r.FormValue("status_page_id"))

	m := &model.Monitor{
		StatusPageID:  spID,
		Name:          strings.TrimSpace(r.FormValue("name")),
		Protocol:      strings.TrimSpace(r.FormValue("protocol")),
		Target:        strings.TrimSpace(r.FormValue("target")),
		Method:        strings.TrimSpace(r.FormValue("method")),
		AuthType:      r.FormValue("auth_type"),
		AuthToken:     strings.TrimSpace(r.FormValue("auth_token")),
		AuthUser:      strings.TrimSpace(r.FormValue("auth_user")),
		AuthPass:      strings.TrimSpace(r.FormValue("auth_pass")),
		HeadersJSON:   strings.TrimSpace(r.FormValue("headers_json")),
		Body:          strings.TrimSpace(r.FormValue("body")),
		SkipTLSVerify: skipTLS,
		IntervalSec:   interval,
		TimeoutSec:    timeout,
		ExpectedCode:  expectedCode,
		Keyword:       strings.TrimSpace(r.FormValue("keyword")),
		RetryCount:    retryCount,
		MaxDays:       60,
		IsActive:      true,
		CurrentStatus: "pending",
	}

	if err := h.monitorRepo.Create(m); err != nil {
		h.logger.Error("admin", "Failed to create monitor: "+err.Error(), nil)
	} else {
		h.logger.Info("admin", "Created monitor: "+m.Name, map[string]any{"id": m.ID, "protocol": m.Protocol})

		// Attach to selected status pages
		_ = r.ParseForm()
		selectedPages := r.Form["status_page_ids"]
		for _, spID := range selectedPages {
			mappings, _ := h.statusPageRepo.GetMonitorsForPage(spID)
			mappings = append(mappings, model.StatusPageMonitor{
				StatusPageID: spID,
				MonitorID:    m.ID,
				DisplayOrder: len(mappings),
				GroupName:    "Services",
			})
			_ = h.statusPageRepo.SetMonitors(spID, mappings)
		}

		// Trigger initial check immediately in background with independent context
		go func(mid string) {
			_, _ = h.scheduler.TriggerNow(context.Background(), mid)
		}(m.ID)
	}

	redirectURL := "/admin"
	if m.StatusPageID != "" {
		redirectURL = "/admin/pages/" + m.StatusPageID
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *AdminMonitorsHandler) ServeTriggerCheck(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	monitorID := parts[2]

	res, err := h.scheduler.TriggerNow(context.Background(), monitorID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	if res.IsSuccess {
		_, _ = fmt.Fprintf(w, "Check OK (%dms)", res.LatencyMs)
	} else {
		_, _ = fmt.Fprintf(w, "Check Failed: %s", res.ErrorMessage)
	}
}

func (h *AdminMonitorsHandler) ServeDiagnostics(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	monitorID := parts[2]

	m, err := h.monitorRepo.GetByID(monitorID)
	if err != nil {
		http.Error(w, "monitor not found", http.StatusNotFound)
		return
	}

	logs, _ := h.checkRepo.GetRecentLogs(monitorID, 10)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := fmt.Sprintf(`
	<div class="space-y-4 font-mono text-xs">
		<div class="p-3 bg-[var(--bg-subtle)] rounded-lg border border-[var(--border-color)]">
			<div class="text-[var(--text-muted)] text-[10px] uppercase font-bold">Target Diagnostic Inspection</div>
			<div class="text-sm font-semibold text-[var(--text-main)] mt-0.5">%s (%s)</div>
			<div class="text-[var(--text-muted)] mt-1">%s</div>
		</div>

		<div class="text-xs font-bold uppercase text-[var(--text-muted)]">Recent Check Waterfall & Failures</div>
	`, m.Name, m.Protocol, m.Target)

	if len(logs) == 0 {
		html += `<div class="p-4 text-center text-[var(--text-muted)]">No check logs recorded yet.</div>`
	} else {
		for _, l := range logs {
			statusBadge := `<span class="px-2 py-0.5 rounded bg-emerald-500/20 text-emerald-400 font-bold">SUCCESS</span>`
			if !l.IsSuccess {
				statusBadge = fmt.Sprintf(`<span class="px-2 py-0.5 rounded bg-red-500/20 text-red-400 font-bold">%s</span>`, l.ErrorType)
			}

			phaseDetails := ""
			if l.FailureDetails != nil {
				d := l.FailureDetails
				phaseDetails = fmt.Sprintf(`
				<div class="mt-2 pt-2 border-t border-[var(--border-color)] grid grid-cols-3 gap-2 text-[10px] text-[var(--text-muted)]">
					<div>DNS: <span class="text-[var(--text-main)] font-semibold">%dms</span></div>
					<div>TCP: <span class="text-[var(--text-main)] font-semibold">%dms</span></div>
					<div>TLS: <span class="text-[var(--text-main)] font-semibold">%dms</span></div>
				</div>`, d.DNSLookupMs, d.TCPConnectMs, d.TLSHandshakeMs)

				if d.CertExpiry != "" {
					phaseDetails += fmt.Sprintf(`<div class="mt-1 text-[10px] text-blue-400">SSL Issuer: %s (expires: %s, %d days left)</div>`, d.CertIssuer, d.CertExpiry, d.CertExpiryDays)
				}

				if d.ResponseBodyHead != "" {
					color := "text-emerald-300"
					title := "Banner / Response Snippet"
					if !l.IsSuccess {
						color = "text-red-300"
						title = "Response Snippet"
					}
					phaseDetails += fmt.Sprintf(`<div class="mt-2 bg-black/40 p-2 rounded text-[10px] %s overflow-x-auto whitespace-pre-wrap"><span class="text-[var(--text-muted)]">%s:</span> %s</div>`, color, title, d.ResponseBodyHead)
				}
			}

			html += fmt.Sprintf(`
			<div class="p-3 bg-[var(--bg-subtle)]/70 rounded-lg border border-[var(--border-color)]">
				<div class="flex items-center justify-between">
					<div>%s <span class="text-[var(--text-muted)] ml-2">%dms</span></div>
					<div class="text-[var(--text-muted)] text-[10px]">%s</div>
				</div>
				%s
				%s
			</div>`,
				statusBadge, l.LatencyMs, l.CheckedAt.Format("15:04:05.000 UTC"),
				func() string {
					if l.ErrorMessage != "" {
						return fmt.Sprintf(`<div class="text-red-400 mt-1 text-[11px]">%s</div>`, l.ErrorMessage)
					}
					return ""
				}(),
				phaseDetails,
			)
		}
	}

	html += `</div>`
	_, _ = w.Write([]byte(html))
}

func (h *AdminMonitorsHandler) ServeDelete(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	monitorID := parts[2]

	m, _ := h.monitorRepo.GetByID(monitorID)
	_ = h.monitorRepo.Delete(monitorID)
	h.logger.Info("admin", "Deleted monitor: "+monitorID, nil)

	redirectURL := "/admin"
	if m != nil && m.StatusPageID != "" {
		redirectURL = "/admin/pages/" + m.StatusPageID
	} else if ref := r.Header.Get("Referer"); ref != "" {
		redirectURL = ref
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}
