package monbooru

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/leqwin/monloader/internal/config"
	"github.com/leqwin/monloader/internal/queue"
)

// PushMeta is the mapped metadata sent alongside a file.
type PushMeta struct {
	Filename        string
	Tags            []string
	Source          string
	URL             string
	Collection      string
	CollectionOrder int
	Via             string
	Folder          string
}

// Result is a successful push outcome.
type Result struct {
	Outcome     queue.Outcome
	MonbooruID  int64
	SHA256      string
	TagWarnings []string
}

// Gallery is one entry from GET /api/v1/galleries.
type Gallery struct {
	Name   string `json:"name"`
	Images int    `json:"images"`
	Tags   int    `json:"tags"`
	Active bool   `json:"active"`
}

// Client talks to a monbooru instance over its REST API. It reads the API URL
// and token from the config provider on each request, so a settings save takes
// effect without a restart and without racing the worker goroutine.
type Client struct {
	cfg  *config.Provider
	http *http.Client
}

// New builds a client from the config provider. Request lifetime is bounded by
// the caller's context; a generous backstop timeout guards against a wedged
// connection.
func New(cfg *config.Provider) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: 10 * time.Minute}}
}

func (c *Client) base() string {
	return strings.TrimRight(c.cfg.Current().Monbooru.APIURL, "/")
}

func (c *Client) authHeader(req *http.Request) {
	if tok := c.cfg.Current().Monbooru.APIToken; tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

// PushImage uploads file bytes plus mapped metadata to
// POST /api/v1/images?gallery=<name>, computing the sha256 the item carries and
// sending a buffered multipart body.
func (c *Client) PushImage(ctx context.Context, data []byte, meta PushMeta, gallery string) (*Result, error) {
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	body, contentType, err := buildMultipart(data, meta)
	if err != nil {
		return nil, &queue.CodedError{Code: queue.ErrCodeMappingFailed, Msg: err.Error()}
	}
	return c.sendPush(ctx, c.imagesEndpoint(gallery), contentType, body, sha)
}

// PushImageFile streams a file (a built .cbz) plus mapped metadata to the same
// endpoint, hashing it in a streaming pass and writing the multipart body
// through an io.Pipe so a large archive is never buffered whole in memory.
func (c *Client) PushImageFile(ctx context.Context, path string, meta PushMeta, gallery string) (*Result, error) {
	sha, err := fileSHA256(path)
	if err != nil {
		return nil, &queue.CodedError{Code: queue.ErrCodeMappingFailed, Msg: err.Error()}
	}
	pr, pw := io.Pipe()
	w := multipart.NewWriter(pw)
	contentType := w.FormDataContentType()
	go func() { pw.CloseWithError(streamFileMultipart(w, path, meta)) }()
	return c.sendPush(ctx, c.imagesEndpoint(gallery), contentType, pr, sha)
}

// imagesEndpoint is the push URL with the optional gallery selector.
func (c *Client) imagesEndpoint(gallery string) string {
	endpoint := c.base() + "/api/v1/images"
	if gallery != "" {
		endpoint += "?gallery=" + url.QueryEscape(gallery)
	}
	return endpoint
}

// sendPush posts a prepared multipart body and classifies the response: 201 ->
// created, 200 (alias) -> duplicate, 413 -> file_too_large, a missing response
// -> monbooru_unreachable (canceled if the job was aborted), any other 4xx/5xx
// -> monbooru_rejected. The success body is polymorphic (a bare image or an
// envelope); the id and any tag warnings are read from whichever arrives.
func (c *Client) sendPush(ctx context.Context, endpoint, contentType string, body io.Reader, sha string) (*Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return nil, &queue.CodedError{Code: queue.ErrCodeMonbooruRejected, Msg: err.Error()}
	}
	req.Header.Set("Content-Type", contentType)
	c.authHeader(req)

	resp, err := c.http.Do(req)
	if err != nil {
		// A canceled job aborts the in-flight request; report it as canceled
		// rather than the generic unreachable classification, which would look
		// to the future extension like a real connectivity failure.
		if ctx.Err() != nil {
			return nil, &queue.CodedError{Code: queue.ErrCodeCanceled, Msg: ctx.Err().Error()}
		}
		// No HTTP response at all: connection refused, DNS failure, timeout.
		return nil, &queue.CodedError{Code: queue.ErrCodeMonbooruUnreachable, Msg: err.Error()}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusCreated:
		id, warnings := parseImageResult(respBody)
		return &Result{Outcome: queue.OutcomeCreated, MonbooruID: id, SHA256: sha, TagWarnings: warnings}, nil
	case http.StatusOK:
		id, warnings := parseImageResult(respBody)
		return &Result{Outcome: queue.OutcomeDuplicate, MonbooruID: id, SHA256: sha, TagWarnings: warnings}, nil
	case http.StatusRequestEntityTooLarge:
		return nil, &queue.CodedError{Code: queue.ErrCodeFileTooLarge, Msg: apiErrMessage(respBody, resp.Status)}
	default:
		return nil, &queue.CodedError{Code: queue.ErrCodeMonbooruRejected, Msg: apiErrMessage(respBody, resp.Status)}
	}
}

