package gdl

import (
	"strings"

	"github.com/leqwin/monloader/internal/queue"
)

// classifyError maps a non-zero gallery-dl exit plus its stderr to a stable
// error code. gallery-dl's numeric exit codes shift across
// releases, so the classification keys on stderr substrings first and treats
// any other non-zero exit as a generic download failure.
func classifyError(exitCode int, stderr string) *queue.CodedError {
	low := strings.ToLower(stderr)
	switch {
	case containsAny(low, "no suitable extractor", "unsupported url", "no extractor found"):
		return &queue.CodedError{Code: queue.ErrCodeUnsupportedURL, Msg: stderr}
	// A bot-protection wall (Cloudflare, a captcha challenge) returns 403 but is
	// not a missing credential, so classify it before the auth rule below - else
	// the "403 forbidden" match would mislabel it auth_required.
	case containsAny(low, "cloudflare", "challengeerror", "ddos-guard", "captcha"):
		return &queue.CodedError{Code: queue.ErrCodeBlocked, Msg: stderr}
	case containsAny(low, "missing authentication", "authentication", "authorization", "authrequired", "login required", "http 401", "401 unauthorized", "http 403", "403 forbidden"):
		return &queue.CodedError{Code: queue.ErrCodeAuthRequired, Msg: stderr}
	case containsAny(low, "http 429", "429 too many requests", "rate limit", "too many requests"):
		return &queue.CodedError{Code: queue.ErrCodeRateLimited, Msg: stderr}
	default:
		msg := stderr
		if msg == "" {
			msg = "gallery-dl exited with a non-zero status"
		}
		return &queue.CodedError{Code: queue.ErrCodeDownloadFailed, Msg: msg}
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
