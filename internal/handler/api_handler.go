package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"uptime-monitor/internal/checker"
	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
)

type APIHandler struct {
	tokenRepo      *db.APITokenRepo
	monitorRepo    *db.MonitorRepo
	checkRepo      *db.CheckRepo
	statusPageRepo *db.StatusPageRepo
	incidentRepo   *db.IncidentRepo
	scheduler      *checker.Scheduler
	logger         *logger.Logger
}

func NewAPIHandler(
	tokenRepo *db.APITokenRepo,
	mRepo *db.MonitorRepo,
	cRepo *db.CheckRepo,
	spRepo *db.StatusPageRepo,
	incRepo *db.IncidentRepo,
	sched *checker.Scheduler,
	log *logger.Logger,
) *APIHandler {
	return &APIHandler{
		tokenRepo:      tokenRepo,
		monitorRepo:    mRepo,
		checkRepo:      cRepo,
		statusPageRepo: spRepo,
		incidentRepo:   incRepo,
		scheduler:      sched,
		logger:         log,
	}
}

func (h *APIHandler) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (h *APIHandler) writeError(w http.ResponseWriter, status int, msg string) {
	h.writeJSON(w, status, map[string]any{"error": msg})
}

// Authenticate verifies the incoming Bearer token or X-API-Key header against the database.
func (h *APIHandler) authenticate(r *http.Request) (*model.APIToken, bool) {
	rawToken := ""
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		rawToken = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	} else if keyHeader := r.Header.Get("X-API-Key"); keyHeader != "" {
		rawToken = strings.TrimSpace(keyHeader)
	}

	if rawToken == "" {
		return nil, false
	}

	hash := db.HashToken(rawToken)
	token, err := h.tokenRepo.GetByHash(hash)
	if err != nil || token == nil {
		// Fallback: master environment API_TOKEN if set
		if envMaster := os.Getenv("API_TOKEN"); envMaster != "" && rawToken == envMaster {
			prefix := rawToken
			if len(prefix) > 10 {
				prefix = prefix[:10] + "..."
			}
			return &model.APIToken{
				ID:          "env-master-token",
				Name:        "Master Environment Token",
				TokenPrefix: prefix,
				CreatedAt:   time.Now().UTC(),
			}, true
		}
		return nil, false
	}

	// Update last_used_at timestamp in background
	go func(id string) {
		_ = h.tokenRepo.TouchLastUsed(id)
	}(token.ID)

	return token, true
}

