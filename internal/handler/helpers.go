package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
	"uptime-monitor/web"
)

// SessionManager manages authenticated admin sessions.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]model.Session
	repo     *db.SessionRepo
}

func NewSessionManager(repo ...*db.SessionRepo) *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]model.Session),
	}
	if len(repo) > 0 && repo[0] != nil {
		sm.repo = repo[0]
	}
	// Background cleanup of expired sessions
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			sm.cleanup()
		}
	}()
	return sm
}

func (sm *SessionManager) Create(adminID, username string) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	expiresAt := time.Now().UTC().Add(24 * time.Hour)

	sm.mu.Lock()
	sm.sessions[token] = model.Session{
		Token:     token,
		AdminID:   adminID,
		Username:  username,
		ExpiresAt: expiresAt,
	}
	sm.mu.Unlock()

	if sm.repo != nil {
		_ = sm.repo.Create(token, adminID, username, expiresAt)
	}
	return token
}

func (sm *SessionManager) Get(token string) (model.Session, bool) {
	sm.mu.RLock()
	sess, ok := sm.sessions[token]
	sm.mu.RUnlock()

	if ok && time.Now().UTC().Before(sess.ExpiresAt) {
		return sess, true
	}

	if sm.repo != nil {
		dbSess, err := sm.repo.Get(token)
		if err == nil && dbSess != nil && time.Now().UTC().Before(dbSess.ExpiresAt) {
			sm.mu.Lock()
			sm.sessions[token] = *dbSess
			sm.mu.Unlock()
			return *dbSess, true
		}
	}

	return model.Session{}, false
}

func (sm *SessionManager) Delete(token string) {
	sm.mu.Lock()
	delete(sm.sessions, token)
	sm.mu.Unlock()

	if sm.repo != nil {
		_ = sm.repo.Delete(token)
	}
}

func (sm *SessionManager) cleanup() {
	sm.mu.Lock()
	now := time.Now().UTC()
	for k, v := range sm.sessions {
		if now.After(v.ExpiresAt) {
			delete(sm.sessions, k)
		}
	}
	sm.mu.Unlock()

	if sm.repo != nil {
		_ = sm.repo.DeleteExpired()
	}
}

// ExtractHost strips the port number from a Host string if present
func ExtractHost(host string) string {
	clean := strings.ToLower(strings.TrimSpace(host))
	if idx := strings.Index(clean, ":"); idx != -1 {
		return clean[:idx]
	}
	return clean
}

// ExtractRequestHost retrieves the normalized host from X-Forwarded-Host or r.Host
func ExtractRequestHost(r *http.Request) string {
	if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
		parts := strings.Split(xfh, ",")
		return ExtractHost(strings.TrimSpace(parts[0]))
	}
	return ExtractHost(r.Host)
}

// GetClientIP extracts the client IP address from proxy headers or RemoteAddr
func GetClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		ip := strings.TrimSpace(parts[0])
		if ip != "" {
			return cleanHostIP(ip)
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		ip := strings.TrimSpace(xri)
		if ip != "" {
			return cleanHostIP(ip)
		}
	}
	return cleanHostIP(r.RemoteAddr)
}

func cleanHostIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	raw = strings.TrimPrefix(raw, "[")
	raw = strings.TrimSuffix(raw, "]")
	if raw == "::1" {
		return "127.0.0.1"
	}
	return raw
}

// ParseDateTime flexibly parses datetime-local strings and standard formats
func ParseDateTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("empty datetime string")
	}
	layouts := []string{
		"2006-01-02T15:04",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		time.RFC3339,
		time.RFC3339Nano,
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse datetime: %q", s)
}

// TemplateRenderer compiles and executes templates from embedded assets.
type TemplateRenderer struct {
	templates map[string]*template.Template
}

func NewTemplateRenderer() (*TemplateRenderer, error) {
	funcMap := template.FuncMap{
		"eq": func(a, b any) bool { return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b) },
		"safeURL": func(s string) template.URL { return template.URL(s) },
		"sanitizeURL": logger.SanitizeURL,
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
		"toJSON": func(v any) template.JS {
			b, _ := json.Marshal(v)
			return template.JS(b)
		},
		"formatDateTimeLocal": func(t any) string {
			switch v := t.(type) {
			case time.Time:
				if v.IsZero() {
					return ""
				}
				return v.Format("2006-01-02T15:04")
			case *time.Time:
				if v == nil || v.IsZero() {
					return ""
				}
				return v.Format("2006-01-02T15:04")
			default:
				return ""
			}
		},
		"gt": func(a, b any) bool {
			toF := func(v any) (float64, bool) {
				switch n := v.(type) {
				case int:
					return float64(n), true
				case int8:
					return float64(n), true
				case int16:
					return float64(n), true
				case int32:
					return float64(n), true
				case int64:
					return float64(n), true
				case uint:
					return float64(n), true
				case uint8:
					return float64(n), true
				case uint16:
					return float64(n), true
				case uint32:
					return float64(n), true
				case uint64:
					return float64(n), true
				case float32:
					return float64(n), true
				case float64:
					return n, true
				default:
					return 0, false
				}
			}
			fA, okA := toF(a)
			fB, okB := toF(b)
			return okA && okB && fA > fB
		},
	}

	pages := map[string][]string{
		"setup": {
			"templates/layout.html",
			"templates/setup.html",
		},
		"login": {
			"templates/layout.html",
			"templates/login.html",
		},
		"status_page": {
			"templates/layout.html",
			"templates/status_page.html",
			"templates/partials/day_bar.html",
			"templates/partials/incident_banner.html",
		},
		"admin_dashboard": {
			"templates/admin/layout.html",
			"templates/admin/dashboard.html",
		},
		"admin_page_workspace": {
			"templates/admin/layout.html",
			"templates/admin/page_workspace.html",
		},
		"admin_new_page": {
			"templates/admin/layout.html",
			"templates/admin/new_page.html",
		},
		"admin_monitors": {
			"templates/admin/layout.html",
			"templates/admin/monitors.html",
			"templates/partials/day_bar.html",
		},
		"admin_pages": {
			"templates/admin/layout.html",
			"templates/admin/pages.html",
		},
		"admin_incidents": {
			"templates/admin/layout.html",
			"templates/admin/incidents.html",
		},
		"admin_logs": {
			"templates/admin/layout.html",
			"templates/admin/logs.html",
		},
		"admin_webhooks": {
			"templates/admin/layout.html",
			"templates/admin/webhooks.html",
		},
		"admin_backup": {
			"templates/admin/layout.html",
			"templates/admin/backup.html",
		},
		"admin_settings": {
			"templates/admin/layout.html",
			"templates/admin/settings.html",
		},
	}

	compiled := make(map[string]*template.Template)
	for name, files := range pages {
		tmpl, err := template.New("").Funcs(funcMap).ParseFS(web.Files, files...)
		if err != nil {
			return nil, err
		}
		compiled[name] = tmpl
	}

	return &TemplateRenderer{templates: compiled}, nil
}

func (tr *TemplateRenderer) Render(w http.ResponseWriter, name string, data any) error {
	tmpl, ok := tr.templates[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return nil
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		fmt.Printf("[ERROR] template execution failed for %s: %v\n", name, err)
		return err
	}
	return nil
}
