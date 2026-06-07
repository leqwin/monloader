package monbooru

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/leqwin/monloader/internal/config"
	"github.com/leqwin/monloader/internal/queue"
)

func testClient(url, token string) *Client {
	cfg := config.Default()
	cfg.Monbooru.APIURL = url
	cfg.Monbooru.APIToken = token
	return New(config.NewProvider(cfg))
}

func TestPushImageCreatedBareBody(t *testing.T) {
	data := []byte("the image bytes")
	var gotGallery, gotAuth, gotVia, gotSource, gotURL, gotCollection, gotOrder, gotTags, gotFilename string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotGallery = r.URL.Query().Get("gallery")
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		gotVia = r.FormValue("via")
		gotSource = r.FormValue("source")
		gotURL = r.FormValue("url")
		gotCollection = r.FormValue("collection")
		gotOrder = r.FormValue("collection_order")
		gotTags = r.FormValue("tags")
		f, fh, err := r.FormFile("file")
		if err != nil {
			t.Errorf("FormFile: %v", err)
		} else {
			gotFilename = fh.Filename
			b, _ := io.ReadAll(f)
			if string(b) != string(data) {
				t.Errorf("file bytes = %q, want %q", b, data)
			}
		}
		// Bare image object (no notes): the success body has no envelope.
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 123, "sha256": "x"})
	}))
	defer srv.Close()

	meta := PushMeta{
		Filename:        "post.jpg",
		Tags:            []string{"1girl", "artist:x", "rating:general"},
		Source:          "danbooru",
		URL:             "https://example.com/posts/1",
		Collection:      "pool name",
		CollectionOrder: 2,
		Via:             "monloader",
	}
	res, err := testClient(srv.URL, "tok").PushImage(context.Background(), data, meta, "mygallery")
	if err != nil {
		t.Fatalf("PushImage: %v", err)
	}
	if res.Outcome != queue.OutcomeCreated {
		t.Errorf("outcome = %s, want created", res.Outcome)
	}
	if res.MonbooruID != 123 {
		t.Errorf("id = %d, want 123", res.MonbooruID)
	}
	sum := sha256.Sum256(data)
	if res.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256 mismatch")
	}
	if gotGallery != "mygallery" {
		t.Errorf("gallery query = %q, want mygallery", gotGallery)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("auth = %q, want Bearer tok", gotAuth)
	}
	if gotVia != "monloader" || gotSource != "danbooru" || gotURL != "https://example.com/posts/1" {
		t.Errorf("provenance fields wrong: via=%q source=%q url=%q", gotVia, gotSource, gotURL)
	}
	if gotCollection != "pool name" || gotOrder != "2" {
		t.Errorf("collection fields wrong: %q / %q", gotCollection, gotOrder)
	}
	if gotFilename != "post.jpg" {
		t.Errorf("filename = %q, want post.jpg", gotFilename)
	}
	var tags []string
	if err := json.Unmarshal([]byte(gotTags), &tags); err != nil || len(tags) != 3 {
		t.Errorf("tags field = %q, want a 3-element JSON array", gotTags)
	}
}

func TestPushImageFileStreams(t *testing.T) {
	data := []byte("the cbz archive bytes")
	path := filepath.Join(t.TempDir(), "bundle.cbz")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	var gotFilename, gotCollection string
	var gotFile []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		gotCollection = r.FormValue("collection")
		f, fh, err := r.FormFile("file")
		if err != nil {
			t.Errorf("FormFile: %v", err)
		} else {
			gotFilename = fh.Filename
			gotFile, _ = io.ReadAll(f)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 55})
	}))
	defer srv.Close()

	meta := PushMeta{Filename: "My Doujin.cbz", Tags: []string{"rating:explicit"}, Source: "nhentai", Collection: "vol1"}
	res, err := testClient(srv.URL, "tok").PushImageFile(context.Background(), path, meta, "g")
	if err != nil {
		t.Fatalf("PushImageFile: %v", err)
	}
	if res.Outcome != queue.OutcomeCreated || res.MonbooruID != 55 {
		t.Errorf("outcome/id = %s/%d, want created/55", res.Outcome, res.MonbooruID)
	}
	sum := sha256.Sum256(data)
	if res.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256 = %q, want the file's digest", res.SHA256)
	}
	if string(gotFile) != string(data) {
		t.Errorf("streamed file = %q, want %q", gotFile, data)
	}
	if gotFilename != "My Doujin.cbz" {
		t.Errorf("filename = %q, want My Doujin.cbz", gotFilename)
	}
	if gotCollection != "vol1" {
		t.Errorf("collection field = %q, want it streamed alongside the file", gotCollection)
	}
}

