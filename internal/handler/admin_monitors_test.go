package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"uptime-monitor/internal/checker"
	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
)

func TestAdminMonitorsLifecycleAndToggle(t *testing.T) {
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer database.Close()

	mRepo := db.NewMonitorRepo(database)
	cRepo := db.NewCheckRepo(database)
	spRepo := db.NewStatusPageRepo(database)
	log := logger.New(db.NewLogRepo(database))
	coord := checker.NewCoordinator(mRepo, cRepo, nil, log)
	sched := checker.NewScheduler(coord, mRepo, log, 10)
	tr, err := NewTemplateRenderer()
	if err != nil {
		t.Fatalf("failed to init template renderer: %v", err)
	}

	h := NewAdminMonitorsHandler(mRepo, cRepo, spRepo, sched, tr, log)

	// 1. Create a monitor
	mon := &model.Monitor{
		Name:          "Initial API",
		Protocol:      "https",
		Target:        "https://api.initial.com",
		Method:        "GET",
		IntervalSec:   60,
		TimeoutSec:    10,
		ExpectedCode:  200,
		RetryCount:    2,
		IsActive:      true,
		CurrentStatus: "up",
	}
	if err := mRepo.Create(mon); err != nil {
		t.Fatalf("failed to create monitor: %v", err)
	}

	// Add check logs so heartbeats and uptime are computed
	_ = cRepo.LogCheck(&model.CheckLog{MonitorID: mon.ID, StatusCode: 200, LatencyMs: 25, IsSuccess: true, CheckedAt: time.Now().UTC()})

	// 2. Test ServeList
	reqList := httptest.NewRequest(http.MethodGet, "/admin/monitors", nil)
	rrList := httptest.NewRecorder()
	h.ServeList(rrList, reqList)
	if rrList.Code != http.StatusOK {
		t.Errorf("expected 200 OK from ServeList, got %d", rrList.Code)
	}
	listBody := rrList.Body.String()
	if !strings.Contains(listBody, "Initial API") || !strings.Contains(listBody, "https://api.initial.com") {
		t.Errorf("expected ServeList to contain monitor details, got: %s", listBody)
	}

	// 3. Test ServeGet (JSON)
	reqGet := httptest.NewRequest(http.MethodGet, "/admin/monitors/"+mon.ID, nil)
	rrGet := httptest.NewRecorder()
	h.ServeGet(rrGet, reqGet)
	if rrGet.Code != http.StatusOK {
		t.Errorf("expected 200 OK from ServeGet, got %d", rrGet.Code)
	}
	var fetched model.Monitor
	if err := json.Unmarshal(rrGet.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("failed to parse JSON from ServeGet: %v", err)
	}
	if fetched.Name != "Initial API" {
		t.Errorf("expected Name 'Initial API', got %q", fetched.Name)
	}

	// 4. Test ServeToggle (Pause monitor)
	reqToggle := httptest.NewRequest(http.MethodPost, "/admin/monitors/"+mon.ID+"/toggle", nil)
	rrToggle := httptest.NewRecorder()
	h.ServeToggle(rrToggle, reqToggle)
	if rrToggle.Code != http.StatusFound {
		t.Errorf("expected 302 redirect after toggle, got %d", rrToggle.Code)
	}
	pausedMon, _ := mRepo.GetByID(mon.ID)
	if pausedMon.IsActive {
		t.Errorf("expected monitor to be paused (IsActive=false), got true")
	}

	// Toggle again (Resume monitor)
	rrResume := httptest.NewRecorder()
	h.ServeToggle(rrResume, reqToggle)
	resumedMon, _ := mRepo.GetByID(mon.ID)
	if !resumedMon.IsActive {
		t.Errorf("expected monitor to be resumed (IsActive=true), got false")
	}

	// 5. Test ServeUpdate (Edit monitor)
	form := url.Values{
		"name":            {"Updated Service API"},
		"protocol":        {"https"},
		"target":          {"https://api.updated.com/v2"},
		"method":          {"POST"},
		"expected_code":   {"201"},
		"interval_sec":    {"30"},
		"timeout_sec":     {"5"},
		"retry_count":     {"3"},
		"skip_tls_verify": {"true"},
		"keyword":         {"CREATED"},
	}
	reqUpdate := httptest.NewRequest(http.MethodPost, "/admin/monitors/"+mon.ID+"/edit", strings.NewReader(form.Encode()))
	reqUpdate.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrUpdate := httptest.NewRecorder()
	h.ServeUpdate(rrUpdate, reqUpdate)
	if rrUpdate.Code != http.StatusFound {
		t.Errorf("expected 302 redirect after edit, got %d", rrUpdate.Code)
	}

	updatedMon, _ := mRepo.GetByID(mon.ID)
	if updatedMon.Name != "Updated Service API" || updatedMon.Target != "https://api.updated.com/v2" || updatedMon.IntervalSec != 30 {
		t.Errorf("unexpected updated monitor: %+v", updatedMon)
	}

	// 6. Test ServeDiagnostics
	reqDiag := httptest.NewRequest(http.MethodGet, "/admin/monitors/"+mon.ID+"/diagnostics", nil)
	rrDiag := httptest.NewRecorder()
	h.ServeDiagnostics(rrDiag, reqDiag)
	if rrDiag.Code != http.StatusOK {
		t.Errorf("expected 200 OK from ServeDiagnostics, got %d", rrDiag.Code)
	}
	if !strings.Contains(rrDiag.Body.String(), "Target Diagnostic Inspection") {
		t.Errorf("expected diagnostics HTML in response")
	}
}