// fileSHA256 streams a file through sha256 and returns the hex digest.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// buildMultipart assembles a buffered POST /api/v1/images body from bytes.
func buildMultipart(data []byte, meta PushMeta) (*bytes.Buffer, string, error) {
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	if err := writeFilePart(w, meta.Filename, bytes.NewReader(data)); err != nil {
		return nil, "", err
	}
	if err := writeMetaFields(w, meta); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf, w.FormDataContentType(), nil
}

// streamFileMultipart writes the multipart body for a file push into w (backed
// by an io.Pipe): the file part streamed from disk, then the metadata fields.
func streamFileMultipart(w *multipart.Writer, path string, meta PushMeta) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := writeFilePart(w, meta.Filename, f); err != nil {
		return err
	}
	if err := writeMetaFields(w, meta); err != nil {
		return err
	}
	return w.Close()
}

// writeFilePart writes the "file" part, streaming from src.
func writeFilePart(w *multipart.Writer, filename string, src io.Reader) error {
	if filename == "" {
		filename = "upload"
	}
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, src)
	return err
}

// writeMetaFields writes the non-file form fields, omitting empty optionals so a
// bare push touches nothing on monbooru's side.
func writeMetaFields(w *multipart.Writer, meta PushMeta) error {
	if len(meta.Tags) > 0 {
		tagsJSON, err := json.Marshal(meta.Tags)
		if err != nil {
			return err
		}
		_ = w.WriteField("tags", string(tagsJSON))
	}
	if meta.Via != "" {
		_ = w.WriteField("via", meta.Via)
	}
	if meta.Folder != "" {
		_ = w.WriteField("folder", meta.Folder)
	}
	if meta.Source != "" {
		_ = w.WriteField("source", meta.Source)
	}
	if meta.URL != "" {
		_ = w.WriteField("url", meta.URL)
	}
	if meta.Collection != "" {
		_ = w.WriteField("collection", meta.Collection)
	}
	if meta.CollectionOrder > 0 {
		_ = w.WriteField("collection_order", strconv.Itoa(meta.CollectionOrder))
	}
	return nil
}

// parseImageResult reads the monbooru id and tag warnings from the polymorphic
// create/duplicate body: a bare image object, or an envelope keyed by "image".
func parseImageResult(data []byte) (id int64, warnings []string) {
	var env map[string]json.RawMessage
	if err := json.Unmarshal(data, &env); err != nil {
		return 0, nil
	}
	imgRaw, enveloped := env["image"]
	if enveloped {
		if w, ok := env["tag_warnings"]; ok {
			_ = json.Unmarshal(w, &warnings)
		}
	} else {
		imgRaw = data
	}
	var img struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(imgRaw, &img)
	return img.ID, warnings
}

// apiErrMessage pulls monbooru's {error,code} message out of an error body,
// falling back to the HTTP status line.
func apiErrMessage(body []byte, status string) string {
	var e struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return e.Error
	}
	return status
}

// ListGalleries reads GET /api/v1/galleries for the settings dropdown.
func (c *Client) ListGalleries(ctx context.Context) ([]Gallery, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/api/v1/galleries", nil)
	if err != nil {
		return nil, err
	}
	c.authHeader(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &queue.CodedError{Code: queue.ErrCodeMonbooruUnreachable, Msg: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, &queue.CodedError{Code: queue.ErrCodeMonbooruRejected, Msg: apiErrMessage(body, resp.Status)}
	}
	var galleries []Gallery
	if err := json.NewDecoder(resp.Body).Decode(&galleries); err != nil {
		return nil, fmt.Errorf("decoding galleries: %w", err)
	}
	return galleries, nil
}

// TestConnection hits GET /api/v1/ to verify the configured URL and token,
// backing the settings "test connection" button.
func (c *Client) TestConnection(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/api/v1/", nil)
	if err != nil {
		return err
	}
	c.authHeader(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return &queue.CodedError{Code: queue.ErrCodeMonbooruUnreachable, Msg: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return &queue.CodedError{Code: queue.ErrCodeMonbooruRejected, Msg: apiErrMessage(body, resp.Status)}
	}
	return nil
}
