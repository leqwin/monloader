// Package logx is a thin level gate over the stdlib log package.
//
// Three levels: "warn" (default), "info", "debug". Warnings, errors, and
// fatals always fire; only Infof and Debugf respect the gate. Copied from
// monbooru so the companion logs the same way.
package logx

import (
	"log"
	"strings"
	"sync/atomic"
)

// Level orders verbosity low-to-high (smaller = more verbose) to match
// the slog / syslog convention; Enabled fires only when the message's
// level is at or above the configured threshold.
type Level int32

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
)

var level atomic.Int32

// Set parses a name ("warn", "info", "debug"; anything else becomes "warn")
// and installs it as the current threshold.
func Set(name string) {
	var l Level
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "info":
		l = LevelInfo
	case "debug":
		l = LevelDebug
	default:
		l = LevelWarn
	}
	level.Store(int32(l))
}

// Enabled reports whether messages at l clear the configured threshold.
func Enabled(l Level) bool { return l >= Level(level.Load()) }

func Debugf(format string, a ...any) {
	if Enabled(LevelDebug) {
		log.Printf("DEBUG "+format, a...)
	}
}

func Infof(format string, a ...any) {
	if Enabled(LevelInfo) {
		log.Printf("INFO "+format, a...)
	}
}

func Warnf(format string, a ...any)  { log.Printf("WARN "+format, a...) }
func Errorf(format string, a ...any) { log.Printf("ERROR "+format, a...) }
