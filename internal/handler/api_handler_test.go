package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
)

func setupTestRouter(t *testing.T) (*Router, *db.APITokenRepo, *model.CreatedAPIToken, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_api.db")

	database, err := db.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	adminRepo := db.NewAdminRepo(database)
	_, _ = adminRepo.CreateAdmin("admin", "password123")

	tokenRepo := db.NewAPITokenRepo(database)
	createdToken, err := tokenRepo.Create("Test Integration Token")
	if err != nil {
		t.Fatalf("Create token failed: %v", err)
	}

	mRepo := db.NewMonitorRepo(database)
	cRepo := db.NewCheckRepo(database)
	spRepo := db.NewStatusPageRepo(database)
	incRepo := db.NewIncidentRepo(database)
	wRepo := db.NewWebhookRepo(database)
	lRepo := db.NewLogRepo(database)
	bRepo := db.NewBlacklistRepo(database)
	log := logger.New(lRepo)

	rt, err := NewRouter(
		adminRepo,
		bRepo,
		mRepo,
		cRepo,
		spRepo,
		incRepo,
		wRepo,
		lRepo,
		nil,
		nil,
		log,
	)
	if err != nil {
		t.Fatalf("NewRouter failed: %v", err)
	}

	cleanup := func() {
		_ = database.Close()
		_ = os.Remove(dbPath)
	}

	return rt, tokenRepo, createdToken, cleanup
}

func TestAPIHealth(t *testing.T) {
	rt, _, _, cleanup := setupTestRouter(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var res map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}
	if res["status"] != "ok" {
		t.Errorf("Expected status 'ok', got %v", res["status"])
	}
}

func TestAPIAuthentication(t *testing.T) {
	rt, _, createdToken, cleanup := setupTestRouter(t)
	defer cleanup()

	// 1. Missing authentication
	req := httptest.NewRequest(http.MethodGet, "/api/v1/summary", nil)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 Unauthorized, got %d", w.Code)
	}

	// 2. Invalid Bearer Token
	req = httptest.NewRequest(http.MethodGet, "/api/v1/summary", nil)
	req.Header.Set("Authorization", "Bearer invalid-token-123")
	w = httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 Unauthorized for bad token, got %d", w.Code)
	}

	// 3. Valid Bearer Token
	req = httptest.NewRequest(http.MethodGet, "/api/v1/summary", nil)
	req.Header.Set("Authorization", "Bearer "+createdToken.Token)
	w = httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK with Bearer token, got %d: %s", w.Code, w.Body.String())
	}

	// 4. Valid X-API-Key Header
	req = httptest.NewRequest(http.MethodGet, "/api/v1/summary", nil)
	req.Header.Set("X-API-Key", createdToken.Token)
	w = httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK with X-API-Key, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPIMonitorsCRUD(t *testing.T) {
	rt, _, createdToken, cleanup := setupTestRouter(t)
	defer cleanup()

	authHeader := "Bearer " + createdToken.Token

	// 1. Create a Monitor
	payload := map[string]any{
		"name":            "API Test HTTP",
		"type":            "http",
		"target":          "https://httpbin.org/status/200",
		"interval":        60,
		"timeout":         5,
		"expected_status": 200,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors", bytes.NewReader(body))
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}

	var created model.Monitor
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if created.ID == "" || created.Name != "API Test HTTP" {
		t.Errorf("Unexpected created monitor: %+v", created)
	}

	// 2. List Monitors
	req = httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("Authorization", authHeader)
	w = httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	var list []model.Monitor
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("Expected 1 monitor, got %d", len(list))
	}

	// 3. Get Monitor by ID
	req = httptest.NewRequest(http.MethodGet, "/api/v1/monitors/"+created.ID, nil)
	req.Header.Set("Authorization", authHeader)
	w = httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	// 4. Update Monitor
	updatePayload := map[string]any{
		"name": "API Test HTTP (Updated)",
	}
	updateBody, _ := json.Marshal(updatePayload)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/monitors/"+created.ID, bytes.NewReader(updateBody))
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on update, got %d: %s", w.Code, w.Body.String())
	}

	// 5. Delete Monitor
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/monitors/"+created.ID, nil)
	req.Header.Set("Authorization", authHeader)
	w = httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on delete, got %d: %s", w.Code, w.Body.String())
	}

	// Verify deleted
	req = httptest.NewRequest(http.MethodGet, "/api/v1/monitors/"+created.ID, nil)
	req.Header.Set("Authorization", authHeader)
	w = httptest.NewRecorder()
	rt.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404 Not Found after deletion, got %d", w.Code)
	}
}

