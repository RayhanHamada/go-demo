package config_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"gin-clean-arch-demo/internal/config"
	"gin-clean-arch-demo/internal/logger"
)

func TestParseDefaults(t *testing.T) {
	// t.Setenv in the subtests isolates the process environment.
	cfg, err := config.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.Database.Path != "data/app.db" || cfg.Database.MaxOpenConns != 4 {
		t.Errorf("database defaults = %+v", cfg.Database)
	}
	if cfg.Database.BusyTimeout != 5*time.Second {
		t.Errorf("BusyTimeout = %v, want 5s", cfg.Database.BusyTimeout)
	}
	if cfg.Cache.Addr != "127.0.0.1:6379" || cfg.Cache.TTL != 5*time.Minute {
		t.Errorf("cache defaults = %+v", cfg.Cache)
	}
	if cfg.LogLevel != "info" || cfg.LogFormat != logger.FormatJSON {
		t.Errorf("log defaults = %q/%q", cfg.LogLevel, cfg.LogFormat)
	}
}

func TestParseOverrides(t *testing.T) {
	t.Setenv("HTTP_ADDR", "127.0.0.1:9000")
	t.Setenv("SHUTDOWN_TIMEOUT", "3s")
	t.Setenv("DATABASE_PATH", "/tmp/x.db")
	t.Setenv("DATABASE_MAX_CONNS", "9")
	t.Setenv("DATABASE_BUSY_TIMEOUT", "750ms")
	t.Setenv("DATABASE_LOG_SQL", "true")
	t.Setenv("CACHE_ADDR", "cache:6380")
	t.Setenv("CACHE_DB", "2")
	t.Setenv("CACHE_TTL", "90s")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "console")

	cfg, err := config.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9000" || cfg.ShutdownTimeout != 3*time.Second {
		t.Errorf("http = %q/%v", cfg.HTTPAddr, cfg.ShutdownTimeout)
	}
	if cfg.Database.Path != "/tmp/x.db" || cfg.Database.MaxOpenConns != 9 ||
		cfg.Database.BusyTimeout != 750*time.Millisecond || !cfg.Database.LogSQL {
		t.Errorf("database = %+v", cfg.Database)
	}
	if cfg.Cache.Addr != "cache:6380" || cfg.Cache.DB != 2 || cfg.Cache.TTL != 90*time.Second {
		t.Errorf("cache = %+v", cfg.Cache)
	}
	if cfg.LogLevel != "debug" || cfg.LogFormat != logger.FormatConsole {
		t.Errorf("log = %q/%q", cfg.LogLevel, cfg.LogFormat)
	}
}

func TestParseRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name string
		key  string
		val  string
		want string
	}{
		{"unparsable duration", "CACHE_TTL", "soon", "CACHE_TTL"},
		{"unparsable int", "DATABASE_MAX_CONNS", "many", "DATABASE_MAX_CONNS"},
		{"unparsable bool", "DATABASE_LOG_SQL", "yes please", "DATABASE_LOG_SQL"},
		{"unknown log format", "LOG_FORMAT", "xml", "log format"},
		{"zero max conns", "DATABASE_MAX_CONNS", "0", "DATABASE_MAX_CONNS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.key, tc.val)
			_, err := config.Parse()
			if err == nil {
				t.Fatalf("expected error for %s=%q", tc.key, tc.val)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// Every invalid variable should be reported in one error, not one at a time.
func TestParseAggregatesErrors(t *testing.T) {
	t.Setenv("CACHE_TTL", "soon")
	t.Setenv("DATABASE_MAX_CONNS", "many")

	_, err := config.Parse()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"CACHE_TTL", "DATABASE_MAX_CONNS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %s", err, want)
		}
	}
}

// unsetAfterTest removes variables that Parse picked up from a .env file:
// godotenv writes straight to the process environment, so tests must undo it.
func unsetAfterTest(t *testing.T, keys ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, key := range keys {
			os.Unsetenv(key)
		}
	})
}

func writeEnvFile(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(config.EnvFile, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", config.EnvFile, err)
	}
}

func TestParseReadsEnvFile(t *testing.T) {
	t.Chdir(t.TempDir())
	unsetAfterTest(t, "HTTP_ADDR", "CACHE_TTL", "LOG_LEVEL")
	writeEnvFile(t, `
# comment
HTTP_ADDR=127.0.0.1:7777
CACHE_TTL=45s
LOG_LEVEL=warn
`)

	cfg, err := config.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:7777" {
		t.Errorf("HTTPAddr = %q, want 127.0.0.1:7777", cfg.HTTPAddr)
	}
	if cfg.Cache.TTL != 45*time.Second {
		t.Errorf("Cache.TTL = %v, want 45s", cfg.Cache.TTL)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want warn", cfg.LogLevel)
	}
}

// The process environment must win: that is what makes an exported variable
// (or a container's environment) authoritative over a leftover file.
func TestParseProcessEnvBeatsEnvFile(t *testing.T) {
	t.Chdir(t.TempDir())
	unsetAfterTest(t, "CACHE_TTL")
	writeEnvFile(t, "CACHE_TTL=45s\n")
	t.Setenv("CACHE_TTL", "90s")

	cfg, err := config.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Cache.TTL != 90*time.Second {
		t.Errorf("Cache.TTL = %v, want the environment's 90s", cfg.Cache.TTL)
	}
}

// A missing .env is normal outside development and must not fail startup.
func TestParseWithoutEnvFile(t *testing.T) {
	t.Chdir(t.TempDir())

	if _, err := config.Parse(); err != nil {
		t.Fatalf("parse without .env: %v", err)
	}
}

// A broken .env is a real mistake and should be reported, not ignored.
func TestParseMalformedEnvFile(t *testing.T) {
	t.Chdir(t.TempDir())
	writeEnvFile(t, "this line has no equals sign\n")

	_, err := config.Parse()
	if err == nil {
		t.Fatal("expected an error for a malformed .env")
	}
	if !strings.Contains(err.Error(), config.EnvFile) {
		t.Errorf("error = %v, want it to name %s", err, config.EnvFile)
	}
}
