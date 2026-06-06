package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"chowkidar/internal/config"
)

type sessionEntry struct {
	username  string
	expiresAt time.Time
}

const sessionCookieName = "chowkidar_session"

func (a *app) createSession(username string, remember bool) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	expiry := time.Now().Add(24 * time.Hour)
	if remember {
		expiry = time.Now().Add(30 * 24 * time.Hour)
	}
	a.sessionMu.Lock()
	a.sessions[token] = sessionEntry{username: username, expiresAt: expiry}
	a.sessionMu.Unlock()
	return token
}

func (a *app) validateSession(r *http.Request) (string, bool) {
	if a.authCfg == nil || !a.authCfg.Enabled {
		return "", true // auth disabled, always valid
	}
	// Setup required (no password set yet) — allow through so the setup API is reachable
	if a.authCfg.PasswordHash == "" {
		return "", true
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	entry, ok := a.sessions[cookie.Value]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(a.sessions, cookie.Value)
		return "", false
	}
	return entry.username, true
}

func (a *app) deleteSession(r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return
	}
	a.sessionMu.Lock()
	delete(a.sessions, cookie.Value)
	a.sessionMu.Unlock()
}

func (a *app) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := a.validateSession(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

// ── Auth handlers ─────────────────────────────────────────────────────────────

func (a *app) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// Seed the client timezone from the tz query param sent on every page load.
	a.recordClientTimezone(r.URL.Query().Get("tz"))
	// setup_required = auth is enabled but no password has been set yet (first boot)
	setupRequired := a.authCfg == nil || (a.authCfg.Enabled && a.authCfg.PasswordHash == "")
	enabled := a.authCfg != nil && a.authCfg.Enabled
	username, loggedIn := a.validateSession(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":       enabled,
		"loggedIn":      loggedIn,
		"username":      username,
		"setupRequired": setupRequired,
	})
}

func (a *app) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request"})
		return
	}
	if a.authCfg == nil || !a.authCfg.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "auth disabled"})
		return
	}
	if req.Username != a.authCfg.Username {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid credentials"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(a.authCfg.PasswordHash), []byte(req.Password)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid credentials"})
		return
	}
	token := a.createSession(req.Username, req.Remember)
	maxAge := 86400
	if req.Remember {
		maxAge = 86400 * 30
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": req.Username})
}

func (a *app) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	a.deleteSession(r)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *app) handleAuthChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
		Username        string `json:"username"` // for first-time setup
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request"})
		return
	}
	if req.NewPassword == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "new password required"})
		return
	}
	// First-time setup: no password set yet (PasswordHash empty = setup required)
	if a.authCfg == nil || !a.authCfg.Enabled || a.authCfg.PasswordHash == "" {
		if req.Username == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "username required for first-time setup"})
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to hash password"})
			return
		}
		a.authCfg = &config.AuthConfig{Enabled: true, Username: req.Username, PasswordHash: string(hash)}
		if err := config.SaveAuthConfig(a.cfg.ConfigDir, a.authCfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to save auth config"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	// Change existing password
	if err := bcrypt.CompareHashAndPassword([]byte(a.authCfg.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "current password incorrect"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to hash password"})
		return
	}
	a.authCfg.PasswordHash = string(hash)
	if err := config.SaveAuthConfig(a.cfg.ConfigDir, a.authCfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to save auth config"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *app) handleAuthDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.authCfg != nil {
		a.authCfg.Enabled = false
		if err := config.SaveAuthConfig(a.cfg.ConfigDir, a.authCfg); err != nil {
			logWarnf("auth: disable — failed to persist auth config: %v", err)
		}
	}
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	a.sessions = make(map[string]sessionEntry)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
