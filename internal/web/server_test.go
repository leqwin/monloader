package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/leqwin/monloader/internal/config"
	"github.com/leqwin/monloader/internal/gdl"
	"github.com/leqwin/monloader/internal/mapping"
	"github.com/leqwin/monloader/internal/monbooru"
	"github.com/leqwin/monloader/internal/queue"
	"github.com/leqwin/monloader/internal/sitestate"
	"golang.org/x/crypto/bcrypt"
)

// fakeRunner is the web tests' gdl.Runner. A zero probe reports ok; set probe
// to drive testSite down its auth/blocked/failed branches.
type fakeRunner struct{ probe gdl.ProbeResult }

func (fakeRunner) Resolve(context.Context, string, string, bool) (gdl.ResolveResult, error) {
	return gdl.ResolveResult{}, nil
}
func (fakeRunner) Download(context.Context, string, string, string, bool, func(int, gdl.Downloaded), bool) ([]gdl.Downloaded, error) {
	return nil, nil
}
func (fakeRunner) ListExtractors(context.Context) ([]gdl.Extractor, error) { return nil, nil }
func (f fakeRunner) Probe(context.Context, string) (gdl.ProbeResult, error) {
	if f.probe.Status == "" {
		return gdl.ProbeResult{Status: gdl.ProbeOK}, nil
	}
	return f.probe, nil
}
func (fakeRunner) Version(context.Context) string { return "1.32.1" }

type noopProc struct{}

func (noopProc) Process(context.Context, *queue.Job) error { return nil }

// monbooruStub answers the two endpoints the UI calls.
func monbooruStub() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/galleries":
			_, _ = w.Write([]byte(`[{"name":"default","images":3,"tags":2,"active":true}]`))
		default:
			_, _ = w.Write([]byte(`{"api":"monbooru"}`))
		}
	}))
}

