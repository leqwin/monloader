package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/leqwin/monloader/internal/api"
	"github.com/leqwin/monloader/internal/config"
	"github.com/leqwin/monloader/internal/gdl"
	"github.com/leqwin/monloader/internal/logx"
	"github.com/leqwin/monloader/internal/mapping"
	"github.com/leqwin/monloader/internal/monbooru"
	"github.com/leqwin/monloader/internal/queue"
	"github.com/leqwin/monloader/internal/sitestate"
	webFS "github.com/leqwin/monloader/web"
)

// Version and RepoURL are set at build time via -ldflags (see the Makefile).
var (
	Version = "dev"
	RepoURL = "https://github.com/leqwin/monloader"
)

// Server renders the three-screen htmx UI and mounts the JSON API on the same
// mux.
type Server struct {
	cfg        *config.Provider
	configPath string
	cfgMu      sync.Mutex

	queue      *queue.Queue
	client     *monbooru.Client
	runner     gdl.Runner
	mapper     *mapping.Mapper
	extractors []gdl.Extractor
	gdlVersion string
	siteState  *sitestate.Tracker

	sessions   *SessionStore
	csrfSecret []byte
	tmpl       *template.Template
	staticFS   fs.FS
}

// NewServer wires the UI server. extractors is the cached --list-extractors
// result and gdlVersion the bundled gallery-dl version (both feed the API and
// settings); siteState is the shared "last reached" tracker the settings sites
// table reads and the test probe writes (the pipeline writes it on a fetch).
func NewServer(cfg *config.Provider, configPath string, q *queue.Queue, client *monbooru.Client, runner gdl.Runner, mapper *mapping.Mapper, extractors []gdl.Extractor, gdlVersion string, siteState *sitestate.Tracker) (*Server, error) {
	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(webFS.FS, "templates/*.html", "templates/partials/*.html")
	if err != nil {
		return nil, err
	}
	staticFS, err := fs.Sub(webFS.FS, "static")
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:        cfg,
		configPath: configPath,
		queue:      q,
		client:     client,
		runner:     runner,
		mapper:     mapper,
		extractors: extractors,
		gdlVersion: gdlVersion,
		siteState:  siteState,
		sessions:   NewSessionStore(),
		csrfSecret: mustRandBytes(32),
		tmpl:       tmpl,
		staticFS:   staticFS,
	}, nil
}

// Handler returns the root HTTP handler: web routes plus the mounted API, with
// logging, session, and CSRF middleware applied (outermost first).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(s.staticFS))))
	mux.HandleFunc("GET /custom.css", s.serveCustomCSS)
	mux.HandleFunc("GET /custom.logo", s.serveCustomLogo)
	// Browsers request /favicon.ico unconditionally; redirect to the asset.
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, s.booruFaviconURL(), http.StatusMovedPermanently)
	})

	mux.HandleFunc("GET /{$}", s.addScreen)
	mux.HandleFunc("POST /{$}", s.enqueueForm)
	mux.HandleFunc("GET /queue", s.queueScreen)
	mux.HandleFunc("GET /internal/queue-rows", s.queueRows)
	mux.HandleFunc("POST /queue/{id}/retry", s.retryJob)
	mux.HandleFunc("POST /queue/{id}/continue", s.continueJob)
	mux.HandleFunc("POST /queue/clear", s.clearQueue)
	mux.HandleFunc("DELETE /queue/{id}", s.deleteJob)
	mux.HandleFunc("GET /internal/monbooru-status", s.monbooruStatus)

	mux.HandleFunc("GET /settings", s.settingsScreen)
	mux.HandleFunc("POST /settings/monbooru", s.saveMonbooru)
	mux.HandleFunc("POST /settings/monbooru/test", s.testMonbooru)
	mux.HandleFunc("POST /settings/downloader", s.saveDownloader)
	mux.HandleFunc("POST /settings/sites", s.saveSite)
	mux.HandleFunc("POST /settings/sites/{name}/reset", s.resetSite)
	mux.HandleFunc("POST /settings/sites/{name}/test", s.testSite)
	mux.HandleFunc("POST /settings/raw", s.saveRaw)

	mux.HandleFunc("POST /settings/auth/password", s.settingsPasswordPost)
	mux.HandleFunc("POST /settings/auth/remove-password", s.settingsRemovePasswordPost)
	mux.HandleFunc("POST /settings/auth/token", s.settingsTokenPost)

	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginPost)
	mux.HandleFunc("POST /logout", s.logoutPost)

	api.New(s.queue, s.runner, s.mapper, s.cfg, s.extractors, Version, s.gdlVersion).Mount(mux)

	var h http.Handler = mux
	h = s.CSRFMiddleware(h)
	h = s.SessionMiddleware(h)
	h = loggingMiddleware(h)
	return h
}

// templateFuncs are the helpers the templates use. dict builds an inline map
// so a partial can be handed a small sub-context (e.g. the auth password
// block); humanBytes formats a byte count for the stats section.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"dict": func(pairs ...any) map[string]any {
			m := make(map[string]any, len(pairs)/2)
			for i := 0; i+1 < len(pairs); i += 2 {
				key, _ := pairs[i].(string)
				m[key] = pairs[i+1]
			}
			return m
		},
		"humanBytes": humanBytes,
		"humanSince": humanSince,
		"stampUTC":   stampUTC,
		"join":       strings.Join,
	}
}

