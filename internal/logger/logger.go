// Package logger provides structured logging via log/slog.
//
// Two handlers are wired at package init based on ENVIRONMENT: JSON for
// production (machine-parseable, stitches cleanly with Caddy's JSON access
// log) and Text for development (human-friendly in terminal). Both include
// source=file:line so a log line points at the code that emitted it.
//
// Log level honours the LOG_LEVEL env var (debug/info/warn/error), default
// info. No dynamic reconfiguration — bump the var and restart.
//
// Usage:
//
//	log := logger.New("recorder")
//	log.Info("polling", "feed", "vehicles", "interval", "15s")
//	log.Error("fetch failed", "err", err)
//
// In request handlers, prefer logger.FromContext(r.Context()) so downstream
// logs inherit the request ID set by the RequestID middleware:
//
//	log := logger.FromContext(ctx)
//	log.Warn("db query slow", "duration", d)
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// Logger is an alias for slog.Logger — kept as a local name so callers can
// depend on `*logger.Logger` without importing log/slog themselves.
type Logger = slog.Logger

type ctxKey struct{}

func init() {
	opts := &slog.HandlerOptions{
		Level:     parseLevel(os.Getenv("LOG_LEVEL")),
		AddSource: true,
	}
	var h slog.Handler
	if os.Getenv("ENVIRONMENT") == "production" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}

// New returns a logger tagged with a component name.
func New(component string) *Logger {
	return slog.Default().With("component", component)
}

// FromContext returns the request-scoped logger from ctx, or the default
// logger if none is attached. Safe to call in any code path; falls back
// gracefully outside of HTTP handlers.
func FromContext(ctx context.Context) *Logger {
	if l, ok := ctx.Value(ctxKey{}).(*Logger); ok {
		return l
	}
	return slog.Default()
}

// Into returns a new context carrying the given logger. Used by the
// RequestID middleware to hand a logger with a per-request id tag down
// through the handler chain.
func Into(ctx context.Context, l *Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
