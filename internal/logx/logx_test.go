package logx

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// Levels are ordered low-to-high (Debug < Info < Warn) so Set("info")
// enables Info and Warn but not Debug, matching the slog convention.
func TestEnabledMatchesThreshold(t *testing.T) {
	cases := []struct {
		threshold string
		expect    map[Level]bool
	}{
		{
			threshold: "debug",
			expect: map[Level]bool{
				LevelDebug: true,
				LevelInfo:  true,
				LevelWarn:  true,
			},
		},
		{
			threshold: "info",
			expect: map[Level]bool{
				LevelDebug: false,
				LevelInfo:  true,
				LevelWarn:  true,
			},
		},
		{
			threshold: "warn",
			expect: map[Level]bool{
				LevelDebug: false,
				LevelInfo:  false,
				LevelWarn:  true,
			},
		},
		{
			// An unrecognised name falls back to warn.
			threshold: "bogus",
			expect: map[Level]bool{
				LevelDebug: false,
				LevelInfo:  false,
				LevelWarn:  true,
			},
		},
	}

	for _, tc := range cases {
		Set(tc.threshold)
		for l, want := range tc.expect {
			if got := Enabled(l); got != want {
				t.Errorf("threshold=%q level=%d: Enabled = %v, want %v", tc.threshold, l, got, want)
			}
		}
	}
}

// captureOutput redirects the stdlib logger to a buffer for the duration
// of fn and returns what was written.
func captureOutput(fn func()) string {
	var buf bytes.Buffer
	saved := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(saved)
	fn()
	return buf.String()
}

func TestWarnLevelSuppressesInfoAndDebug(t *testing.T) {
	Set("warn")
	out := captureOutput(func() {
		Debugf("dbg-%d", 1)
		Infof("inf-%d", 2)
		Warnf("wrn-%d", 3)
		Errorf("err-%d", 4)
	})
	if strings.Contains(out, "dbg-1") {
		t.Errorf("warn level should suppress Debugf, got: %q", out)
	}
	if strings.Contains(out, "inf-2") {
		t.Errorf("warn level should suppress Infof, got: %q", out)
	}
	if !strings.Contains(out, "WARN wrn-3") {
		t.Errorf("Warnf should always fire, got: %q", out)
	}
	if !strings.Contains(out, "ERROR err-4") {
		t.Errorf("Errorf should always fire, got: %q", out)
	}
}

func TestDebugLevelEmitsEverything(t *testing.T) {
	Set("debug")
	out := captureOutput(func() {
		Debugf("dbg-%d", 1)
		Infof("inf-%d", 2)
		Warnf("wrn-%d", 3)
	})
	for _, want := range []string{"DEBUG dbg-1", "INFO inf-2", "WARN wrn-3"} {
		if !strings.Contains(out, want) {
			t.Errorf("debug level should emit %q, got: %q", want, out)
		}
	}
}
