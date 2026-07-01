package monbooru

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPairRequestAndStatus(t *testing.T) {
	var gotApp, gotPeer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/pair/request":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotApp, _ = body["app"].(string)
			gotPeer, _ = body["peer_token"].(string)
			_, _ = w.Write([]byte(`{"request_id":"abc","status":"pending"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/pair/status":
			_, _ = w.Write([]byte(`{"status":"approved","token":"mb-secret"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := testClient(srv.URL, "")

	id, err := c.PairRequest(context.Background(), "ml-secret", "http://ml:8081", []string{"read", "write"})
	if err != nil || id != "abc" {
		t.Fatalf("PairRequest: id=%q err=%v", id, err)
	}
	if gotApp != "monloader" || gotPeer != "ml-secret" {
		t.Errorf("request body: app=%q peer=%q", gotApp, gotPeer)
	}
	status, token, err := c.PairStatus(context.Background(), "abc")
	if err != nil || status != "approved" || token != "mb-secret" {
		t.Fatalf("PairStatus: status=%q token=%q err=%v", status, token, err)
	}
}
