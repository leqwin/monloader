package gdl

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/leqwin/monloader/internal/kwdict"
)

func TestMediaExtension(t *testing.T) {
	for _, tc := range []struct {
		ct   string
		want string
		ok   bool
	}{
		{"image/jpeg", "jpg", true},
		{"image/jpeg; charset=binary", "jpg", true},
		{"IMAGE/PNG", "png", true}, // ParseMediaType lowercases the type
		{"image/webp", "webp", true},
		{"video/mp4", "mp4", true},
		{"text/html", "", false},
		{"application/json", "", false},
		{"application/octet-stream", "", false},
		{"", "", false},
		{"not a media type", "", false},
	} {
		got, ok := mediaExtension(tc.ct)
		if ok != tc.ok || got != tc.want {
			t.Errorf("mediaExtension(%q) = (%q, %v), want (%q, %v)", tc.ct, got, ok, tc.want, tc.ok)
		}
	}
}

func TestDirectlinkMeta(t *testing.T) {
	for _, tc := range []struct {
		raw                          string
		domain, path, filename       string
		query, fragment, subcategory string
	}{
		{
			raw:    "https://yt3.ggpht.com/ytc/AIdro_avatar=s400-c-k-no-rj",
			domain: "yt3.ggpht.com", path: "ytc", filename: "AIdro_avatar=s400-c-k-no-rj",
			subcategory: "ggpht.com",
		},
		{
			raw:    "https://cdn.example.com/image", // root-level, no directory
			domain: "cdn.example.com", path: "", filename: "image",
			subcategory: "example.com",
		},
		{
			raw:    "https://host.example.com/a/b/c?size=large#frag",
			domain: "host.example.com", path: "a/b", filename: "c",
			query: "size=large", fragment: "frag", subcategory: "example.com",
		},
	} {
		u, ok := parseHTTPURL(tc.raw)
		if !ok {
			t.Fatalf("parseHTTPURL(%q) failed", tc.raw)
		}
		meta := directlinkMeta(u)
		if got := kwdict.String(meta, "category"); got != "directlink" {
			t.Errorf("%s: category = %q, want directlink", tc.raw, got)
		}
		if got := kwdict.String(meta, "domain"); got != tc.domain {
			t.Errorf("%s: domain = %q, want %q", tc.raw, got, tc.domain)
		}
		if got := kwdict.String(meta, "path"); got != tc.path {
			t.Errorf("%s: path = %q, want %q", tc.raw, got, tc.path)
		}
		if got := kwdict.String(meta, "filename"); got != tc.filename {
			t.Errorf("%s: filename = %q, want %q", tc.raw, got, tc.filename)
		}
		// The submitted URL carries no media extension, so the rebuilt URL must
		// not gain one; the mapping package pins the full reconstruction.
		if got := kwdict.String(meta, "extension"); got != "" {
			t.Errorf("%s: extension = %q, want empty", tc.raw, got)
		}
		if got := kwdict.String(meta, "query"); got != tc.query {
			t.Errorf("%s: query = %q, want %q", tc.raw, got, tc.query)
		}
		if got := kwdict.String(meta, "fragment"); got != tc.fragment {
			t.Errorf("%s: fragment = %q, want %q", tc.raw, got, tc.fragment)
		}
		if got := kwdict.String(meta, "subcategory"); got != tc.subcategory {
			t.Errorf("%s: subcategory = %q, want %q", tc.raw, got, tc.subcategory)
		}
	}
}

func TestParseHTTPURLRejectsNonHTTP(t *testing.T) {
	for _, raw := range []string{"ftp://example.com/x", "file:///etc/passwd", "mailto:a@b.c", "/relative/path", "example.com/no-scheme"} {
		if _, ok := parseHTTPURL(raw); ok {
			t.Errorf("parseHTTPURL(%q) accepted a non-http(s) URL", raw)
		}
	}
}

// mediaServer serves image/jpeg bytes at any path, modeling a CDN that hosts an
// image at an extension-less URL (gallery-dl's directlink cannot match it).
func mediaServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProbeContentTypeFallsBackToGET(t *testing.T) {
	// A server that does not implement HEAD (404) but serves the file on GET; the
	// probe must fall back to a ranged GET to read the content type.
	var gotRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotRange = r.Header.Get("Range")
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG"))
	}))
	defer srv.Close()

	ct, ok := probeContentType(context.Background(), srv.URL+"/avatar")
	if !ok {
		t.Fatal("probe should fall back to GET when HEAD is not 200")
	}
	if ext, _ := mediaExtension(ct); ext != "png" {
		t.Errorf("content type = %q, want image/png", ct)
	}
	if gotRange != "bytes=0-0" {
		t.Errorf("GET probe Range = %q, want bytes=0-0 (must not pull the whole file)", gotRange)
	}
}

func TestProbeContentTypeHonorsTimeout(t *testing.T) {
	// A host that never answers must not stall the probe for the full client
	// ceiling: probeContentType has to give up after directProbeTimeout.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	old := directProbeTimeout
	directProbeTimeout = 100 * time.Millisecond
	defer func() { directProbeTimeout = old }()

	done := make(chan bool, 1)
	go func() {
		_, ok := probeContentType(context.Background(), srv.URL+"/avatar")
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Error("probe of a hanging host should fail, not succeed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("probe did not honor directProbeTimeout; it hung past the deadline")
	}
}

// The fallback runs after gallery-dl rejects the URL, so these need the real
// binary; a machine without it skips, like the other live wrapper tests.
func TestLiveResolveDirectlinkFallback(t *testing.T) {
	tool := liveTool(t)
	srv := mediaServer(t, []byte("\xff\xd8\xffjpeg"))
	rawURL := srv.URL + "/ytc/AIdro_avatar=s400-c-k-no-rj"

	items, err := tool.Resolve(context.Background(), rawURL, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1 directlink item", len(items))
	}
	if items[0].Category != "directlink" {
		t.Errorf("category = %q, want directlink", items[0].Category)
	}
	host := strings.TrimPrefix(srv.URL, "http://")
	if got := kwdict.String(items[0].Meta, "domain"); got != host {
		t.Errorf("domain = %q, want the file host %q", got, host)
	}
	if got := kwdict.String(items[0].Meta, "filename"); got != "AIdro_avatar=s400-c-k-no-rj" {
		t.Errorf("filename = %q, want the last path segment", got)
	}
}

func TestLiveDownloadDirectlinkFallback(t *testing.T) {
	tool := liveTool(t)
	body := []byte("\xff\xd8\xffYTAVATARBYTES")
	srv := mediaServer(t, body)
	rawURL := srv.URL + "/ytc/AIdro_avatar=s400-c-k-no-rj"
	workDir := t.TempDir()

	var streamed []Downloaded
	dls, err := tool.Download(context.Background(), rawURL, "", workDir, false, func(_ int, d Downloaded) {
		streamed = append(streamed, d)
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(dls) != 1 {
		t.Fatalf("downloaded = %d, want 1", len(dls))
	}
	if got := kwdict.String(dls[0].Meta, "category"); got != "directlink" {
		t.Errorf("category = %q, want directlink", got)
	}
	// The served type is jpeg, so the file lands with a real .jpg extension even
	// though the URL had none.
	if !strings.HasSuffix(dls[0].Path, ".jpg") {
		t.Errorf("path = %q, want a .jpg extension from the served content type", dls[0].Path)
	}
	got, err := os.ReadFile(dls[0].Path)
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("downloaded bytes = %q, want the served body", got)
	}
	if len(streamed) != 1 {
		t.Errorf("onFile called %d times, want 1 (the item must advance as the file lands)", len(streamed))
	}
}
