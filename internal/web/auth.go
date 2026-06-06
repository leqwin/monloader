package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/leqwin/monloader/internal/config"
	"golang.org/x/crypto/bcrypt"
)

type contextKey int

const sessionContextKey contextKey = 1

// Session is one logged-in browser session.
type Session struct {
	ID        string
	ExpiresAt time.Time
}

// SessionStore is an in-memory session set (copied from monbooru).
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

func NewSessionStore() *SessionStore { return &SessionStore{sessions: map[string]Session{}} }

func (s *SessionStore) New(lifetimeDays int) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(buf)
	s.mu.Lock()
	s.sessions[id] = Session{ID: id, ExpiresAt: time.Now().Add(time.Duration(lifetimeDays) * 24 * time.Hour)}
	s.mu.Unlock()
	return id, nil
}

func (s *SessionStore) Get(id string) (Session, bool) {
	s.mu.RLock()
	sess, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok || time.Now().After(sess.ExpiresAt) {
		return Session{}, false
	}
	return sess, true
}

func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// Clear drops every session, used after the password is removed so nobody is
// left locked out of the now-open instance.
func (s *SessionStore) Clear() {
	s.mu.Lock()
	s.sessions = map[string]Session{}
	s.mu.Unlock()
}

func sessionFromContext(ctx context.Context) string {
	v, _ := ctx.Value(sessionContextKey).(string)
	return v
}

func isHTMXRequest(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// SessionMiddleware injects a session id for CSRF and, when a UI password is
// configured, redirects unauthenticated requests to /login. The API and
// static assets bypass it.
func (s *Server) SessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/v1/") || p == "/health" || strings.HasPrefix(p, "/static/") || p == "/login" || p == "/custom.css" || p == "/custom.logo" {
			ctx := context.WithValue(r.Context(), sessionContextKey, "anon")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if !s.cfg.Current().Auth.EnablePassword {
			ctx := context.WithValue(r.Context(), sessionContextKey, "anon")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		sessID := sessionCookie(r)
		if _, ok := s.sessions.Get(sessID); !ok {
			if isHTMXRequest(r) {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), sessionContextKey, sessID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func sessionCookie(r *http.Request) string {
	c, err := r.Cookie("monloader_session")
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Current().Auth.EnablePassword {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, "login", map[string]any{
		"Title":        "Login - " + s.booruName(),
		"CSRFToken":    s.csrfToken("anon"),
		"Conn":         "checking",
		"BooruName":    s.booruName(),
		"BooruFavicon": s.booruFaviconURL(),
	})
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Current().Auth.EnablePassword {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	password := r.FormValue("password")
	if bcrypt.CompareHashAndPassword([]byte(s.cfg.Current().Auth.PasswordHash), []byte(password)) != nil {
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, "login", map[string]any{
			"Title":        "Login - " + s.booruName(),
			"CSRFToken":    s.csrfToken("anon"),
			"Error":        "incorrect password",
			"Conn":         "checking",
			"BooruName":    s.booruName(),
			"BooruFavicon": s.booruFaviconURL(),
		})
		return
	}
	id, err := s.sessions.New(s.cfg.Current().Auth.SessionLifetimeDays)
	if err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "monloader_session",
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   s.cfg.Current().Auth.SessionLifetimeDays * 24 * 3600,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logoutPost(w http.ResponseWriter, r *http.Request) {
	s.sessions.Delete(sessionCookie(r))
	http.SetCookie(w, &http.Cookie{Name: "monloader_session", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// settingsPasswordPost sets or changes the optional UI password from the
// authentication settings section. Setting a password also turns auth on. An
// existing password must be confirmed before it changes.
func (s *Server) settingsPasswordPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		flashFragment(w, "err", "bad form")
		return
	}
	newPass := r.FormValue("new_password")
	if newPass == "" {
		flashFragment(w, "err", "new password required")
		return
	}
	if s.cfg.Current().Auth.EnablePassword && s.cfg.Current().Auth.PasswordHash != "" {
		if bcrypt.CompareHashAndPassword([]byte(s.cfg.Current().Auth.PasswordHash), []byte(r.FormValue("current_password"))) != nil {
			flashFragment(w, "err", "current password is incorrect")
			return
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
	if err != nil {
		flashFragment(w, "err", "error hashing password")
		return
	}
	if err := s.updateConfig(func(c *config.Config) error {
		c.Auth.PasswordHash = string(hash)
		c.Auth.EnablePassword = true
		return nil
	}); err != nil {
		flashFragment(w, "err", "could not save: "+err.Error())
		return
	}
	flashFragment(w, "ok", "password updated")
	s.renderAuthPasswordOOB(w, r)
}

// settingsRemovePasswordPost disables the UI password. The current password is
// required whenever a hash is set, even if the TOML flag was flipped off, so
// the disable path cannot be bypassed.
func (s *Server) settingsRemovePasswordPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		flashFragment(w, "err", "bad form")
		return
	}
	if s.cfg.Current().Auth.PasswordHash != "" {
		if bcrypt.CompareHashAndPassword([]byte(s.cfg.Current().Auth.PasswordHash), []byte(r.FormValue("current_password"))) != nil {
			flashFragment(w, "err", "current password is incorrect")
			return
		}
	}
	if err := s.updateConfig(func(c *config.Config) error {
		c.Auth.EnablePassword = false
		c.Auth.PasswordHash = ""
		return nil
	}); err != nil {
		flashFragment(w, "err", "could not save: "+err.Error())
		return
	}
	s.sessions.Clear()
	flashFragment(w, "ok", "password removed; authentication is now disabled")
	s.renderAuthPasswordOOB(w, r)
}

// settingsTokenPost generates (or regenerates) the downloader's own API bearer
// token. The plaintext key is shown once in the response and never echoed by
// the settings page again.
func (s *Server) settingsTokenPost(w http.ResponseWriter, r *http.Request) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		flashFragment(w, "err", "failed to generate key")
		return
	}
	token := fmt.Sprintf("%x", buf)
	if err := s.updateConfig(func(c *config.Config) error {
		c.Auth.APIToken = token
		return nil
	}); err != nil {
		flashFragment(w, "err", "could not save: "+err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, "token_flash", map[string]any{"Token": token})
}

// renderAuthPasswordOOB re-renders the password sub-section out of band so its
// enabled/disabled line updates after a change without a full page reload.
func (s *Server) renderAuthPasswordOOB(w http.ResponseWriter, r *http.Request) {
	s.render(w, "auth_password_section", map[string]any{
		"AuthEnabled": s.cfg.Current().Auth.EnablePassword,
		"CSRFToken":   s.csrfToken(sessionFromContext(r.Context())),
		"OOB":         true,
	})
}
