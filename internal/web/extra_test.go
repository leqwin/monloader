package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leqwin/monloader/internal/config"
	"github.com/leqwin/monloader/internal/gdl"
	"github.com/leqwin/monloader/internal/mapping"
	"github.com/leqwin/monloader/internal/monbooru"
	"github.com/leqwin/monloader/internal/queue"
	"github.com/leqwin/monloader/internal/sitestate"
)

// itemsProc drives a job to a created + a failed item so the queue_rows
// partial exercises the monbooru-link and error-code branches.
type itemsProc struct{}

func (itemsProc) Process(_ context.Context, job *queue.Job) error {
	job.SetItems([]queue.Item{
		{PostID: "a", Num: 1, URL: "https://example.com/posts/100"},
		{PostID: "b", Num: 2},
	})
	job.UpdateItem(0, func(it *queue.Item) { it.Status = queue.ItemDownloaded })
	job.UpdateItem(0, func(it *queue.Item) { it.Status = queue.ItemUploaded })
	job.UpdateItem(0, func(it *queue.Item) {
		it.Status = queue.ItemDone
		it.Outcome = queue.OutcomeCreated
		it.MonbooruID = 5
	})
	job.UpdateItem(1, func(it *queue.Item) {
		it.Status = queue.ItemFailed
		it.Outcome = queue.OutcomeFailed
		it.ErrorCode = queue.ErrCodeMonbooruRejected
	})
	return nil
}