func (h *APIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(r.URL.Path, "/")
	// Expected prefix: api/v1
	segments := strings.Split(path, "/")
	if len(segments) < 2 || segments[0] != "api" || segments[1] != "v1" {
		http.NotFound(w, r)
		return
	}

	subPath := segments[2:]

	// Public Health Endpoint: /api/v1/health
	if len(subPath) == 1 && subPath[0] == "health" {
		h.writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
			"time":   time.Now().UTC().Format(time.RFC3339),
		})
		return
	}

	// Authenticate all other /api/v1 endpoints
	token, ok := h.authenticate(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="uptime-monitor-api"`)
		h.writeError(w, http.StatusUnauthorized, "Unauthorized: invalid or missing API token")
		return
	}
	_ = token

	if len(subPath) == 0 {
		h.writeJSON(w, http.StatusOK, map[string]any{
			"version": "v1",
			"message": "Uptime Monitor REST API",
		})
		return
	}

	resource := subPath[0]
	switch resource {
	case "summary":
		h.handleSummary(w, r)
	case "monitors":
		h.handleMonitors(w, r, subPath[1:])
	case "status-pages":
		h.handleStatusPages(w, r, subPath[1:])
	case "incidents":
		h.handleIncidents(w, r, subPath[1:])
	case "tokens":
		h.handleTokens(w, r, subPath[1:])
	default:
		h.writeError(w, http.StatusNotFound, "Resource not found")
	}
}

// GET /api/v1/summary
func (h *APIHandler) handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	monitors, _ := h.monitorRepo.GetAll()
	pages, _ := h.statusPageRepo.GetAll()

	up, down, degraded, pending := 0, 0, 0, 0
	for _, m := range monitors {
		switch m.CurrentStatus {
		case "up":
			up++
		case "down":
			down++
		case "degraded":
			degraded++
		default:
			pending++
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"total_monitors": len(monitors),
		"monitors": map[string]int{
			"up":       up,
			"down":     down,
			"degraded": degraded,
			"pending":  pending,
		},
		"total_status_pages": len(pages),
		"time":               time.Now().UTC().Format(time.RFC3339),
	})
}

// /api/v1/monitors
func (h *APIHandler) handleMonitors(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 {
		switch r.Method {
		case http.MethodGet:
			spID := r.URL.Query().Get("status_page_id")
			var list []model.Monitor
			var err error
			if spID != "" {
				list, err = h.monitorRepo.GetByStatusPage(spID)
			} else {
				list, err = h.monitorRepo.GetAll()
			}
			if err != nil {
				h.writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if list == nil {
				list = []model.Monitor{}
			}
			h.writeJSON(w, http.StatusOK, list)

		case http.MethodPost:
			var m model.Monitor
			if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
				h.writeError(w, http.StatusBadRequest, "Invalid JSON body: "+err.Error())
				return
			}
			m.Name = strings.TrimSpace(m.Name)
			m.Target = strings.TrimSpace(m.Target)
			if m.Name == "" || m.Target == "" {
				h.writeError(w, http.StatusBadRequest, "Name and Target are required")
				return
			}
			if m.Protocol == "" {
				m.Protocol = "https"
			}
			if m.IntervalSec <= 0 {
				m.IntervalSec = 60
			}
			if m.TimeoutSec <= 0 {
				m.TimeoutSec = 10
			}
			if m.ExpectedCode <= 0 {
				m.ExpectedCode = 200
			}

			if err := h.monitorRepo.Create(&m); err != nil {
				h.writeError(w, http.StatusInternalServerError, "Failed to create monitor: "+err.Error())
				return
			}
			if m.StatusPageID != "" && h.statusPageRepo != nil {
				existing, _ := h.statusPageRepo.GetMonitorsForPage(m.StatusPageID)
				alreadyExists := false
				for _, ex := range existing {
					if ex.MonitorID == m.ID {
						alreadyExists = true
						break
					}
				}
				if !alreadyExists {
					existing = append(existing, model.StatusPageMonitor{
						StatusPageID: m.StatusPageID,
						MonitorID:    m.ID,
						DisplayOrder: len(existing),
						GroupName:    "Services",
					})
					_ = h.statusPageRepo.SetMonitors(m.StatusPageID, existing)
				}
			}
			if h.logger != nil {
				h.logger.Info("api", "Created monitor via API: "+m.Name, map[string]any{"id": m.ID, "target": m.Target})
			}
			h.writeJSON(w, http.StatusCreated, m)

		default:
			h.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}

	id := parts[0]
	if len(parts) == 2 {
		action := parts[1]
		if r.Method != http.MethodPost {
			h.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		switch action {
		case "pause":
			_, _ = h.monitorRepo.ToggleActive(id)
			h.writeJSON(w, http.StatusOK, map[string]any{"id": id, "active": false})
		case "resume":
			_, _ = h.monitorRepo.ToggleActive(id)
			h.writeJSON(w, http.StatusOK, map[string]any{"id": id, "active": true})
		default:
			h.writeError(w, http.StatusNotFound, "Action not found")
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		m, err := h.monitorRepo.GetByID(id)
		if err != nil || m == nil {
			h.writeError(w, http.StatusNotFound, "Monitor not found")
			return
		}
		latestCheck, _ := h.checkRepo.GetLatestCheck(id)
		h.writeJSON(w, http.StatusOK, map[string]any{
			"monitor":      m,
			"latest_check": latestCheck,
		})

	case http.MethodPut:
		existing, err := h.monitorRepo.GetByID(id)
		if err != nil || existing == nil {
			h.writeError(w, http.StatusNotFound, "Monitor not found")
			return
		}
		if err := json.NewDecoder(r.Body).Decode(existing); err != nil {
			h.writeError(w, http.StatusBadRequest, "Invalid JSON body: "+err.Error())
			return
		}
		existing.ID = id
		if err := h.monitorRepo.Update(existing); err != nil {
			h.writeError(w, http.StatusInternalServerError, "Failed to update monitor: "+err.Error())
			return
		}
		h.writeJSON(w, http.StatusOK, existing)

	case http.MethodDelete:
		if err := h.monitorRepo.Delete(id); err != nil {
			h.writeError(w, http.StatusInternalServerError, "Failed to delete monitor: "+err.Error())
			return
		}
		h.writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})

	default:
		h.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// /api/v1/status-pages
func (h *APIHandler) handleStatusPages(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 {
		switch r.Method {
		case http.MethodGet:
			pages, err := h.statusPageRepo.GetAll()
			if err != nil {
				h.writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if pages == nil {
				pages = []model.StatusPage{}
			}
			h.writeJSON(w, http.StatusOK, pages)

		case http.MethodPost:
			var p model.StatusPage
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				h.writeError(w, http.StatusBadRequest, "Invalid JSON body: "+err.Error())
				return
			}
			p.Name = strings.TrimSpace(p.Name)
			p.Slug = strings.TrimSpace(p.Slug)
			if p.Name == "" || p.Slug == "" {
				h.writeError(w, http.StatusBadRequest, "Name and Slug are required")
				return
			}
			if p.Theme == "" {
				p.Theme = "openstatus-slate"
			}
			if p.AccentColor == "" {
				p.AccentColor = "#10b981"
			}
			if p.HistoryDays <= 0 {
				p.HistoryDays = 60
			}

			if err := h.statusPageRepo.Create(&p); err != nil {
				h.writeError(w, http.StatusInternalServerError, "Failed to create status page: "+err.Error())
				return
			}
			h.writeJSON(w, http.StatusCreated, p)

		default:
			h.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}

	id := parts[0]
	if len(parts) >= 2 && parts[1] == "monitors" {
		sp, err := h.statusPageRepo.FindByIDOrSlug(id)
		if err != nil || sp == nil {
			h.writeError(w, http.StatusNotFound, "Status page not found")
			return
		}
		if r.Method == http.MethodGet {
			monitors, _ := h.statusPageRepo.GetMonitorsForPage(sp.ID)
			h.writeJSON(w, http.StatusOK, monitors)
			return
		}
		if r.Method == http.MethodPost {
			var linkReq struct {
				MonitorID    string `json:"monitor_id"`
				GroupName    string `json:"group_name"`
				DisplayOrder int    `json:"display_order"`
			}
			if err := json.NewDecoder(r.Body).Decode(&linkReq); err != nil {
				h.writeError(w, http.StatusBadRequest, "Invalid JSON body: "+err.Error())
				return
			}
			if linkReq.MonitorID == "" {
				h.writeError(w, http.StatusBadRequest, "monitor_id is required")
				return
			}
			if linkReq.GroupName == "" {
				linkReq.GroupName = "Services"
			}
			existing, _ := h.statusPageRepo.GetMonitorsForPage(sp.ID)
			found := false
			for i, ex := range existing {
				if ex.MonitorID == linkReq.MonitorID {
					existing[i].GroupName = linkReq.GroupName
					existing[i].DisplayOrder = linkReq.DisplayOrder
					found = true
					break
				}
			}
			if !found {
				existing = append(existing, model.StatusPageMonitor{
					StatusPageID: sp.ID,
					MonitorID:    linkReq.MonitorID,
					DisplayOrder: linkReq.DisplayOrder,
					GroupName:    linkReq.GroupName,
				})
			}
			if err := h.statusPageRepo.SetMonitors(sp.ID, existing); err != nil {
				h.writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if mon, err := h.monitorRepo.GetByID(linkReq.MonitorID); err == nil && mon != nil && mon.StatusPageID == "" {
				mon.StatusPageID = sp.ID
				_ = h.monitorRepo.Update(mon)
			}
			h.writeJSON(w, http.StatusOK, existing)
			return
		}
		h.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	switch r.Method {
	case http.MethodGet:
		p, err := h.statusPageRepo.FindByIDOrSlug(id)
		if err != nil || p == nil {
			h.writeError(w, http.StatusNotFound, "Status page not found")
			return
		}
		h.writeJSON(w, http.StatusOK, p)

	case http.MethodPut:
		p, err := h.statusPageRepo.FindByIDOrSlug(id)
		if err != nil || p == nil {
			h.writeError(w, http.StatusNotFound, "Status page not found")
			return
		}
		if err := json.NewDecoder(r.Body).Decode(p); err != nil {
			h.writeError(w, http.StatusBadRequest, "Invalid JSON body: "+err.Error())
			return
		}
		if err := h.statusPageRepo.Update(p); err != nil {
			h.writeError(w, http.StatusInternalServerError, "Failed to update status page: "+err.Error())
			return
		}
		h.writeJSON(w, http.StatusOK, p)

	case http.MethodDelete:
		if err := h.statusPageRepo.Delete(id); err != nil {
			h.writeError(w, http.StatusInternalServerError, "Failed to delete status page: "+err.Error())
			return
		}
		h.writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})

	default:
		h.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// /api/v1/incidents
func (h *APIHandler) handleIncidents(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 {
		switch r.Method {
		case http.MethodGet:
			spID := r.URL.Query().Get("status_page_id")
			var list []model.Incident
			var err error
			if spID != "" {
				list, err = h.incidentRepo.GetByPageID(spID)
			} else {
				list, err = h.incidentRepo.GetAll()
			}
			if err != nil {
				h.writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if list == nil {
				list = []model.Incident{}
			}
			h.writeJSON(w, http.StatusOK, list)

		case http.MethodPost:
			var inc model.Incident
			if err := json.NewDecoder(r.Body).Decode(&inc); err != nil {
				h.writeError(w, http.StatusBadRequest, "Invalid JSON body: "+err.Error())
				return
			}
			inc.Title = strings.TrimSpace(inc.Title)
			if inc.Title == "" {
				h.writeError(w, http.StatusBadRequest, "Title is required")
				return
			}
			if inc.Status == "" {
				inc.Status = "investigating"
			}
			if inc.Severity == "" {
				inc.Severity = "minor"
			}

			initialMsg := "Incident reported"
			if len(inc.Updates) > 0 && inc.Updates[0].Message != "" {
				initialMsg = inc.Updates[0].Message
			}

			if err := h.incidentRepo.Create(&inc); err != nil {
				h.writeError(w, http.StatusInternalServerError, "Failed to create incident: "+err.Error())
				return
			}
			if initialMsg != "" {
				_ = h.incidentRepo.AddUpdate(inc.ID, inc.Status, initialMsg)
			}
			h.writeJSON(w, http.StatusCreated, inc)

		default:
			h.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}

	id := parts[0]
	if len(parts) == 2 && parts[1] == "updates" && r.Method == http.MethodPost {
		var update struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			h.writeError(w, http.StatusBadRequest, "Invalid JSON body: "+err.Error())
			return
		}
		if update.Status == "" || update.Message == "" {
			h.writeError(w, http.StatusBadRequest, "Status and Message are required")
			return
		}
		if err := h.incidentRepo.AddUpdate(id, update.Status, update.Message); err != nil {
			h.writeError(w, http.StatusInternalServerError, "Failed to add incident update: "+err.Error())
			return
		}
		h.writeJSON(w, http.StatusCreated, map[string]any{"incident_id": id, "status": update.Status, "message": update.Message})
		return
	}

	switch r.Method {
	case http.MethodGet:
		inc, err := h.incidentRepo.GetByID(id)
		if err != nil || inc == nil {
			h.writeError(w, http.StatusNotFound, "Incident not found")
			return
		}
		h.writeJSON(w, http.StatusOK, inc)

	case http.MethodDelete:
		if err := h.incidentRepo.Delete(id); err != nil {
			h.writeError(w, http.StatusInternalServerError, "Failed to delete incident: "+err.Error())
			return
		}
		h.writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})

	default:
		h.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// /api/v1/tokens
func (h *APIHandler) handleTokens(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 {
		switch r.Method {
		case http.MethodGet:
			tokens, err := h.tokenRepo.GetAll()
			if err != nil {
				h.writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if tokens == nil {
				tokens = []model.APIToken{}
			}
			h.writeJSON(w, http.StatusOK, tokens)

		case http.MethodPost:
			var req struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				h.writeError(w, http.StatusBadRequest, "Invalid JSON body: "+err.Error())
				return
			}
			req.Name = strings.TrimSpace(req.Name)
			if req.Name == "" {
				h.writeError(w, http.StatusBadRequest, "Token Name is required")
				return
			}

			token, err := h.tokenRepo.Create(req.Name)
			if err != nil {
				h.writeError(w, http.StatusInternalServerError, "Failed to generate token: "+err.Error())
				return
			}
			h.writeJSON(w, http.StatusCreated, token)

		default:
			h.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}

	id := parts[0]
	if r.Method == http.MethodDelete {
		if err := h.tokenRepo.Delete(id); err != nil {
			h.writeError(w, http.StatusInternalServerError, "Failed to delete token: "+err.Error())
			return
		}
		h.writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
		return
	}

	h.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
}
