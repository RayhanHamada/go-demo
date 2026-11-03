// Package logger builds the application logger and carries request-scoped
// values (logger, request id) through context.Context.
package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Format names a supported output encoding.
type Format string

const (
	// FormatJSON emits one JSON object per line (production default).
	FormatJSON Format = "json"
	// FormatConsole emits human-readable colourised lines (local development).
	FormatConsole Format = "console"
)

// UnmarshalText implements encoding.TextUnmarshaler so the format can be read
// straight from the environment.
func (f *Format) UnmarshalText(text []byte) error {
	switch Format(strings.ToLower(strings.TrimSpace(string(text)))) {
	case FormatJSON, "":
		*f = FormatJSON
	case FormatConsole:
		*f = FormatConsole
	default:
		return fmt.Errorf("unknown log format %q (want %q or %q)", text, FormatJSON, FormatConsole)
	}
	return nil
}

// MarshalJSON keeps the value readable when config is logged.
func (f Format) MarshalJSON() ([]byte, error) { return json.Marshal(string(f)) }

// New builds the root logger at level (trace, debug, info, warn, error, ...)
// writing to out in the requested format. Unrecognised levels fall back to
// info. The result is also installed as zerolog's global logger.
func New(level string, format Format, out io.Writer) zerolog.Logger {
	if out == nil {
		out = os.Stdout
	}
	lvl, err := zerolog.ParseLevel(level)
	if err != nil || lvl == zerolog.NoLevel {
		lvl = zerolog.InfoLevel
	}
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.SetGlobalLevel(lvl)

	w := out
	if format == FormatConsole {
		w = zerolog.ConsoleWriter{Out: out, TimeFormat: time.RFC3339}
	}
	log := zerolog.New(w).Level(lvl).With().Timestamp().Logger()
	zerolog.DefaultContextLogger = &log
	return log
}

// NewRequestID mints a correlation identifier for one inbound request.
func NewRequestID() string { return uuid.NewString() }

// FromContext returns the request-scoped logger stored by WithLogger, or base
// when ctx carries none (background jobs, tests, startup). It deliberately
// ignores zerolog's process-wide DefaultContextLogger so one request can never
// inherit another's logger.
func FromContext(ctx context.Context, base zerolog.Logger) zerolog.Logger {
	if l, ok := ctx.Value(loggerKey).(*zerolog.Logger); ok && l != nil {
		return *l
	}
	return base
}

// WithLogger stores l in ctx, typically once per inbound request.
func WithLogger(ctx context.Context, l zerolog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, &l)
}

// contextKey is private so no other package can collide with these values.
type contextKey int

const (
	requestIDKey contextKey = iota
	loggerKey
)

// WithRequestID stores id in ctx so every layer can correlate its log lines.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the identifier stored by WithRequestID, or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}