// humanSince formats how long ago t was, compactly, for the narrow sites
// state column: "just now", "5m ago", "2h ago", "3d ago". A zero time renders
// empty.
func humanSince(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours())/24)
	}
}

// stampUTC is the absolute form shown on hover beside the relative time.
func stampUTC(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

// humanBytes formats a byte count with binary units (KiB, MiB, ...).
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/queue-rows" || r.URL.Path == "/internal/monbooru-status" || r.URL.Path == "/health" {
			logx.Debugf("%s %s", r.Method, r.URL.Path)
		} else {
			logx.Infof("%s %s", r.Method, r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}

// base returns the template data common to every page.
func (s *Server) base(r *http.Request, nav, title string) map[string]any {
	return map[string]any{
		"Title":            title,
		"ActiveNav":        nav,
		"CSRFToken":        s.csrfToken(sessionFromContext(r.Context())),
		"AuthEnabled":      s.cfg.Current().Auth.EnablePassword,
		"Version":          Version,
		"GalleryDLVersion": s.gdlVersion,
		"RepoURL":          RepoURL,
		"CustomCSS":        s.cfg.Current().Server.CustomCSS != "",
		"BooruName":        s.booruName(),
		"BooruLogo":        s.booruLogoURL(),
		"BooruFavicon":     s.booruFaviconURL(),
		"Conn":             "checking",
		// Synchronously known reachability: an unset API URL is definitively
		// unreachable, so the add/queue banner and the blocked submit render
		// server-side at once. A configured-but-down instance is left to the
		// async connectivity light to surface.
		"MonbooruConfigured": s.monbooruConfigured(),
	}
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		logx.Errorf("template %q: %v", name, err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	_, _ = buf.WriteTo(w)
}

func (s *Server) serveCustomCSS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Current().Server.CustomCSS == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, s.cfg.Current().Server.CustomCSS)
}

// serveCustomLogo serves the operator-supplied logo/favicon pointed at by
// server.logo. Same shape as serveCustomCSS - an empty config 404s so the
// layout falls back to the bundled logo and favicon.
func (s *Server) serveCustomLogo(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Current().Server.BooruLogo == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, s.cfg.Current().Server.BooruLogo)
}

// booruName resolves server.name with a "monloader" fallback so every
// title and wordmark callsite reads one source of truth.
func (s *Server) booruName() string {
	if name := s.cfg.Current().Server.BooruName; name != "" {
		return name
	}
	return "monloader"
}

// booruLogoURL points the topbar logo at /custom.logo when an override is
// configured, the bundled logo otherwise. A configured server.logo backs
// both surfaces; only the unset fallback differs from booruFaviconURL.
func (s *Server) booruLogoURL() string {
	if s.cfg.Current().Server.BooruLogo != "" {
		return "/custom.logo"
	}
	return "/static/logo.png"
}

// booruFaviconURL points the favicon link at /custom.logo when an override
// is configured, the bundled favicon otherwise.
func (s *Server) booruFaviconURL() string {
	if s.cfg.Current().Server.BooruLogo != "" {
		return "/custom.logo"
	}
	return "/static/favicon.png"
}

// updateConfig applies fn to a fresh copy of the running config and, once it is
// persisted, publishes that copy through the provider. The current snapshot is
// never mutated in place, so the worker goroutine and request handlers reading
// the config never observe a half-updated struct. Persistence targets the
// on-disk file layer (reloaded without MONLOADER_* overrides) so an ephemeral
// env value, like a token from the container env, is never baked into
// monloader.toml. fn must be idempotent: it runs against both the runtime copy
// and the file layer.
func (s *Server) updateConfig(fn func(*config.Config) error) error {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	next := s.cfg.Current().Clone()
	if err := fn(next); err != nil {
		return err
	}
	persist, err := config.LoadFromFile(s.configPath)
	if err != nil {
		return err
	}
	if err := fn(persist); err != nil {
		return err
	}
	if err := config.Save(persist, s.configPath); err != nil {
		return err
	}
	s.cfg.Store(next)
	return nil
}

// rewriteGDLConfig regenerates the managed gallery-dl config after a settings
// change that affects it (credentials, sleep, raw passthrough).
func (s *Server) rewriteGDLConfig() {
	if err := gdl.WriteManagedConfig(s.cfg.Current(), s.mapper.FlatTagSites()); err != nil {
		logx.Warnf("rewriting managed gallery-dl config: %v", err)
	}
}

// monbooruConfigured reports whether a monbooru instance is set up to push to.
// An empty API URL means none is, which the UI treats as unreachable without a
// connectivity probe - there is no host to dial.
func (s *Server) monbooruConfigured() bool {
	return s.cfg.Current().Monbooru.APIURL != ""
}

// checkMonbooru returns "ok", "rejected" (monbooru answered but refused the
// token), or "down" (no response) from a short-lived connectivity probe. The
// rejected state distinguishes a bad/expired token from an actual network
// outage, which otherwise both read as "unreachable".
func (s *Server) checkMonbooru(ctx context.Context) string {
	if !s.monbooruConfigured() {
		return "down"
	}
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	err := s.client.TestConnection(cctx)
	if err == nil {
		return "ok"
	}
	var ce *queue.CodedError
	if errors.As(err, &ce) && ce.Code == queue.ErrCodeMonbooruRejected {
		return "rejected"
	}
	return "down"
}
