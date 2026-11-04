package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
)

type PageSummary struct {
	Page          model.StatusPage
	Status        string // "up", "degraded", "down"
	MonitorCount  int
	IncidentCount int
}

type AdminPagesHandler struct {
	statusPageRepo *db.StatusPageRepo
	monitorRepo    *db.MonitorRepo
	incidentRepo   *db.IncidentRepo
	renderer       *TemplateRenderer
	logger         *logger.Logger
}

func NewAdminPagesHandler(
	spRepo *db.StatusPageRepo,
	mRepo *db.MonitorRepo,
	incRepo *db.IncidentRepo,
	tr *TemplateRenderer,
	log *logger.Logger,
) *AdminPagesHandler {
	return &AdminPagesHandler{
		statusPageRepo: spRepo,
		monitorRepo:    mRepo,
		incidentRepo:   incRepo,
		renderer:       tr,
		logger:         log,
	}
}

// ServeDashboard renders the global status summary across all status pages/tenants
func (h *AdminPagesHandler) ServeDashboard(w http.ResponseWriter, r *http.Request) {
	pages, _ := h.statusPageRepo.GetAll()

	totalMonitors := 0
	upMonitors := 0
	degradedMonitors := 0
	downMonitors := 0

	var summaries []PageSummary
	for _, p := range pages {
		monitors, _ := h.monitorRepo.GetByStatusPage(p.ID)
		if len(monitors) == 0 && p.IsDefault {
			monitors, _ = h.monitorRepo.GetActive()
		}

		pageStatus := "up"
		for _, m := range monitors {
			totalMonitors++
			switch m.CurrentStatus {
			case "up":
				upMonitors++
			case "degraded":
				degradedMonitors++
				if pageStatus != "down" {
					pageStatus = "degraded"
				}
			case "down":
				downMonitors++
				pageStatus = "down"
			}
		}

		activeIncidents, _ := h.incidentRepo.GetActiveForStatusPage(p.ID)

		summaries = append(summaries, PageSummary{
			Page:          p,
			Status:        pageStatus,
			MonitorCount:  len(monitors),
			IncidentCount: len(activeIncidents),
		})
	}

	data := map[string]any{
		"Title":            "Dashboard Overview",
		"ActiveTab":        "dashboard",
		"PageSummaries":    summaries,
		"TotalMonitors":    totalMonitors,
		"UpMonitors":       upMonitors,
		"DegradedMonitors": degradedMonitors,
		"DownMonitors":     downMonitors,
		"Error":            r.URL.Query().Get("err"),
		"Message":          r.URL.Query().Get("msg"),
	}

	_ = h.renderer.Render(w, "admin_dashboard", data)
}

// SanitizeDomain strips protocol, path, and port, converting to lowercase
func SanitizeDomain(raw string) string {
	d := strings.TrimSpace(strings.ToLower(raw))
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")
	if idx := strings.Index(d, "/"); idx != -1 {
		d = d[:idx]
	}
	if idx := strings.Index(d, ":"); idx != -1 {
		d = d[:idx]
	}
	return strings.TrimSpace(d)
}

func extractLogo(r *http.Request, existingLogo string) string {
	_ = r.ParseMultipartForm(5 << 20)
	if r.FormValue("remove_logo") == "true" {
		return ""
	}
	file, header, err := r.FormFile("logo_file")
	if err == nil && file != nil {
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, 2<<20)) // 2MB max
		if err == nil && len(data) > 0 {
			contentType := header.Header.Get("Content-Type")
			filename := strings.ToLower(header.Filename)
			if strings.HasSuffix(filename, ".svg") {
				contentType = "image/svg+xml"
			} else if strings.HasSuffix(filename, ".png") {
				contentType = "image/png"
			} else if strings.HasSuffix(filename, ".jpg") || strings.HasSuffix(filename, ".jpeg") {
				contentType = "image/jpeg"
			} else if strings.HasSuffix(filename, ".webp") {
				contentType = "image/webp"
			} else if strings.HasSuffix(filename, ".gif") {
				contentType = "image/gif"
			} else if strings.HasSuffix(filename, ".ico") {
				contentType = "image/x-icon"
			}
			if contentType == "" || contentType == "application/octet-stream" {
				contentType = http.DetectContentType(data)
			}
			if strings.HasPrefix(contentType, "image/") {
				return fmt.Sprintf("data:%s;base64,%s", contentType, base64.StdEncoding.EncodeToString(data))
			}
		}
	}
	if logoURL := strings.TrimSpace(r.FormValue("logo_url")); logoURL != "" {
		return logoURL
	}
	return existingLogo
}

