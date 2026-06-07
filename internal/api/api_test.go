package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/leqwin/monloader/internal/config"
	"github.com/leqwin/monloader/internal/gdl"
	"github.com/leqwin/monloader/internal/mapping"
	"github.com/leqwin/monloader/internal/queue"
)

// fakeRunner satisfies gdl.Runner for the probe and version paths.
type fakeRunner struct{ probe gdl.ProbeResult }

func (f fakeRunner) Resolve(context.Context, string, string, bool) (gdl.ResolveResult, error) {
	return gdl.ResolveResult{}, nil
}
func (f fakeRunner) Download(context.Context, string, string, string, bool, func(int, gdl.Downloaded), bool) ([]gdl.Downloaded, error) {
	return nil, nil
}
func (f fakeRunner) ListExtractors(context.Context) ([]gdl.Extractor, error) { return nil, nil }
func (f fakeRunner) Probe(context.Context, string) (gdl.ProbeResult, error)  { return f.probe, nil }
func (f fakeRunner) Version(context.Context) string                          { return "1.32.1" }

// fakeProc produces an outcome keyed off the job URL so tests can drive the
// whole vocabulary deterministically.
type fakeProc struct{}

func (fakeProc) Process(ctx context.Context, job *queue.Job) error {
	url := job.Snapshot().URL
	job.SetItems([]queue.Item{{PostID: "1"}})
	switch {
	case strings.Contains(url, "cap"):
		job.UpdateItem(0, func(it *queue.Item) { it.Status = queue.ItemDownloaded })
		job.UpdateItem(0, func(it *queue.Item) { it.Status = queue.ItemUploaded })
		job.UpdateItem(0, func(it *queue.Item) {
			it.Status = queue.ItemDone
			it.Outcome = queue.OutcomeCreated
			it.MonbooruID = 99
		})
		job.SetCapped(1)
	case strings.Contains(url, "dup"):
		job.UpdateItem(0, func(it *queue.Item) { it.Status = queue.ItemDownloaded })
		job.UpdateItem(0, func(it *queue.Item) { it.Status = queue.ItemUploaded })
		job.UpdateItem(0, func(it *queue.Item) {
			it.Status = queue.ItemSkipped
			it.Outcome = queue.OutcomeDuplicate
			it.MonbooruID = 7
		})
	case strings.Contains(url, "fail"):
		job.UpdateItem(0, func(it *queue.Item) {
			it.Status = queue.ItemFailed
			it.Outcome = queue.OutcomeFailed
			it.ErrorCode = queue.ErrCodeMonbooruRejected
		})
	default:
		job.UpdateItem(0, func(it *queue.Item) { it.Status = queue.ItemDownloaded })
		job.UpdateItem(0, func(it *queue.Item) { it.Status = queue.ItemUploaded })
		job.UpdateItem(0, func(it *queue.Item) {
			it.Status = queue.ItemDone
			it.Outcome = queue.OutcomeCreated
			it.MonbooruID = 42
		})
	}
	return nil
}

func newTestServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	cfg := config.Default()
	cfg.Auth.APIToken = token
	cfg.Server.BaseURL = "http://localhost:8081"
	q := queue.New(fakeProc{}, 1, 100)
	q.Start()
	mapper, err := mapping.New(config.NewProvider(cfg))
	if err != nil {
		t.Fatal(err)
	}
	extractors := []gdl.Extractor{
		{Category: "danbooru", Subcategory: "post", Example: "https://example.com/posts/1"},
		{Category: "gelbooru", Subcategory: "post", Example: "https://example.com/index.php?id=1"},
		{Category: "weirdsite", Subcategory: "post", Example: "https://weird.example/1"},
	}
	h := New(q, fakeRunner{probe: gdl.ProbeResult{Status: gdl.ProbeOK}}, mapper, config.NewProvider(cfg), extractors, "v1.2.3", "1.32.1")
	mux := http.NewServeMux()
	h.Mount(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(func() { srv.Close(); q.Close() })
	return srv
}

func doJSON(t *testing.T, method, url, token, body string) (*http.Response, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	_ = json.Unmarshal(data, &out)
	return resp, out
}

func TestHealthNoAuth(t *testing.T) {
	srv := newTestServer(t, "secret") // token set, but /health is open
	resp, body := doJSON(t, "GET", srv.URL+"/health", "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body["status"] != "ok" || body["version"] != "v1.2.3" || body["gallerydl_version"] != "1.32.1" {
		t.Errorf("health body = %v", body)
	}
}

