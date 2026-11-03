package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"gin-clean-arch-demo/internal/logger"
)

func TestNewHonoursLevelAndFormat(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New("warn", logger.FormatJSON, &buf)

	log.Debug().Msg("hidden")
	log.Info().Msg("hidden")
	log.Warn().Msg("visible")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1 at warn level (%q)", len(lines), buf.String())
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if entry["level"] != "warn" || entry["message"] != "visible" {
		t.Errorf("entry = %v", entry)
	}
	if _, ok := entry["time"]; !ok {
		t.Error("timestamp field missing")
	}
}

func TestNewFallsBackToInfoForUnknownLevel(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New("loud", logger.FormatJSON, &buf)
	log.Info().Msg("kept")
	if !strings.Contains(buf.String(), "kept") {
		t.Error("info line suppressed; unknown level should fall back to info")
	}
}

func TestNewConsoleFormat(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New("info", logger.FormatConsole, &buf)
	log.Info().Str("k", "v").Msg("console line")

	out := buf.String()
	if !strings.Contains(out, "console line") || json.Valid([]byte(strings.TrimSpace(out))) {
		t.Errorf("console output not human readable: %q", out)
	}
}

func TestContextLoggerAndRequestID(t *testing.T) {
	var buf bytes.Buffer
	base := logger.New("debug", logger.FormatJSON, &buf)

	ctx := logger.WithRequestID(context.Background(), "req-1")
	if got := logger.RequestID(ctx); got != "req-1" {
		t.Errorf("RequestID = %q, want req-1", got)
	}
	if got := logger.RequestID(context.Background()); got != "" {
		t.Errorf("RequestID on bare ctx = %q, want empty", got)
	}

	scoped := base.With().Str("request_id", logger.RequestID(ctx)).Logger()
	ctx = logger.WithLogger(ctx, scoped)

	l := logger.FromContext(ctx, base)
	l.Info().Msg("scoped")
	if !strings.Contains(buf.String(), `"request_id":"req-1"`) {
		t.Errorf("context logger did not carry the request id: %s", buf.String())
	}
}

func TestFromContextFallsBackToBase(t *testing.T) {
	var buf bytes.Buffer
	base := logger.New("info", logger.FormatJSON, &buf)

	l := logger.FromContext(context.Background(), base)
	l.Info().Msg("fallback")
	if !strings.Contains(buf.String(), "fallback") {
		t.Error("base logger not used for a context without a scoped logger")
	}
}

// A disabled context logger must not swallow records: zerolog.Ctx returns a
// disabled logger, and FromContext has to detect that and use base instead.
func TestFromContextIgnoresDisabledLogger(t *testing.T) {
	var buf bytes.Buffer
	base := zerolog.New(&buf).Level(zerolog.InfoLevel)

	l := logger.FromContext(context.Background(), base)
	l.Info().Msg("survived")
	if !strings.Contains(buf.String(), "survived") {
		t.Error("logging through a nil context logger lost the record")
	}
}