func TestAPIStatusPagesCRUD(t *testing.T) {
	rt, _, createdToken, cleanup := setupTestRouter(t)
	defer cleanup()

	authHeader := "Bearer " + createdToken.Token

	// 1. Create a Status Page
	payload := map[string]any{
		"name":         "Main Public Status",
		"slug":         "main-status",
		"description":  "Core infrastructure health",
		"is_published": true,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/status-pages", bytes.NewReader(body))
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}

	var created model.StatusPage
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == "" || created.Slug != "main-status" {
		t.Errorf("Unexpected created status page: %+v", created)
	}

	// 2. List Status Pages
	req = httptest.NewRequest(http.MethodGet, "/api/v1/status-pages", nil)
	req.Header.Set("Authorization", authHeader)
	w = httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}
	var pages []model.StatusPage
	_ = json.Unmarshal(w.Body.Bytes(), &pages)
	// Default setup may create a default status page or 1
	if len(pages) < 1 {
		t.Fatalf("Expected at least 1 status page, got %d", len(pages))
	}
}

func TestAPITokensManagement(t *testing.T) {
	rt, _, createdToken, cleanup := setupTestRouter(t)
	defer cleanup()

	authHeader := "Bearer " + createdToken.Token

	// 1. Create a new token via API
	payload := map[string]any{
		"name": "CI Bot Token",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tokens", bytes.NewReader(body))
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}

	var resp model.CreatedAPIToken
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to decode token response: %v", err)
	}
	if !strings.HasPrefix(resp.Token, "upt_") {
		t.Errorf("Expected token to start with upt_, got %s", resp.Token)
	}

	// 2. Use the new token to make a request
	req = httptest.NewRequest(http.MethodGet, "/api/v1/summary", nil)
	req.Header.Set("Authorization", "Bearer "+resp.Token)
	w = httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK with new token, got %d", w.Code)
	}

	// 3. Delete the token
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/tokens/"+resp.ID, nil)
	req.Header.Set("Authorization", authHeader)
	w = httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on token delete, got %d", w.Code)
	}

	// 4. Verify deleted token is now unauthorized
	req = httptest.NewRequest(http.MethodGet, "/api/v1/summary", nil)
	req.Header.Set("Authorization", "Bearer "+resp.Token)
	w = httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 Unauthorized for deleted token, got %d", w.Code)
	}
}

func TestAdminSettingsTokenWebHandlers(t *testing.T) {
	rt, tokenRepo, _, cleanup := setupTestRouter(t)
	defer cleanup()

	// Direct call to AdminSettingsHandler methods
	h := rt.adminSettingsHandler

	// 1. Create Token via web form
	form := url.Values{}
	form.Set("name", "Web Generated Token")
	req := httptest.NewRequest(http.MethodPost, "/admin/settings/tokens", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeCreateToken(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("Expected 302 Found, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "new_token=") {
		t.Errorf("Expected redirect to contain new_token, got %s", loc)
	}

	tokens, err := tokenRepo.GetAll()
	if err != nil || len(tokens) < 2 {
		t.Fatalf("Expected at least 2 tokens, got %d (err: %v)", len(tokens), err)
	}

	// 2. Delete Token via web form
	targetID := tokens[0].ID
	delForm := url.Values{}
	delForm.Set("id", targetID)
	req = httptest.NewRequest(http.MethodPost, "/admin/settings/tokens/"+targetID+"/delete", strings.NewReader(delForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	h.ServeDeleteToken(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("Expected 302 Found on delete, got %d", w.Code)
	}
}