// ServeNew renders the dedicated status page creation form
func (h *AdminPagesHandler) ServeNew(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		"Title":     "Create Status Page",
		"ActiveTab": "dashboard",
		"Error":     r.URL.Query().Get("err"),
	}
	_ = h.renderer.Render(w, "admin_new_page", data)
}

func (h *AdminPagesHandler) renderNewWithError(w http.ResponseWriter, r *http.Request, errMsg, name, slug, domain, desc, theme, logoURL string, isDefault bool, historyDays int) {
	if historyDays <= 0 {
		historyDays = 60
	}
	data := map[string]any{
		"Title":       "Create Status Page",
		"ActiveTab":   "dashboard",
		"Error":       errMsg,
		"Name":        name,
		"Slug":        slug,
		"Domain":      domain,
		"Description": desc,
		"LogoURL":     logoURL,
		"Theme":       theme,
		"IsDefault":   isDefault,
		"HistoryDays": historyDays,
	}
	_ = h.renderer.Render(w, "admin_new_page", data)
}

// ServePageWorkspace renders the workspace for an isolated status page tenant
func (h *AdminPagesHandler) ServePageWorkspace(w http.ResponseWriter, r *http.Request, pageID string) {
	page, err := h.statusPageRepo.GetByID(pageID)
	if err != nil {
		http.Redirect(w, r, "/admin", http.StatusFound)
		return
	}

	monitors, _ := h.monitorRepo.GetByStatusPage(page.ID)
	incidents, _ := h.incidentRepo.GetByPageID(page.ID)

	msg := r.URL.Query().Get("msg")
	errMsg := r.URL.Query().Get("err")

	data := map[string]any{
		"Title":     page.Name + " • Workspace",
		"ActiveTab": "dashboard",
		"Page":      page,
		"Monitors":  monitors,
		"Incidents": incidents,
		"Message":   msg,
		"Error":     errMsg,
	}

	_ = h.renderer.Render(w, "admin_page_workspace", data)
}

func (h *AdminPagesHandler) ServeCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := strings.TrimSpace(strings.ToLower(r.FormValue("slug")))
	name := strings.TrimSpace(r.FormValue("name"))
	domain := SanitizeDomain(r.FormValue("domain"))
	desc := strings.TrimSpace(r.FormValue("description"))
	theme := strings.TrimSpace(r.FormValue("theme"))
	isDefault := r.FormValue("is_default") == "true"
	historyDays := 60
	if hd, err := strconv.Atoi(r.FormValue("history_days")); err == nil && hd > 0 {
		historyDays = hd
	}
	logoURL := extractLogo(r, "")

	if name == "" {
		h.renderNewWithError(w, r, "Status page title is required.", name, slug, domain, desc, theme, logoURL, isDefault, historyDays)
		return
	}
	if slug == "" {
		h.renderNewWithError(w, r, "URL slug is required (e.g. 'status', 'git', 'app').", name, slug, domain, desc, theme, logoURL, isDefault, historyDays)
		return
	}

	// 1. Check if slug is already used
	existingSlug, err := h.statusPageRepo.GetBySlug(slug)
	if err == nil && existingSlug != nil {
		h.renderNewWithError(w, r, fmt.Sprintf("The URL slug '%s' is already in use by status page '%s'. Please choose a unique slug.", slug, existingSlug.Name), name, slug, domain, desc, theme, logoURL, isDefault, historyDays)
		return
	}

	// 2. Check if custom domain is already used
	if domain != "" {
		existingDomain, err := h.statusPageRepo.FindByDomain(domain)
		if err == nil && existingDomain != nil {
			h.renderNewWithError(w, r, fmt.Sprintf("The domain '%s' is already assigned to status page '%s'. Each status page must have a unique domain, or you can manage that status page directly.", domain, existingDomain.Name), name, slug, domain, desc, theme, logoURL, isDefault, historyDays)
			return
		}
	}

	page := &model.StatusPage{
		Slug:        slug,
		Name:        name,
		Domain:      domain,
		Description: desc,
		LogoURL:     logoURL,
		Theme:       theme,
		IsDefault:   isDefault,
		HistoryDays: historyDays,
	}

	if err := h.statusPageRepo.Create(page); err != nil {
		h.logger.Error("admin", "Failed to create status page: "+err.Error(), nil)
		h.renderNewWithError(w, r, "Failed to create status page: "+err.Error(), name, slug, domain, desc, theme, logoURL, isDefault, historyDays)
		return
	}

	h.logger.Info("admin", "Created status page tenant: "+page.Name, map[string]any{"id": page.ID, "domain": page.Domain})

	// Redirect straight into the new status page's workspace
	http.Redirect(w, r, "/admin/pages/"+page.ID, http.StatusFound)
}

