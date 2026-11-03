// Package sqlite implements the usecase persistence port on SQLite through
// GORM. The package owns the database schema: the model in model.go is the
// single source of truth and is applied by GORM's migrator.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/rs/zerolog"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Config describes where and how to open the database.
type Config struct {
	// Path is the database file, or ":memory:" for an ephemeral database.
	Path string
	// MaxOpenConns bounds concurrent connections. SQLite serialises writers,
	// so a small pool is correct and avoids lock contention.
	MaxOpenConns int
	// BusyTimeout is how long a statement waits on a locked database.
	BusyTimeout time.Duration
	// LogSQL logs statements at debug level and slow queries at warn.
	LogSQL bool
	// Logger receives connection and migration events.
	Logger zerolog.Logger
}

// Store owns the GORM handle and the connection pool behind it.
type Store struct {
	db  *gorm.DB
	sql *sql.DB
	log zerolog.Logger
}

// Open opens the database, applies connection pragmas and migrates the schema.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("sqlite: Path is required")
	}
	if cfg.MaxOpenConns < 1 {
		cfg.MaxOpenConns = 4
	}
	timeout := cfg.BusyTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	log := cfg.Logger.With().Str("layer", "sqlite").Logger()

	db, err := gorm.Open(sqlite.Open(dsnFor(cfg.Path, timeout)), &gorm.Config{
		TranslateError:         true,
		SkipDefaultTransaction: true,
		Logger:                 queryLogger(log, cfg.LogSQL),
	})
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %s: %w", cfg.Path, err)
	}

	pool, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("sqlite: connection pool: %w", err)
	}
	// An in-memory database lives inside its connection, so a pool larger than
	// one hands out separate empty databases. Pin it for the memory forms.
	conns := cfg.MaxOpenConns
	if isMemoryPath(cfg.Path) {
		conns = 1
	}
	pool.SetMaxOpenConns(conns)
	pool.SetMaxIdleConns(conns)
	pool.SetConnMaxIdleTime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := pool.PingContext(pingCtx); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("sqlite: ping %s: %w", cfg.Path, err)
	}

	s := &Store{db: db, sql: pool, log: log}
	if err := s.migrate(ctx); err != nil {
		_ = pool.Close()
		return nil, err
	}
	log.Info().Str("path", cfg.Path).Int("max_conns", conns).Msg("database ready")
	return s, nil
}

// dsnFor builds a modernc/go-sqlite DSN with the pragmas this application
// relies on. WAL keeps readers from blocking the writer; IMMEDIATE transactions
// avoid upgrade deadlocks.
func dsnFor(path string, timeout time.Duration) string {
	q := url.Values{}
	q.Set("_txlock", "immediate")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "busy_timeout("+strconv.FormatInt(timeout.Milliseconds(), 10)+")")

	if strings.HasPrefix(path, "file:") {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		return path + sep + q.Encode()
	}
	return "file:" + path + "?" + q.Encode()
}

// isMemoryPath reports whether path names an in-memory database.
func isMemoryPath(path string) bool {
	return path == ":memory:" ||
		strings.HasPrefix(path, "file::memory:") ||
		strings.Contains(path, "mode=memory")
}

// migrate brings the schema up to date with the models. GORM's migrator is
// idempotent: it creates missing tables, columns, indexes and constraints.
func (s *Store) migrate(ctx context.Context) error {
	if err := s.db.WithContext(ctx).AutoMigrate(Models()...); err != nil {
		s.log.Error().Err(err).Msg("schema migration failed")
		return fmt.Errorf("sqlite: migrate: %w", err)
	}
	s.log.Debug().Int("models", len(Models())).Msg("schema up to date")
	return nil
}

// Close releases the connection pool.
func (s *Store) Close() error { return s.sql.Close() }

// DB exposes the GORM handle for tests and future repositories.
func (s *Store) DB() *gorm.DB { return s.db }

// Ping reports database reachability.
func (s *Store) Ping(ctx context.Context) error { return s.sql.PingContext(ctx) }

// queryLogger adapts GORM's logger interface to zerolog. Errors and slow
// queries always surface; every statement is logged at debug when verbose.
func queryLogger(log zerolog.Logger, verbose bool) gormlogger.Interface {
	return &gormLog{log: log, verbose: verbose, slow: 200 * time.Millisecond}
}

type gormLog struct {
	log     zerolog.Logger
	verbose bool
	slow    time.Duration
}

func (l *gormLog) LogMode(gormlogger.LogLevel) gormlogger.Interface { return l }

func (l *gormLog) Info(ctx context.Context, msg string, args ...any) {
	l.log.Info().Str("event", fmt.Sprintf(msg, args...)).Msg("gorm")
}

func (l *gormLog) Warn(ctx context.Context, msg string, args ...any) {
	l.log.Warn().Str("event", fmt.Sprintf(msg, args...)).Msg("gorm")
}

func (l *gormLog) Error(ctx context.Context, msg string, args ...any) {
	l.log.Error().Str("event", fmt.Sprintf(msg, args...)).Msg("gorm")
}

// isNotFound reports GORM's not-found sentinel, which is an expected outcome
// for reads and must not be logged as a query failure.
func isNotFound(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }

func (l *gormLog) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	elapsed := time.Since(begin)
	sql, rows := fc()

	event := l.log.With().
		Str("sql", sql).
		Int64("rows", rows).
		Float64("duration_ms", float64(elapsed.Microseconds())/1000).
		Logger()

	switch {
	case err != nil && !isNotFound(err):
		event.Error().Err(err).Msg("query failed")
	case elapsed > l.slow:
		event.Warn().Dur("threshold", l.slow).Msg("slow query")
	case l.verbose:
		event.Debug().Msg("query")
	}
}
