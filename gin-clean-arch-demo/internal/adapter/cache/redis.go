// Package cache holds the Valkey (Redis-compatible) implementation of the
// usecase.Cache port.
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// RedisCache is a usecase.Cache backed by a Redis-compatible server such as
// Valkey. It is safe for concurrent use.
type RedisCache struct {
	client *redis.Client
	log    zerolog.Logger
}

// Config describes how to reach the cache server.
type Config struct {
	Addr     string
	Password string
	DB       int
	Timeout  time.Duration
	// Logger receives connection events. Zero value logs nothing.
	Logger zerolog.Logger
}

// New dials the cache server and verifies it answers a PING.
func New(ctx context.Context, cfg Config) (*RedisCache, error) {
	if cfg.Addr == "" {
		return nil, errors.New("cache: Addr is required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  timeout,
		ReadTimeout:  timeout,
		WriteTimeout: timeout,
		PoolSize:     16,
	})
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	log := cfg.Logger.With().Str("layer", "cache").Str("addr", cfg.Addr).Logger()
	redis.SetLogger(redisLog{log: log})
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		log.Error().Err(err).Msg("cache connection failed")
		return nil, fmt.Errorf("cache: ping %s: %w", cfg.Addr, err)
	}
	log.Info().Int("db", cfg.DB).Msg("cache connected")
	return &RedisCache{client: client, log: log}, nil
}

// Get implements usecase.Cache. A missing key is (nil, false, nil).
func (c *RedisCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	raw, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		c.log.Warn().Err(err).Str("key", key).Msg("cache get failed")
		return nil, false, fmt.Errorf("cache: get %q: %w", key, err)
	}
	return raw, true, nil
}

// Set implements usecase.Cache.
func (c *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := c.client.Set(ctx, key, value, ttl).Err(); err != nil {
		c.log.Warn().Err(err).Str("key", key).Dur("ttl", ttl).Msg("cache set failed")
		return fmt.Errorf("cache: set %q: %w", key, err)
	}
	c.log.Debug().Str("key", key).Dur("ttl", ttl).Int("bytes", len(value)).Msg("cache entry stored")
	return nil
}

// Delete implements usecase.Cache.
func (c *RedisCache) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := c.client.Del(ctx, keys...).Err(); err != nil {
		c.log.Warn().Err(err).Strs("keys", keys).Msg("cache delete failed")
		return fmt.Errorf("cache: delete %v: %w", keys, err)
	}
	c.log.Debug().Strs("keys", keys).Msg("cache entries evicted")
	return nil
}

// Ping reports cache server reachability.
func (c *RedisCache) Ping(ctx context.Context) error { return c.client.Ping(ctx).Err() }

// redisLog routes go-redis' internal stdlib logging into zerolog so pool
// warnings share the application's structured output.
type redisLog struct{ log zerolog.Logger }

func (l redisLog) Printf(ctx context.Context, format string, v ...any) {
	l.log.Warn().Str("event", fmt.Sprintf(format, v...)).Msg("redis")
}

// Close releases the connection pool.
func (c *RedisCache) Close() error { return c.client.Close() }
