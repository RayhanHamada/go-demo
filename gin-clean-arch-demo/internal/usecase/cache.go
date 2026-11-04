package usecase

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog"
)

// cacheRead loads and decodes a JSON value. It reports false on a miss, a
// disabled cache or any cache failure, so callers fall through to the
// database; a corrupt entry is evicted instead of served.
func cacheRead[T any](ctx context.Context, cache Cache, ttl time.Duration, log zerolog.Logger, key string) (T, bool) {
	var zero T
	if cache == nil || ttl <= 0 {
		return zero, false
	}
	raw, ok, err := cache.Get(ctx, key)
	if err != nil {
		log.Warn().Err(err).Str("cache_key", key).Msg("cache read failed; falling back to database")
		return zero, false
	}
	if !ok {
		return zero, false
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		log.Warn().Err(err).Str("cache_key", key).Msg("cache entry unreadable; evicting")
		deleteCacheKeys(ctx, cache, log, key)
		return zero, false
	}
	return value, true
}

// cacheWrite stores a JSON value, logging (never returning) failures: the
// cache is an optimisation and must not fail a request.
func cacheWrite[T any](ctx context.Context, cache Cache, ttl time.Duration, log zerolog.Logger, key string, value T) {
	if cache == nil || ttl <= 0 {
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		log.Error().Err(err).Str("cache_key", key).Msg("cache encode failed")
		return
	}
	if err := cache.Set(ctx, key, raw, ttl); err != nil {
		log.Warn().Err(err).Str("cache_key", key).Msg("cache write failed")
	}
}

// deleteCacheKeys evicts entries, logging (never returning) failures.
func deleteCacheKeys(ctx context.Context, cache Cache, log zerolog.Logger, keys ...string) {
	if cache == nil || len(keys) == 0 {
		return
	}
	if err := cache.Delete(ctx, keys...); err != nil {
		log.Warn().Err(err).Strs("cache_keys", keys).Msg("cache delete failed")
	}
}
