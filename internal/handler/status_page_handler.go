package handler

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/model"
)

type StatusPageHandler struct {
	statusPageRepo *db.StatusPageRepo
	monitorRepo    *db.MonitorRepo
	checkRepo      *db.CheckRepo
	incidentRepo   *db.IncidentRepo
	settingsRepo   *db.SettingsRepo
	renderer       *TemplateRenderer
}

func NewStatusPageHandler(
	spRepo *db.StatusPageRepo,
	mRepo *db.MonitorRepo,
	cRepo *db.CheckRepo,
	incRepo *db.IncidentRepo,
	sRepo *db.SettingsRepo,
	tr *TemplateRenderer,
) *StatusPageHandler {
	return &StatusPageHandler{
		statusPageRepo: spRepo,
		monitorRepo:    mRepo,
		checkRepo:      cRepo,
		incidentRepo:   incRepo,
		settingsRepo:   sRepo,
		renderer:       tr,
	}
}

func (h *StatusPageHandler) ServeStatusPage(w http.ResponseWriter, r *http.Request, page *model.StatusPage) {
	// Fetch monitors belonging directly to this page
	targetMonitors, err := h.monitorRepo.GetByStatusPage(page.ID)
	if err != nil || len(targetMonitors) == 0 {
		mappings, _ := h.statusPageRepo.GetMonitorsForPage(page.ID)
		if len(mappings) > 0 {
			for _, mapItem := range mappings {
				if m, err := h.monitorRepo.GetByID(mapItem.MonitorID); err == nil && m.IsActive {
					targetMonitors = append(targetMonitors, *m)
				}
			}
		} else if page.IsDefault {
			targetMonitors, _ = h.monitorRepo.GetActive()
		}
	}

	historyDays := page.HistoryDays
	if historyDays <= 0 {
		historyDays = 60
	}
	if historyDays > 90 {
		historyDays = 90
	}

	var monitors []model.MonitorSummary
	overallStatus := "up"

	for _, m := range targetMonitors {
		if m.CurrentStatus == "down" {
			overallStatus = "down"
		} else if m.CurrentStatus == "degraded" && overallStatus != "down" {
			overallStatus = "degraded"
		}

		maxDays := historyDays
		if m.MaxDays > 0 && m.MaxDays < maxDays {
			maxDays = m.MaxDays
		}

		since := time.Now().UTC().AddDate(0, 0, -maxDays)
		logs, _ := h.checkRepo.GetLogsForWindow(m.ID, since)
		dayBars := model.ComputeDayBars(logs, maxDays, m.IntervalSec)

		var totalRatio float64
		var counted int
		for _, b := range dayBars {
			if b.Status != "no_data" {
				totalRatio += b.UptimeRatio
				counted++
			}
		}
		overallUptime := 100.0
		if counted > 0 {
			overallUptime = totalRatio / float64(counted)
		}

		latestCheck, _ := h.checkRepo.GetLatestCheck(m.ID)

		monitors = append(monitors, model.MonitorSummary{
			Monitor:       m,
			OverallUptime: overallUptime,
			AvgLatencyMs:  m.LastLatencyMs,
			DayBars:       dayBars,
			LatestCheck:   latestCheck,
		})
	}

	since := time.Now().UTC().AddDate(0, 0, -historyDays)

	// Fetch active incidents & ongoing maintenance
	activeIncidents, _ := h.incidentRepo.GetActiveForStatusPage(page.ID)

	// Fetch past resolved incidents within configured days
	pastIncidents, _ := h.incidentRepo.GetPastForStatusPage(page.ID, since)

	refreshURL := "/"
	if page.Slug != "" && page.Slug != "default" {
		refreshURL = "/status?page=" + page.Slug
	}

	canAccessAdmin := false
	if h.settingsRepo != nil {
		allowedDomain, err := h.settingsRepo.GetAdminAllowedDomain()
		if err == nil {
			allowedDomain = strings.TrimSpace(strings.ToLower(allowedDomain))
			reqHost := strings.ToLower(ExtractRequestHost(r))
			if allowedDomain == "" || reqHost == allowedDomain {
				canAccessAdmin = true
			}
		}
	} else {
		canAccessAdmin = true
	}

	data := map[string]any{
		"Title":           page.Name,
		"Theme":           page.Theme,
		"StatusPage":      page,
		"OverallStatus":   overallStatus,
		"Monitors":        monitors,
		"Incidents":       activeIncidents,
		"ActiveIncidents": activeIncidents,
		"PastIncidents":   pastIncidents,
		"HistoryDays":     historyDays,
		"RefreshURL":      refreshURL,
		"CanAccessAdmin":  canAccessAdmin,
	}

	_ = h.renderer.Render(w, "status_page", data)
}

// RSS Feed structures
type rssChannel struct {
	XMLName     xml.Name  `xml:"channel"`
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	PubDate     string    `xml:"pubDate"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	GUID        string `xml:"guid"`
}

type rssRoot struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

func (h *StatusPageHandler) ServeRSSFeed(w http.ResponseWriter, r *http.Request, page *model.StatusPage) {
	incidents, _ := h.incidentRepo.GetAll()

	var items []rssItem
	for _, inc := range incidents {
		desc := ""
		if len(inc.Updates) > 0 {
			desc = inc.Updates[0].Message
		}
		items = append(items, rssItem{
			Title:       fmt.Sprintf("[%s] %s", inc.Severity, inc.Title),
			Description: desc,
			PubDate:     inc.CreatedAt.Format(time.RFC1123Z),
			GUID:        inc.ID,
		})
	}

	feed := rssRoot{
		Version: "2.0",
		Channel: rssChannel{
			Title:       page.Name + " - Status Updates",
			Link:        "http://" + r.Host + "/",
			Description: "Live incident and maintenance updates for " + page.Name,
			PubDate:     time.Now().Format(time.RFC1123Z),
			Items:       items,
		},
	}

	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(feed)
}
