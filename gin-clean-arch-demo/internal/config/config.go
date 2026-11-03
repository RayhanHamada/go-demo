// Package config loads runtime configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"

	"gin-clean-arch-demo/internal/logger"
)

// Config is the fully resolved application configuration. Every field is
// populated from the environment by Parse.
type Config struct {
	HTTPAddr        string        `env:"HTTP_ADDR" envDefault:":8080"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`
	LogLevel        string        `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat       logger.Format `env:"LOG_FORMAT" envDefault:"json"`
	Database        Database      `envPrefix:"DATABASE_"`
	Cache           Cache         `envPrefix:"CACHE_"`
}

// Database configures the SQLite store.
type Database struct {
	// Path is the database file, or ":memory:" for an ephemeral database.
	Path         string        `env:"PATH" envDefault:"data/app.db"`
	MaxOpenConns int           `env:"MAX_CONNS" envDefault:"4"`
	BusyTimeout  time.Duration `env:"BUSY_TIMEOUT" envDefault:"5s"`
	// LogSQL logs every statement at debug level, plus slow queries at warn.
	LogSQL bool `env:"LOG_SQL" envDefault:"false"`
}

// Cache configures the Valkey/Redis cache.
type Cache struct {
	Addr     string        `env:"ADDR" envDefault:"127.0.0.1:6379"`
	Password string        `env:"PASSWORD"`
	DB       int           `env:"DB" envDefault:"0"`
	TTL      time.Duration `env:"TTL" envDefault:"5m"`
	Timeout  time.Duration `env:"TIMEOUT" envDefault:"2s"`
}

// EnvFile is the file consulted by Parse when it exists. It is a development
// convenience: variables already present in the process environment always
// win, so container and CI settings are never overridden by a stray file.
const EnvFile = ".env"

// Parse reads .env (if present) and then the environment into a Config. Every
// invalid variable is reported at once, named as the operator set it rather
// than as the Go field.
func Parse() (Config, error) {
	// A missing .env is the normal production case, so it is not an error;
	// a malformed one is.
	if err := godotenv.Load(EnvFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("config: read %s: %w", EnvFile, err)
	}
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, explain(err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// explain rewrites the env library's aggregate error so each message names the
// environment variable (CACHE_TTL) instead of the struct field (TTL).
func explain(err error) error {
	var agg env.AggregateError
	if !errors.As(err, &agg) {
		return fmt.Errorf("config: %w", err)
	}
	names := envVarNames()
	msgs := make([]string, 0, len(agg.Errors))
	for _, item := range agg.Errors {
		var parseErr env.ParseError
		if errors.As(item, &parseErr) {
			name := names[parseErr.Name]
			if name == "" {
				name = parseErr.Name
			}
			msgs = append(msgs, fmt.Sprintf("%s: %v", name, parseErr.Err))
			continue
		}
		msgs = append(msgs, item.Error())
	}
	return fmt.Errorf("config: %s", strings.Join(msgs, "; "))
}

// envVarNames maps Go struct field names to the environment variables they are
// bound to, following env and envPrefix tags.
func envVarNames() map[string]string {
	names := make(map[string]string)
	var walk func(t reflect.Type, prefix string)
	walk = func(t reflect.Type, prefix string) {
		for i := range t.NumField() {
			field := t.Field(i)
			fieldType := field.Type
			if fieldType.Kind() == reflect.Pointer {
				fieldType = fieldType.Elem()
			}
			if fieldType.Kind() == reflect.Struct && !hasEnvTag(field) {
				walk(fieldType, prefix+field.Tag.Get("envPrefix"))
				continue
			}
			if !hasEnvTag(field) {
				continue
			}
			if _, exists := names[field.Name]; !exists {
				names[field.Name] = prefix + field.Tag.Get("env")
			}
		}
	}
	walk(reflect.TypeOf(Config{}), "")
	return names
}

func hasEnvTag(field reflect.StructField) bool { return field.Tag.Get("env") != "" }

func (c Config) validate() error {
	var errs []error
	if c.HTTPAddr == "" {
		errs = append(errs, errors.New("HTTP_ADDR must not be empty"))
	}
	if c.Database.Path == "" {
		errs = append(errs, errors.New("DATABASE_PATH must not be empty"))
	}
	if c.Database.MaxOpenConns < 1 {
		errs = append(errs, errors.New("DATABASE_MAX_CONNS must be at least 1"))
	}
	if c.Cache.Addr == "" {
		errs = append(errs, errors.New("CACHE_ADDR must not be empty"))
	}
	if c.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("SHUTDOWN_TIMEOUT must be positive"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return nil
}
