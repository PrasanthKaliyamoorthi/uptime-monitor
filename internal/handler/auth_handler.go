package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
)

type AuthHandler struct {
	adminRepo      *db.AdminRepo
	blacklistRepo  *db.BlacklistRepo
	sessionManager *SessionManager
	renderer       *TemplateRenderer
	logger         *logger.Logger
	maxAttempts    int
}

func NewAuthHandler(
	aRepo *db.AdminRepo,
	bRepo *db.BlacklistRepo,
	sm *SessionManager,
	tr *TemplateRenderer,
	log *logger.Logger,
	maxAttempts int,
) *AuthHandler {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	return &AuthHandler{
		adminRepo:      aRepo,
		blacklistRepo:  bRepo,
		sessionManager: sm,
		renderer:       tr,
		logger:         log,
		maxAttempts:    maxAttempts,
	}
}

func (h *AuthHandler) ServeLogin(w http.ResponseWriter, r *http.Request) {
	clientIP := GetClientIP(r)

	// Check if the client IP is currently blacklisted
	if h.blacklistRepo != nil {
		if blocked, _ := h.blacklistRepo.IsBlacklisted(clientIP); blocked {
			w.WriteHeader(http.StatusForbidden)
			_ = h.renderer.Render(w, "login", map[string]any{
				"Title": "Admin Login",
				"Error": "Access Denied: Your IP (" + clientIP + ") has been blacklisted from the admin console due to excessive failed login attempts. To restore access, run: ./uptime-monitor -unblock-ip " + clientIP,
			})
			return
		}
	}

	if r.Method == http.MethodGet {
		// Check if already authenticated
		if c, err := r.Cookie("admin_session"); err == nil {
			if _, ok := h.sessionManager.Get(c.Value); ok {
				http.Redirect(w, r, "/admin", http.StatusFound)
				return
			}
		}

		_ = h.renderer.Render(w, "login", map[string]any{
			"Title": "Admin Login",
		})
		return
	}

	if r.Method == http.MethodPost {
		username := r.FormValue("username")
		password := r.FormValue("password")

		admin, err := h.adminRepo.Authenticate(username, password)
		if err != nil {
			h.logger.Warn("auth", "Failed login attempt for user: "+username+" from IP: "+clientIP, nil)

			errorMsg := "Invalid username or password."
			if h.blacklistRepo != nil {
				blocked, attempts, bErr := h.blacklistRepo.RecordFailedAttempt(clientIP, h.maxAttempts, "Excessive failed login attempts")
				if bErr == nil {
					if blocked {
						h.logger.Warn("auth", "IP "+clientIP+" has been blacklisted after "+strconv.Itoa(attempts)+" failed login attempts", nil)
						w.WriteHeader(http.StatusForbidden)
						_ = h.renderer.Render(w, "login", map[string]any{
							"Title": "Admin Login",
							"Error": "Access Denied: Your IP (" + clientIP + ") has been blacklisted from the admin console after " + strconv.Itoa(attempts) + " failed login attempts. To restore access, run: ./uptime-monitor -unblock-ip " + clientIP,
						})
						return
					}
					remaining := h.maxAttempts - attempts
					if remaining > 0 {
						errorMsg = fmt.Sprintf("Invalid username or password. (%d attempt(s) remaining before IP blacklist)", remaining)
					}
				}
			}

			_ = h.renderer.Render(w, "login", map[string]any{
				"Title": "Admin Login",
				"Error": errorMsg,
			})
			return
		}

		// Successful login: reset failed attempts
		if h.blacklistRepo != nil {
			_ = h.blacklistRepo.ResetAttempts(clientIP)
		}

		token := h.sessionManager.Create(admin.ID, admin.Username)
		http.SetCookie(w, &http.Cookie{
			Name:     "admin_session",
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   86400,
		})

		h.logger.Info("auth", "Admin logged in successfully: "+admin.Username+" from IP: "+clientIP, nil)
		http.Redirect(w, r, "/admin", http.StatusFound)
	}
}

func (h *AuthHandler) ServeLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("admin_session"); err == nil {
		h.sessionManager.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "admin_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}