func serverWith(t *testing.T, proc queue.Processor) *Server {
	t.Helper()
	mb := monbooruStub()
	t.Cleanup(mb.Close)
	cfg := config.Default()
	cfg.Monbooru.APIURL = mb.URL
	provider := config.NewProvider(cfg)
	q := queue.New(proc, 1, 100)
	q.Start()
	t.Cleanup(q.Close)
	mapper, err := mapping.New(provider)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(provider, filepath.Join(t.TempDir(), "monloader.toml"), q, monbooru.New(provider), fakeRunner{}, mapper, []gdl.Extractor{}, "1.32.1", sitestate.New())
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// cappedProc drives a job to a created item and flags it capped, so the
// queue_rows partial exercises the "capped at N" note.
type cappedProc struct{}

func (cappedProc) Process(_ context.Context, job *queue.Job) error {
	job.SetItems([]queue.Item{{PostID: "1"}})
	job.UpdateItem(0, func(it *queue.Item) { it.Status = queue.ItemDownloaded })
	job.UpdateItem(0, func(it *queue.Item) { it.Status = queue.ItemUploaded })
	job.UpdateItem(0, func(it *queue.Item) {
		it.Status = queue.ItemDone
		it.Outcome = queue.OutcomeCreated
	})
	job.SetCapped(8)
	return nil
}

func TestQueueRowsShowsCap(t *testing.T) {
	srv := serverWith(t, cappedProc{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	id := srv.queue.Enqueue("http://danbooru/posts?tags=x", queue.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 2e9)
	defer cancel()
	if _, err := srv.queue.Wait(ctx, id); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if _, body := get(t, ts, "/internal/queue-rows"); !strings.Contains(body, "capped at 8") {
		t.Errorf("a capped job should show the cap note, got %q", body)
	}
}

func TestContinueButton(t *testing.T) {
	srv := serverWith(t, cappedProc{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	id := srv.queue.Enqueue("http://danbooru/posts?tags=x", queue.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 2e9)
	defer cancel()
	if _, err := srv.queue.Wait(ctx, id); err != nil {
		t.Fatalf("wait: %v", err)
	}
	// The capped row offers a continue button.
	if _, body := get(t, ts, "/internal/queue-rows"); !strings.Contains(body, "/queue/"+itoa(id)+"/continue") {
		t.Error("a capped job row should offer a continue button")
	}
	// Posting it queues a follow-up job for the next window.
	before, _ := srv.queue.List(queue.ListOptions{})
	resp := postForm(t, ts, srv, "/queue/"+itoa(id)+"/continue", map[string][]string{})
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("continue status = %d", resp.StatusCode)
	}
	if after, _ := srv.queue.List(queue.ListOptions{}); len(after) != len(before)+1 {
		t.Errorf("continue should queue one follow-up job: before=%d after=%d", len(before), len(after))
	}
}

func TestQueueRowsRendersItems(t *testing.T) {
	srv := serverWith(t, itemsProc{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	id := srv.queue.Enqueue("http://danbooru/pools/1", queue.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 2e9)
	defer cancel()
	if _, err := srv.queue.Wait(ctx, id); err != nil {
		t.Fatalf("wait: %v", err)
	}
	_, body := get(t, ts, "/internal/queue-rows")
	// data-job keys the <details> so the client can restore an expanded job
	// across the 2s poll swap (it would otherwise re-collapse).
	for _, want := range []string{"/images/5", `<a href="https://example.com/posts/100"`, "o-created", "o-failed", "monbooru_rejected", "items", "data-job=", "items-row", "created</span>", "total</span>"} {
		if !strings.Contains(body, want) {
			t.Errorf("queue rows missing %q", want)
		}
	}
}

func TestSettingsSiteGroups(t *testing.T) {
	srv := serverWith(t, noopProc{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	_, body := get(t, ts, "/settings")
	// Two grouped tables, an action column with per-row edit buttons, and the
	// shared edit pop-in; a booru and a manga site land in their groups.
	for _, want := range []string{
		">boorus<", "manga / comics", "<th>action</th>",
		`class="edit-site"`, `id="site-edit-dialog"`,
		"danbooru", "nhentai",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings missing %q", want)
		}
	}
}

func TestLoginInfo(t *testing.T) {
	withKey := &config.Site{APIKey: "k"}
	withCookies := &config.Site{Cookies: "/c"}
	cases := []struct {
		auth      string
		site      *config.Site
		label     string
		needsCred bool
	}{
		{"api_optional", nil, "api (opt)", false},
		{"api_required", nil, "api key", true},
		{"api_required", withKey, "api key", false},
		{"cookies", nil, "cookies", true},
		{"cookies", withCookies, "cookies", false},
		{"oauth", nil, "oauth", false},
		{"none", nil, "none", false},
		{"", nil, "none", false},
	}
	for _, c := range cases {
		label, needs := loginInfo(c.auth, c.site)
		if label != c.label || needs != c.needsCred {
			t.Errorf("loginInfo(%q,%v) = (%q,%v), want (%q,%v)", c.auth, c.site, label, needs, c.label, c.needsCred)
		}
	}
}

func TestTestMonbooruFailure(t *testing.T) {
	dead := monbooruStub()
	deadURL := dead.URL
	dead.Close()
	srv := serverWith(t, noopProc{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp := postForm(t, ts, srv, "/settings/monbooru/test", map[string][]string{"api_url": {deadURL}})
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "kind=err") {
		t.Errorf("unreachable monbooru test should flash err, loc=%q", loc)
	}
}

func TestServeCustomCSS(t *testing.T) {
	srv := serverWith(t, noopProc{})
	// Unset -> 404.
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	if code, _ := get(t, ts, "/custom.css"); code != 404 {
		t.Errorf("unset custom.css = %d, want 404", code)
	}
	// Set -> served.
	cssPath := filepath.Join(t.TempDir(), "custom.css")
	if err := os.WriteFile(cssPath, []byte(":root{--accent:#abc}"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.cfg.Current().Server.CustomCSS = cssPath
	if code, body := get(t, ts, "/custom.css"); code != 200 || !strings.Contains(body, "--accent") {
		t.Errorf("custom.css = %d %q", code, body)
	}
}

func TestServeCustomLogo(t *testing.T) {
	srv := serverWith(t, noopProc{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	// Unset -> 404.
	if code, _ := get(t, ts, "/custom.logo"); code != 404 {
		t.Errorf("unset custom.logo = %d, want 404", code)
	}
	// Set -> served.
	logoPath := filepath.Join(t.TempDir(), "logo.png")
	if err := os.WriteFile(logoPath, []byte("PNGBYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.cfg.Current().Server.BooruLogo = logoPath
	if code, body := get(t, ts, "/custom.logo"); code != 200 || !strings.Contains(body, "PNGBYTES") {
		t.Errorf("custom.logo = %d %q", code, body)
	}
}

// Unset branding leaves the bundled assets and the "monloader" wordmark in
// place across the landing (h1) and topbar (span) surfaces; nothing points at
// the override route.
func TestBrandingDefaults(t *testing.T) {
	srv := serverWith(t, noopProc{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	_, landing := get(t, ts, "/")
	for _, want := range []string{`href="/static/favicon.png"`, `src="/static/logo.png"`, `<h1>monloader</h1>`, `<title>monloader</title>`} {
		if !strings.Contains(landing, want) {
			t.Errorf("default landing page missing %q", want)
		}
	}
	_, queue := get(t, ts, "/queue")
	for _, want := range []string{`src="/static/logo.png"`, `<span>monloader</span>`, `<title>queue - monloader</title>`} {
		if !strings.Contains(queue, want) {
			t.Errorf("default queue page missing %q", want)
		}
	}
	if strings.Contains(landing, "/custom.logo") || strings.Contains(queue, "/custom.logo") {
		t.Error("default pages must not reference /custom.logo")
	}
}

// A configured name and logo reach the title, the wordmark, and the favicon
// link across the landing, topbar, and login surfaces; the single logo path
// backs both the favicon and the logo image.
func TestBrandingOverride(t *testing.T) {
	srv := serverWith(t, noopProc{})
	srv.cfg.Current().Server.BooruName = "Privloader"
	srv.cfg.Current().Server.BooruLogo = "/some/path/logo.png"
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	_, landing := get(t, ts, "/")
	for _, want := range []string{`href="/custom.logo"`, `src="/custom.logo"`, `<h1>Privloader</h1>`, `<title>Privloader</title>`} {
		if !strings.Contains(landing, want) {
			t.Errorf("branded landing page missing %q", want)
		}
	}
	if strings.Contains(landing, "/static/logo.png") || strings.Contains(landing, "/static/favicon.png") {
		t.Error("branded landing page should not link the bundled assets")
	}
	_, queue := get(t, ts, "/queue")
	for _, want := range []string{`src="/custom.logo"`, `<span>Privloader</span>`, `<title>queue - Privloader</title>`} {
		if !strings.Contains(queue, want) {
			t.Errorf("branded queue page missing %q", want)
		}
	}

	// The login page is served before auth, so it carries the same brand.
	mb := monbooruStub()
	defer mb.Close()
	authed := newWebServer(t, mb.URL, "secret")
	authed.cfg.Current().Server.BooruName = "Privloader"
	authed.cfg.Current().Server.BooruLogo = "/some/path/logo.png"
	lts := httptest.NewServer(authed.Handler())
	defer lts.Close()
	_, login := get(t, lts, "/login")
	if !strings.Contains(login, "<h1>Privloader</h1>") {
		t.Errorf("branded login heading missing, got %q", login)
	}
	if !strings.Contains(login, `href="/custom.logo"`) {
		t.Error("branded login favicon should point at /custom.logo")
	}
}

func TestLoginPageRedirectsWhenNoPassword(t *testing.T) {
	srv := serverWith(t, noopProc{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(ts.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Errorf("login with no password set should redirect to /, got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestLogoutClearsSession(t *testing.T) {
	mb := monbooruStub()
	defer mb.Close()
	srv := newWebServer(t, mb.URL, "secret")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Establish a session.
	id, err := srv.sessions.New(7)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: "monloader_session", Value: id}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest("POST", ts.URL+"/logout", strings.NewReader("_csrf="+srv.csrfToken(id)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("logout status = %d, want 303", resp.StatusCode)
	}
	if _, ok := srv.sessions.Get(id); ok {
		t.Error("session should be deleted after logout")
	}
}