func TestPushImageCreatedEnvelopedWithWarnings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"image":        map[string]any{"id": 124},
			"tag_warnings": []string{"tag foo: rejected"},
		})
	}))
	defer srv.Close()
	res, err := testClient(srv.URL, "tok").PushImage(context.Background(), []byte("x"), PushMeta{Via: "monloader"}, "")
	if err != nil {
		t.Fatalf("PushImage: %v", err)
	}
	if res.Outcome != queue.OutcomeCreated || res.MonbooruID != 124 {
		t.Errorf("got %+v, want created id 124", res)
	}
	if len(res.TagWarnings) != 1 || res.TagWarnings[0] != "tag foo: rejected" {
		t.Errorf("tag warnings = %v", res.TagWarnings)
	}
}

func TestPushImageDuplicate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// monbooru returns 200 + alias_added on a known sha256.
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"image":       map[string]any{"id": 55},
			"alias_added": true,
		})
	}))
	defer srv.Close()
	res, err := testClient(srv.URL, "tok").PushImage(context.Background(), []byte("x"), PushMeta{}, "furry")
	if err != nil {
		t.Fatalf("PushImage: %v", err)
	}
	if res.Outcome != queue.OutcomeDuplicate || res.MonbooruID != 55 {
		t.Errorf("got %+v, want duplicate id 55", res)
	}
}

func TestPushImageErrorClassification(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusRequestEntityTooLarge, queue.ErrCodeFileTooLarge},
		{http.StatusInternalServerError, queue.ErrCodeMonbooruRejected},
		{http.StatusBadRequest, queue.ErrCodeMonbooruRejected},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "nope", "code": "x"})
		}))
		_, err := testClient(srv.URL, "tok").PushImage(context.Background(), []byte("x"), PushMeta{}, "")
		srv.Close()
		e, ok := err.(*queue.CodedError)
		if !ok || e.Code != tc.want {
			t.Errorf("status %d -> %v, want code %s", tc.status, err, tc.want)
		}
	}
}

func TestPushImageUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now
	_, err := testClient(url, "tok").PushImage(context.Background(), []byte("x"), PushMeta{}, "")
	e, ok := err.(*queue.CodedError)
	if !ok || e.Code != queue.ErrCodeMonbooruUnreachable {
		t.Errorf("got %v, want monbooru_unreachable", err)
	}
}

func TestPushImageCanceled(t *testing.T) {
	// A job canceled while the push is in flight must be recorded as canceled,
	// not monbooru_unreachable - the context error wins over Do's wrapped error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled before the request goes out
	_, err := testClient(srv.URL, "tok").PushImage(ctx, []byte("x"), PushMeta{Filename: "x.jpg"}, "g")
	e, ok := err.(*queue.CodedError)
	if !ok || e.Code != queue.ErrCodeCanceled {
		t.Errorf("got %v, want canceled", err)
	}
}

func TestListGalleries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/galleries" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]Gallery{
			{Name: "default", Images: 10, Tags: 5, Active: true},
			{Name: "furry", Images: 2, Tags: 1},
		})
	}))
	defer srv.Close()
	gs, err := testClient(srv.URL, "tok").ListGalleries(context.Background())
	if err != nil {
		t.Fatalf("ListGalleries: %v", err)
	}
	if len(gs) != 2 || gs[0].Name != "default" || !gs[0].Active || gs[1].Name != "furry" {
		t.Errorf("galleries = %+v", gs)
	}
}

func TestConnection(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"api": "monbooru"})
	}))
	defer ok.Close()
	if err := testClient(ok.URL, "tok").TestConnection(context.Background()); err != nil {
		t.Errorf("TestConnection on a healthy server: %v", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer bad.Close()
	if err := testClient(bad.URL, "wrong").TestConnection(context.Background()); err == nil {
		t.Error("TestConnection should fail on 401")
	}
}

// TestOpenAPIContract pins the monbooru fields ingest depends on against the
// recorded openapi.json so an upstream rename trips here instead of silently
// breaking the push.
func TestOpenAPIContract(t *testing.T) {
	path := filepath.Join("..", "testdata", "monbooru", "openapi.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read openapi fixture: %v", err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("decode openapi: %v", err)
	}
	requireProps := map[string][]string{
		"Image":                  {"id", "sha256", "source", "url", "collection", "collection_order", "tags"},
		"CreateImageResponse":    {"image", "tag_warnings"},
		"DuplicateImageResponse": {"image", "alias_added"},
		"Gallery":                {"name", "images", "tags", "active"},
	}
	for schema, props := range requireProps {
		s, ok := spec.Components.Schemas[schema]
		if !ok {
			t.Errorf("openapi schema %q missing (monbooru contract drift)", schema)
			continue
		}
		for _, p := range props {
			if _, ok := s.Properties[p]; !ok {
				t.Errorf("openapi %s.%s missing - the client depends on this field", schema, p)
			}
		}
	}
	for _, p := range []string{"/images", "/galleries"} {
		if _, ok := spec.Paths[p]; !ok {
			t.Errorf("openapi path %q missing", p)
		}
	}
}