func newWebServer(t *testing.T, monbooruURL, password string) *Server {
	t.Helper()
	cfg := config.Default()
	cfg.Monbooru.APIURL = monbooruURL
	if password != "" {
		cfg.Auth.EnablePassword = true
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Auth.PasswordHash = string(hash)
	}
	provider := config.NewProvider(cfg)
	q := queue.New(noopProc{}, 1, 100)
	q.Start()
	t.Cleanup(q.Close)
	mapper, err := mapping.New(provider)
	if err != nil {
		t.Fatal(err)
	}
	extractors := []gdl.Extractor{{Category: "danbooru", Subcategory: "post", Example: "https://example.com/posts/1"}}
	srv, err := NewServer(provider, filepath.Join(t.TempDir(), "monloader.toml"), q, monbooru.New(provider), fakeRunner{}, mapper, extractors, "1.32.1", sitestate.New())
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func get(t *testing.T, ts *httptest.Server, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

var csrfRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

func TestAddScreen(t *testing.T) {
	mb := monbooruStub()
	defer mb.Close()
	ts := httptest.NewServer(newWebServer(t, mb.URL, "").Handler())
	defer ts.Close()

	status, body := get(t, ts, "/")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	for _, want := range []string{`id="add-input"`, "search-input-wrap", "monloader", "/queue", "/settings", `name="_csrf"`, ">download<"} {
		if !strings.Contains(body, want) {
			t.Errorf("add screen missing %q", want)
		}
	}
}

func TestQueueScreenHasPoll(t *testing.T) {
	mb := monbooruStub()
	defer mb.Close()
	ts := httptest.NewServer(newWebServer(t, mb.URL, "").Handler())
	defer ts.Close()

	status, body := get(t, ts, "/queue")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	if !strings.Contains(body, `hx-trigger="load, every 2s"`) {
		t.Error("queue screen should poll every 2s")
	}
	if !strings.Contains(body, "queue-rows") {
		t.Error("queue screen should have the rows container")
	}
}

func TestSettingsScreenSections(t *testing.T) {
	mb := monbooruStub()
	defer mb.Close()
	ts := httptest.NewServer(newWebServer(t, mb.URL, "").Handler())
	defer ts.Close()

	status, body := get(t, ts, "/settings")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	for _, want := range []string{">monloader authentication<", ">monbooru<", ">downloads<", ">sites ", ">advanced<", ">stats<", "Goroutines", `name="_csrf"`, "danbooru"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings missing %q", want)
		}
	}
	// monbooru is ordered above the (renamed) authentication section.
	if i, j := strings.Index(body, ">monbooru<"), strings.Index(body, ">monloader authentication<"); i < 0 || j < 0 || i > j {
		t.Errorf("monbooru should render above monloader authentication (monbooru=%d, auth=%d)", i, j)
	}
	// The default gallery dropdown is populated from the monbooru stub.
	if !strings.Contains(body, "default (3 img)") {
		t.Error("settings should list galleries from monbooru")
	}
}

func TestEnqueueViaForm(t *testing.T) {
	mb := monbooruStub()
	defer mb.Close()
	web := newWebServer(t, mb.URL, "")
	ts := httptest.NewServer(web.Handler())
	defer ts.Close()

	// Grab a valid CSRF token from the rendered add screen.
	_, body := get(t, ts, "/")
	m := csrfRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no CSRF token in the add form")
	}
	token := m[1]

	resp, err := http.PostForm(ts.URL+"/", url.Values{
		"_csrf": {token},
		"url":   {"https://example.com/posts/1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("enqueue status = %d", resp.StatusCode)
	}
	// A good submit redirects the operator to the queue screen.
	if loc := resp.Header.Get("HX-Redirect"); loc != "/queue" {
		t.Errorf("enqueue should HX-Redirect to /queue, got %q", loc)
	}
	if _, total := web.queue.List(queue.ListOptions{}); total != 1 {
		t.Errorf("queue should have 1 job, got %d", total)
	}
}

func TestCSRFRejectsMissingToken(t *testing.T) {
	mb := monbooruStub()
	defer mb.Close()
	ts := httptest.NewServer(newWebServer(t, mb.URL, "").Handler())
	defer ts.Close()
	resp, err := http.PostForm(ts.URL+"/", url.Values{"url": {"http://x"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a tokenless POST should be 403, got %d", resp.StatusCode)
	}
}

func TestPasswordRedirectsToLogin(t *testing.T) {
	mb := monbooruStub()
	defer mb.Close()
	ts := httptest.NewServer(newWebServer(t, mb.URL, "secret").Handler())
	defer ts.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(ts.URL + "/queue")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Errorf("redirect to %q, want /login", loc)
	}
}

func TestConnLight(t *testing.T) {
	mb := monbooruStub()
	defer mb.Close()
	up := httptest.NewServer(newWebServer(t, mb.URL, "").Handler())
	defer up.Close()
	if _, body := get(t, up, "/internal/monbooru-status"); !strings.Contains(body, "dot-ok") {
		t.Errorf("reachable monbooru should render a green dot, got %q", body)
	}

	// Point a fresh server at a closed monbooru to get the red dot.
	dead := monbooruStub()
	deadURL := dead.URL
	dead.Close()
	down := httptest.NewServer(newWebServer(t, deadURL, "").Handler())
	defer down.Close()
	if _, body := get(t, down, "/internal/monbooru-status"); !strings.Contains(body, "dot-down") {
		t.Errorf("unreachable monbooru should render a red dot, got %q", body)
	}

	// A reachable monbooru that refuses the token shows the amber "rejected"
	// dot, distinct from the red "unreachable".
	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer rejecting.Close()
	rej := httptest.NewServer(newWebServer(t, rejecting.URL, "").Handler())
	defer rej.Close()
	if _, body := get(t, rej, "/internal/monbooru-status"); !strings.Contains(body, "dot-rejected") {
		t.Errorf("a rejected token should render the amber rejected dot, got %q", body)
	}
}

// TestConnLightUnconfigured checks the synchronous unreachable path: a blank
// API URL reports down without a probe and exposes the machine-readable state
// the add-bar JS reads.
func TestConnLightUnconfigured(t *testing.T) {
	srv := newWebServer(t, "", "")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	_, body := get(t, ts, "/internal/monbooru-status")
	if !strings.Contains(body, "dot-down") {
		t.Errorf("unconfigured monbooru should render a red dot, got %q", body)
	}
	if !strings.Contains(body, `data-conn="down"`) {
		t.Errorf("conn light should expose data-conn for the add-bar JS, got %q", body)
	}
}

// TestMonbooruBannerAndSubmitGate covers the unreachable guardrail on the add
// and queue screens: with no monbooru configured the banner shows and the add
// bar is disabled; once a monbooru is configured the banner is hidden and the
// add bar is enabled.
func TestMonbooruBannerAndSubmitGate(t *testing.T) {
	// Unconfigured: banner visible, add bar disabled.
	down := httptest.NewServer(newWebServer(t, "", "").Handler())
	defer down.Close()
	for _, path := range []string{"/", "/queue"} {
		_, body := get(t, down, path)
		if !strings.Contains(body, `id="monbooru-banner"`) {
			t.Errorf("%s should render the monbooru banner, got %q", path, body)
		}
		if !strings.Contains(body, `id="monbooru-banner-msg"`) {
			t.Errorf("%s banner should carry the message span the add-bar JS sets, got %q", path, body)
		}
		if strings.Contains(body, `class="monbooru-banner" hidden`) {
			t.Errorf("%s banner should be visible when unconfigured", path)
		}
		if !strings.Contains(body, `class="needs-monbooru" disabled`) {
			t.Errorf("%s should disable the add bar when unconfigured, got %q", path, body)
		}
	}

	// Configured + reachable: banner hidden, add bar enabled.
	mb := monbooruStub()
	defer mb.Close()
	up := httptest.NewServer(newWebServer(t, mb.URL, "").Handler())
	defer up.Close()
	for _, path := range []string{"/", "/queue"} {
		_, body := get(t, up, path)
		if !strings.Contains(body, `class="monbooru-banner" hidden`) {
			t.Errorf("%s banner should be hidden when configured, got %q", path, body)
		}
		if strings.Contains(body, `class="needs-monbooru" disabled`) {
			t.Errorf("%s add bar should be enabled when configured", path)
		}
	}
}

func TestConfigSaveIsRaceFree(t *testing.T) {
	// A settings save publishes a new snapshot instead of mutating in place, so
	// it must not race readers on other goroutines (the worker mapping a push,
	// the connectivity poll). Overlap saves with reads of the same fields;
	// -race fails the build if the swap is not atomic.
	mb := monbooruStub()
	defer mb.Close()
	srv := newWebServer(t, mb.URL, "")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = srv.cfg.Current().Monbooru.APIToken
					_ = len(srv.cfg.Current().Sites)
					_ = srv.mapper.Gallery("gelbooru")
				}
			}
		}()
	}
	for i := 0; i < 100; i++ {
		err := srv.updateConfig(func(c *config.Config) error {
			c.Monbooru.APIToken = "tok" + strconv.Itoa(i)
			c.Sites = append(c.Sites, config.Site{Name: "gelbooru", Gallery: "g"})
			return nil
		})
		if err != nil {
			t.Fatalf("updateConfig: %v", err)
		}
	}
	close(stop)
	wg.Wait()
}