// TestHealthCORS pins that /health reflects the request Origin like the rest of
// the API, even though it bypasses the auth gate that sets CORS elsewhere; the
// browser extension reads it cross-origin.
func TestHealthCORS(t *testing.T) {
	srv := newTestServer(t, "secret")
	req, err := http.NewRequest("GET", srv.URL+"/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://booru.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://booru.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the reflected origin", got)
	}
}

func TestEnqueueAsync(t *testing.T) {
	srv := newTestServer(t, "")
	resp, body := doJSON(t, "POST", srv.URL+"/api/v1/queue", "", `{"url":"http://danbooru/posts/1"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if _, ok := body["job_id"]; !ok {
		t.Errorf("expected job_id, got %v", body)
	}
}

func TestEnqueueMissingURL(t *testing.T) {
	srv := newTestServer(t, "")
	resp, _ := doJSON(t, "POST", srv.URL+"/api/v1/queue", "", `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestEnqueueMaxItemsValidation(t *testing.T) {
	srv := newTestServer(t, "")
	// A negative max_items is rejected.
	resp, _ := doJSON(t, "POST", srv.URL+"/api/v1/queue", "", `{"url":"http://x","options":{"max_items":-5}}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("max_items=-5: status = %d, want 400", resp.StatusCode)
	}
	// Zero is accepted (indistinguishable from omitted; treated as no override).
	resp, _ = doJSON(t, "POST", srv.URL+"/api/v1/queue", "", `{"url":"http://x","options":{"max_items":0}}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("max_items=0: status = %d, want 202", resp.StatusCode)
	}
}

func TestEnqueueWaitResolvesOutcomes(t *testing.T) {
	srv := newTestServer(t, "")
	cases := []struct {
		url           string
		wantStatus    string
		wantOutcome   string
		wantMonbooru  float64
		wantErrorCode string
	}{
		{"http://danbooru/posts/1", "succeeded", "created", 42, ""},
		{"http://danbooru/posts/dup", "succeeded", "duplicate", 7, ""},
		{"http://danbooru/posts/fail", "failed", "failed", 0, queue.ErrCodeMonbooruRejected},
	}
	for _, c := range cases {
		resp, body := doJSON(t, "POST", srv.URL+"/api/v1/queue?wait=5", "", `{"url":"`+c.url+`"}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200 (resolved)", c.url, resp.StatusCode)
		}
		if body["status"] != c.wantStatus {
			t.Errorf("%s: job status = %v, want %s", c.url, body["status"], c.wantStatus)
		}
		items, _ := body["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("%s: items = %v", c.url, body["items"])
		}
		it := items[0].(map[string]any)
		if it["outcome"] != c.wantOutcome {
			t.Errorf("%s: outcome = %v, want %s", c.url, it["outcome"], c.wantOutcome)
		}
		if c.wantMonbooru != 0 && it["monbooru_id"] != c.wantMonbooru {
			t.Errorf("%s: monbooru_id = %v, want %v", c.url, it["monbooru_id"], c.wantMonbooru)
		}
		if c.wantErrorCode != "" && it["error_code"] != c.wantErrorCode {
			t.Errorf("%s: error_code = %v, want %s", c.url, it["error_code"], c.wantErrorCode)
		}
	}
}

func TestListGetRetryDelete(t *testing.T) {
	srv := newTestServer(t, "")
	// Enqueue a failing job and wait for it to settle.
	_, body := doJSON(t, "POST", srv.URL+"/api/v1/queue?wait=5", "", `{"url":"http://x/fail"}`)
	id := int64(body["id"].(float64))

	// List
	resp, list := doJSON(t, "GET", srv.URL+"/api/v1/queue", "", "")
	if resp.StatusCode != 200 || list["total"].(float64) < 1 {
		t.Errorf("list total = %v", list["total"])
	}
	// Get
	resp, got := doJSON(t, "GET", srv.URL+"/api/v1/queue/"+itoa(id), "", "")
	if resp.StatusCode != 200 || int64(got["id"].(float64)) != id {
		t.Errorf("get id = %v, want %d", got["id"], id)
	}
	// Get unknown
	resp, _ = doJSON(t, "GET", srv.URL+"/api/v1/queue/99999", "", "")
	if resp.StatusCode != 404 {
		t.Errorf("get unknown status = %d, want 404", resp.StatusCode)
	}
	// Retry
	resp, _ = doJSON(t, "POST", srv.URL+"/api/v1/queue/"+itoa(id)+"/retry", "", "")
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("retry status = %d, want 202", resp.StatusCode)
	}
	// Wait for the retried job to settle, then delete (remove) it.
	doJSON(t, "GET", srv.URL+"/api/v1/queue/"+itoa(id), "", "")
	resp, _ = doJSON(t, "DELETE", srv.URL+"/api/v1/queue/"+itoa(id), "", "")
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete status = %d, want 204", resp.StatusCode)
	}
	resp, _ = doJSON(t, "GET", srv.URL+"/api/v1/queue/"+itoa(id), "", "")
	if resp.StatusCode != 404 {
		t.Errorf("after delete, get status = %d, want 404", resp.StatusCode)
	}
}

// TestRetryForce checks that ?force=1 on the retry endpoint re-queues the job
// with the archive-bypass flag set, surfaced as "force" on the job.
func TestRetryForce(t *testing.T) {
	srv := newTestServer(t, "")

	// wait so the job is finished (retryable) before the retry call.
	resp, job := doJSON(t, "POST", srv.URL+"/api/v1/queue?wait=5", "", `{"url":"http://danbooru/posts/1"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("enqueue wait status = %d, want 200", resp.StatusCode)
	}
	id := int64(job["id"].(float64))

	resp, _ = doJSON(t, "POST", srv.URL+"/api/v1/queue/"+itoa(id)+"/retry?force=1", "", "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("force retry status = %d, want 202", resp.StatusCode)
	}
	if _, got := doJSON(t, "GET", srv.URL+"/api/v1/queue/"+itoa(id), "", ""); got["force"] != true {
		t.Errorf("job force = %v, want true", got["force"])
	}
}

// TestContinueJob checks the continue endpoint queues a follow-up for the next
// window of a capped job and rejects a job that was never capped.
func TestContinueJob(t *testing.T) {
	srv := newTestServer(t, "")

	_, job := doJSON(t, "POST", srv.URL+"/api/v1/queue?wait=5", "", `{"url":"http://x/cap"}`)
	id := int64(job["id"].(float64))
	resp, body := doJSON(t, "POST", srv.URL+"/api/v1/queue/"+itoa(id)+"/continue", "", "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("continue status = %d, want 202", resp.StatusCode)
	}
	newID := int64(body["job_id"].(float64))
	if newID == id {
		t.Errorf("continue should return a new job id, got the source %d", id)
	}
	if r, _ := doJSON(t, "GET", srv.URL+"/api/v1/queue/"+itoa(newID), "", ""); r.StatusCode != 200 {
		t.Errorf("continued job %d should exist, get status = %d", newID, r.StatusCode)
	}

	// A non-capped job has no next window (409); an unknown id is 404.
	_, plain := doJSON(t, "POST", srv.URL+"/api/v1/queue?wait=5", "", `{"url":"http://x/ok"}`)
	pid := int64(plain["id"].(float64))
	if r, _ := doJSON(t, "POST", srv.URL+"/api/v1/queue/"+itoa(pid)+"/continue", "", ""); r.StatusCode != http.StatusConflict {
		t.Errorf("continue on a non-capped job = %d, want 409", r.StatusCode)
	}
	if r, _ := doJSON(t, "POST", srv.URL+"/api/v1/queue/99999/continue", "", ""); r.StatusCode != http.StatusNotFound {
		t.Errorf("continue on unknown id = %d, want 404", r.StatusCode)
	}
}

func TestBearerAuth(t *testing.T) {
	srv := newTestServer(t, "secret")
	// No token -> 401 on a guarded endpoint.
	resp, _ := doJSON(t, "GET", srv.URL+"/api/v1/queue", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("tokenless status = %d, want 401", resp.StatusCode)
	}
	// Wrong token -> 401.
	resp, _ = doJSON(t, "GET", srv.URL+"/api/v1/queue", "wrong", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong-token status = %d, want 401", resp.StatusCode)
	}
	// Right token -> 200.
	resp, _ = doJSON(t, "GET", srv.URL+"/api/v1/queue", "secret", "")
	if resp.StatusCode != 200 {
		t.Errorf("good-token status = %d, want 200", resp.StatusCode)
	}
	// /health stays open.
	resp, _ = doJSON(t, "GET", srv.URL+"/health", "", "")
	if resp.StatusCode != 200 {
		t.Errorf("health status = %d, want 200", resp.StatusCode)
	}
}

func TestListSites(t *testing.T) {
	srv := newTestServer(t, "")
	resp, body := doJSON(t, "GET", srv.URL+"/api/v1/sites", "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	sites := body["sites"].([]any)
	// Curated sites (danbooru, gelbooru) sort ahead of the uncurated weirdsite.
	first := sites[0].(map[string]any)
	if first["curated"] != true {
		t.Errorf("first site should be curated, got %v", first)
	}
	// A curated profile for a multi-instance family is surfaced under its own
	// host even though gallery-dl's --list-extractors only names one instance:
	// aibooru is not in the extractor fixture, but its profile must appear so a
	// client recognizes aibooru.online.
	var aibooru map[string]any
	for _, s := range sites {
		if e := s.(map[string]any); e["category"] == "aibooru" {
			aibooru = e
			break
		}
	}
	if aibooru == nil {
		t.Fatal("aibooru not listed; a multi-instance curated profile must be surfaced")
	}
	if aibooru["curated"] != true {
		t.Errorf("aibooru should be curated, got %v", aibooru)
	}
	if ex, _ := aibooru["example"].(string); !strings.Contains(ex, "aibooru.online") {
		t.Errorf("aibooru example = %q, want the aibooru.online host so a client can match it", ex)
	}
	// Filter narrows the list, and a curated profile already named as an
	// extractor is not duplicated (gelbooru is in the extractor fixture).
	_, filtered := doJSON(t, "GET", srv.URL+"/api/v1/sites?q=gel", "", "")
	fs := filtered["sites"].([]any)
	if len(fs) != 1 || fs[0].(map[string]any)["category"] != "gelbooru" {
		t.Errorf("q=gel should return only gelbooru, got %v", fs)
	}
}

func TestSiteProbe(t *testing.T) {
	srv := newTestServer(t, "")
	resp, body := doJSON(t, "POST", srv.URL+"/api/v1/sites/danbooru/test", "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body["status"] != string(gdl.ProbeOK) {
		t.Errorf("probe status = %v, want ok", body["status"])
	}
	// Unknown site with no url -> 404.
	resp, _ = doJSON(t, "POST", srv.URL+"/api/v1/sites/nosuchsite/test", "", "")
	if resp.StatusCode != 404 {
		t.Errorf("unknown site status = %d, want 404", resp.StatusCode)
	}
}

func TestOpenAPIStructure(t *testing.T) {
	srv := newTestServer(t, "")
	resp, err := http.Get(srv.URL + "/api/v1/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("openapi is not valid JSON: %v", err)
	}
	if spec["openapi"] != "3.0.3" {
		t.Errorf("openapi version = %v, want 3.0.3", spec["openapi"])
	}
	info := spec["info"].(map[string]any)
	if info["title"] == "" || info["version"] == "" {
		t.Error("info.title/version must be set")
	}
	paths := spec["paths"].(map[string]any)
	if len(paths) == 0 {
		t.Fatal("no paths")
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)

	// Every $ref must resolve to a defined schema.
	var refs []string
	collectRefs(spec, &refs)
	for _, ref := range refs {
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		if _, ok := schemas[name]; !ok {
			t.Errorf("dangling $ref %q", ref)
		}
	}
	// Every operation must declare responses.
	for p, item := range paths {
		ops := item.(map[string]any)
		for m, op := range ops {
			o := op.(map[string]any)
			if _, ok := o["responses"]; !ok {
				t.Errorf("%s %s has no responses", m, p)
			}
		}
	}
}

func TestDocsRenders(t *testing.T) {
	srv := newTestServer(t, "")
	resp, err := http.Get(srv.URL + "/api/v1/docs")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
	if !bytes.Contains(body, []byte("/api/v1/queue")) {
		t.Error("docs should list the queue endpoint")
	}
}

// TestDocEndpointsCORS pins that the unauthenticated self-doc endpoints reflect
// the request Origin too; like /health they bypass the auth gate that sets CORS
// on the rest of the API.
func TestDocEndpointsCORS(t *testing.T) {
	srv := newTestServer(t, "secret")
	for _, path := range []string{"/api/v1/openapi.json", "/api/v1/docs"} {
		req, err := http.NewRequest("GET", srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", "https://booru.example")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://booru.example" {
			t.Errorf("%s: Access-Control-Allow-Origin = %q, want the reflected origin", path, got)
		}
	}
}

func collectRefs(v any, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if k == "$ref" {
				if s, ok := val.(string); ok {
					*out = append(*out, s)
				}
			} else {
				collectRefs(val, out)
			}
		}
	case []any:
		for _, e := range t {
			collectRefs(e, out)
		}
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
