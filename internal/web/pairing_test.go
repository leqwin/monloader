package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leqwin/monloader/internal/config"
)

func TestMonbooruPairConnectFlow(t *testing.T) {
	approved := false
	var gotPeerToken, gotURL string
	mb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pair/request":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotPeerToken, _ = body["peer_token"].(string)
			gotURL, _ = body["url"].(string)
			_, _ = w.Write([]byte(`{"request_id":"r1","status":"pending"}`))
		case "/api/v1/pair/status":
			if approved {
				_, _ = w.Write([]byte(`{"status":"approved","token":"mb-issued"}`))
			} else {
				_, _ = w.Write([]byte(`{"status":"pending"}`))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mb.Close()

	srv := newWebServer(t, mb.URL, "")

	srv.monbooruPairConnect(httptest.NewRecorder(), httptest.NewRequest("POST", "/settings/monbooru/pair/connect", nil))
	if srv.getPairAttempt() == nil {
		t.Fatal("no pairing attempt after connect")
	}
	if gotPeerToken == "" || gotURL == "" {
		t.Errorf("request omitted peer_token/url: token=%q url=%q", gotPeerToken, gotURL)
	}

	// Poll while monbooru is still deciding: nothing committed.
	srv.monbooruPairPoll(httptest.NewRecorder(), httptest.NewRequest("POST", "/settings/monbooru/pair/poll", nil))
	if srv.hasPairedToken("monbooru") {
		t.Fatal("paired before approval")
	}

	// Operator approves in monbooru; the next poll commits both directions.
	approved = true
	cr := httptest.NewRecorder()
	srv.monbooruPairPoll(cr, httptest.NewRequest("POST", "/settings/monbooru/pair/poll", nil))
	if !srv.hasPairedToken("monbooru") || srv.cfg.Current().Monbooru.APIToken != "mb-issued" {
		t.Fatalf("not connected: paired=%v token=%q", srv.hasPairedToken("monbooru"), srv.cfg.Current().Monbooru.APIToken)
	}
	if !strings.Contains(cr.Body.String(), `id="auth-tokens"`) || !strings.Contains(cr.Body.String(), "hx-swap-oob") {
		t.Errorf("connecting should OOB-refresh the token list, got %q", cr.Body.String())
	}
	// The token monloader offered authenticates monbooru's calls back.
	if srv.cfg.Current().FindTokenByHash(config.HashToken(gotPeerToken)) == nil {
		t.Error("offered peer token not stored as a monloader token")
	}

	// Remove tears down both directions.
	srv.monbooruPairRemove(httptest.NewRecorder(), httptest.NewRequest("POST", "/settings/monbooru/pair/remove", nil))
	if srv.hasPairedToken("monbooru") || srv.cfg.Current().Monbooru.APIToken != "" {
		t.Error("remove did not tear down the pairing")
	}
}

func TestTakePairAttempt(t *testing.T) {
	srv := newWebServer(t, "http://mb", "")
	att := &outboundPair{requestID: "r"}
	srv.setPairAttempt(att)
	if !srv.takePairAttempt(att) {
		t.Fatal("first take should win")
	}
	if srv.takePairAttempt(att) {
		t.Error("a second take of the same attempt must lose, so no double-commit")
	}
	if srv.getPairAttempt() != nil {
		t.Error("attempt should be cleared after a take")
	}
}

func TestMonbooruPairRemoveWarnsWhenUnreachable(t *testing.T) {
	srv := newWebServer(t, "http://ml", "") // monbooru URL that never resolves
	tok, _ := config.GenerateToken("monbooru (paired)", config.AllScopes)
	tok.Paired = "monbooru"
	if err := srv.updateConfig(func(c *config.Config) error {
		c.Auth.Tokens = append(c.Auth.Tokens, tok)
		c.Monbooru.APIToken = "peer-secret"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	srv.monbooruPairRemove(w, httptest.NewRequest("POST", "/settings/monbooru/pair/remove", nil))

	if srv.hasPairedToken("monbooru") {
		t.Error("local pairing not removed")
	}
	if !strings.Contains(w.Body.String(), "could not reach monbooru") {
		t.Errorf("unreachable peer should warn the operator, got %q", w.Body.String())
	}
}

// pairSelfURL advertises the bind port (where monbooru reaches monloader on the
// LAN), not base_url, which can carry a stale port when only the bind address
// was changed.
func TestPairSelfURLUsesBindPort(t *testing.T) {
	cfg := config.Default()
	cfg.Server.BindAddress = "0.0.0.0:18081"
	cfg.Server.BaseURL = "http://localhost:8081"
	if got, want := pairSelfURL(cfg), "http://localhost:18081"; got != want {
		t.Errorf("pairSelfURL = %q, want %q", got, want)
	}
}
