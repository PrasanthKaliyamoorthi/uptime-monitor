package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
)

type AdminBackupHandler struct {
	monitorRepo    *db.MonitorRepo
	statusPageRepo *db.StatusPageRepo
	webhookRepo    *db.WebhookRepo
	renderer       *TemplateRenderer
	logger         *logger.Logger
}

func NewAdminBackupHandler(
	mRepo *db.MonitorRepo,
	spRepo *db.StatusPageRepo,
	wRepo *db.WebhookRepo,
	tr *TemplateRenderer,
	log *logger.Logger,
) *AdminBackupHandler {
	return &AdminBackupHandler{
		monitorRepo:    mRepo,
		statusPageRepo: spRepo,
		webhookRepo:    wRepo,
		renderer:       tr,
		logger:         log,
	}
}

func (h *AdminBackupHandler) ServePage(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		"Title":     "Backup & Versioned Migration",
		"ActiveTab": "backup",
	}
	_ = h.renderer.Render(w, "admin_backup", data)
}

func (h *AdminBackupHandler) ServeExport(w http.ResponseWriter, r *http.Request) {
	monitors, _ := h.monitorRepo.GetAll()
	pages, _ := h.statusPageRepo.GetAll()
	webhooks, _ := h.webhookRepo.GetAll()

	var mappings []model.StatusPageMonitor
	for _, p := range pages {
		m, _ := h.statusPageRepo.GetMonitorsForPage(p.ID)
		mappings = append(mappings, m...)
	}

	payload := model.BackupPayload{
		Version:            1,
		ExportedAt:         time.Now().UTC(),
		Monitors:           monitors,
		StatusPages:        pages,
		StatusPageMonitors: mappings,
		AlertWebhooks:      webhooks,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="uptime-backup-v1.json"`)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}

func (h *AdminBackupHandler) ServeImport(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("backup_file")
	if err != nil {
		_ = h.renderer.Render(w, "admin_backup", map[string]any{
			"Title":     "Backup & Versioned Migration",
			"ActiveTab": "backup",
			"Error":     "Failed to read uploaded backup file: " + err.Error(),
		})
		return
	}
	defer file.Close()

	bytes, err := io.ReadAll(file)
	if err != nil {
		_ = h.renderer.Render(w, "admin_backup", map[string]any{
			"Title":     "Backup & Versioned Migration",
			"ActiveTab": "backup",
			"Error":     "Failed to parse file: " + err.Error(),
		})
		return
	}

	var payload model.BackupPayload
	if err := json.Unmarshal(bytes, &payload); err != nil {
		_ = h.renderer.Render(w, "admin_backup", map[string]any{
			"Title":     "Backup & Versioned Migration",
			"ActiveTab": "backup",
			"Error":     "Invalid JSON backup format: " + err.Error(),
		})
		return
	}

	if payload.Version != 1 {
		_ = h.renderer.Render(w, "admin_backup", map[string]any{
			"Title":     "Backup & Versioned Migration",
			"ActiveTab": "backup",
			"Error":     fmt.Sprintf("Unsupported backup schema version %d (supported: 1)", payload.Version),
		})
		return
	}

	// Restore monitors
	importedMonitors := 0
	for _, m := range payload.Monitors {
		mon := m
		if _, err := h.monitorRepo.GetByID(mon.ID); err != nil {
			if err := h.monitorRepo.Create(&mon); err == nil {
				importedMonitors++
			}
		}
	}

	// Restore status pages
	importedPages := 0
	for _, p := range payload.StatusPages {
		page := p
		if _, err := h.statusPageRepo.GetByID(page.ID); err != nil {
			if err := h.statusPageRepo.Create(&page); err == nil {
				importedPages++
			}
		}
	}

	// Restore webhooks
	importedWebhooks := 0
	for _, w := range payload.AlertWebhooks {
		webhook := w
		if _, err := h.webhookRepo.GetByID(webhook.ID); err != nil {
			if err := h.webhookRepo.Create(&webhook); err == nil {
				importedWebhooks++
			}
		}
	}

	h.logger.Info("admin", "Configuration imported successfully", map[string]any{
		"monitors": importedMonitors,
		"pages":    importedPages,
		"webhooks": importedWebhooks,
	})

	_ = h.renderer.Render(w, "admin_backup", map[string]any{
		"Title":     "Backup & Versioned Migration",
		"ActiveTab": "backup",
		"Message":   fmt.Sprintf("Successfully restored %d monitors, %d status pages, and %d webhooks!", importedMonitors, importedPages, importedWebhooks),
	})
}