func (h *AdminPagesHandler) ServeUpdateSettings(w http.ResponseWriter, r *http.Request, pageID string) {
	page, err := h.statusPageRepo.GetByID(pageID)
	if err != nil {
		http.Redirect(w, r, "/admin", http.StatusFound)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(strings.ToLower(r.FormValue("slug")))
	domain := SanitizeDomain(r.FormValue("domain"))
	desc := strings.TrimSpace(r.FormValue("description"))
	theme := strings.TrimSpace(r.FormValue("theme"))
	isDefault := r.FormValue("is_default") == "true"
	historyDays := 60
	if hd, err := strconv.Atoi(r.FormValue("history_days")); err == nil && hd > 0 {
		historyDays = hd
	}

	if name == "" || slug == "" {
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%s?err=Name+and+slug+are+required#tab-settings", pageID), http.StatusFound)
		return
	}

	// Check unique slug if changed
	if slug != page.Slug {
		existingSlug, err := h.statusPageRepo.GetBySlug(slug)
		if err == nil && existingSlug != nil && existingSlug.ID != pageID {
			http.Redirect(w, r, fmt.Sprintf("/admin/pages/%s?err=Slug+'%s'+is+already+used+by+'%s'#tab-settings", pageID, slug, existingSlug.Name), http.StatusFound)
			return
		}
	}

	// Check unique domain if changed
	if domain != "" && domain != page.Domain {
		existingDomain, err := h.statusPageRepo.FindByDomain(domain)
		if err == nil && existingDomain != nil && existingDomain.ID != pageID {
			http.Redirect(w, r, fmt.Sprintf("/admin/pages/%s?err=Domain+'%s'+is+already+assigned+to+'%s'#tab-settings", pageID, domain, existingDomain.Name), http.StatusFound)
			return
		}
	}

	page.Name = name
	page.Slug = slug
	page.Domain = domain
	page.Description = desc
	page.LogoURL = extractLogo(r, page.LogoURL)
	page.Theme = theme
	page.IsDefault = isDefault
	page.HistoryDays = historyDays

	if err := h.statusPageRepo.Update(page); err != nil {
		h.logger.Error("admin", "Failed to update page settings: "+err.Error(), nil)
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%s?err=%s#tab-settings", pageID, err.Error()), http.StatusFound)
		return
	}

	h.logger.Info("admin", "Updated status page settings: "+page.Name, nil)
	http.Redirect(w, r, fmt.Sprintf("/admin/pages/%s?msg=Settings+saved+successfully", pageID), http.StatusFound)
}

// ServeExportPage exports exclusively this status page's configuration and monitors
func (h *AdminPagesHandler) ServeExportPage(w http.ResponseWriter, r *http.Request, pageID string) {
	page, err := h.statusPageRepo.GetByID(pageID)
	if err != nil {
		http.Error(w, "status page not found", http.StatusNotFound)
		return
	}

	monitors, _ := h.monitorRepo.GetByStatusPage(page.ID)
	incidents, _ := h.incidentRepo.GetByPageID(page.ID)

	payload := model.PageBackupPayload{
		Version:    1,
		ExportedAt: time.Now().UTC(),
		StatusPage: *page,
		Monitors:   monitors,
		Incidents:  incidents,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="page-%s-backup-v1.json"`, page.Slug))

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}

// ServeImportPage restores endpoints and incidents specifically into this status page
func (h *AdminPagesHandler) ServeImportPage(w http.ResponseWriter, r *http.Request, pageID string) {
	page, err := h.statusPageRepo.GetByID(pageID)
	if err != nil {
		http.Redirect(w, r, "/admin", http.StatusFound)
		return
	}

	file, _, err := r.FormFile("backup_file")
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%s?err=Failed+to+read+uploaded+file", pageID), http.StatusFound)
		return
	}
	defer file.Close()

	bytes, err := io.ReadAll(file)
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%s?err=Failed+to+parse+file", pageID), http.StatusFound)
		return
	}

	var payload model.PageBackupPayload
	if err := json.Unmarshal(bytes, &payload); err != nil {
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%s?err=Invalid+JSON+backup+format", pageID), http.StatusFound)
		return
	}

	if payload.Version != 1 {
		http.Redirect(w, r, fmt.Sprintf("/admin/pages/%s?err=Unsupported+schema+version+%d", pageID, payload.Version), http.StatusFound)
		return
	}

	importedMonitors := 0
	for _, m := range payload.Monitors {
		mon := m
		mon.StatusPageID = page.ID
		if existing, err := h.monitorRepo.GetByID(mon.ID); err == nil && existing != nil {
			if existing.StatusPageID != page.ID {
				mon.ID = "" // ID belongs to another status page; generate fresh ID
			}
		}

		if mon.ID == "" {
			if err := h.monitorRepo.Create(&mon); err == nil {
				importedMonitors++
			}
		} else {
			if _, err := h.monitorRepo.GetByID(mon.ID); err != nil {
				if err := h.monitorRepo.Create(&mon); err == nil {
					importedMonitors++
				}
			} else {
				if err := h.monitorRepo.Update(&mon); err == nil {
					importedMonitors++
				}
			}
		}
	}

	importedIncidents := 0
	for _, inc := range payload.Incidents {
		incident := inc
		incident.StatusPageID = page.ID
		originalUpdates := inc.Updates

		if existing, err := h.incidentRepo.GetByID(incident.ID); err == nil && existing != nil {
			if existing.StatusPageID != page.ID {
				incident.ID = "" // ID belongs to another status page; generate fresh ID
			}
		}

		if incident.ID == "" {
			if err := h.incidentRepo.Create(&incident); err == nil {
				importedIncidents++
				for _, u := range originalUpdates {
					_ = h.incidentRepo.AddUpdate(incident.ID, u.Status, u.Message)
				}
			}
		} else {
			if _, err := h.incidentRepo.GetByID(incident.ID); err != nil {
				if err := h.incidentRepo.Create(&incident); err == nil {
					importedIncidents++
					for _, u := range originalUpdates {
						_ = h.incidentRepo.AddUpdate(incident.ID, u.Status, u.Message)
					}
				}
			}
		}
	}

	h.logger.Info("admin", fmt.Sprintf("Imported %d monitors and %d incidents into %s", importedMonitors, importedIncidents, page.Name), nil)
	http.Redirect(w, r, fmt.Sprintf("/admin/pages/%s?msg=Restored+%d+endpoints+and+%d+incidents", pageID, importedMonitors, importedIncidents), http.StatusFound)
}

func (h *AdminPagesHandler) ServeDelete(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	pageID := parts[2]

	_ = h.statusPageRepo.Delete(pageID)
	h.logger.Info("admin", "Deleted status page: "+pageID, nil)
	http.Redirect(w, r, "/admin", http.StatusFound)
}
