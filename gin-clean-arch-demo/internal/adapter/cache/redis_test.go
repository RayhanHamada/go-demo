package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"gin-clean-arch-demo/internal/adapter/cache"
	"gin-clean-arch-demo/internal/usecase"
)

// The adapter must satisfy the port the service consumes.
var _ usecase.Cache = (*cache.RedisCache)(nil)

func TestRedisCacheSemantics(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)

	c, err := cache.New(ctx, cache.Config{Addr: server.Addr()})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if _, ok, err := c.Get(ctx, "missing"); err != nil || ok {
		t.Fatalf("get missing: ok = %v, err = %v; want false, nil", ok, err)
	}

	if err := c.Set(ctx, "k", []byte(`{"a":1}`), time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
	raw, ok, err := c.Get(ctx, "k")
	if err != nil || !ok {
		t.Fatalf("get: ok = %v, err = %v", ok, err)
	}
	if string(raw) != `{"a":1}` {
		t.Errorf("value = %q", raw)
	}
	if ttl := server.TTL("k"); ttl <= 0 || ttl > time.Minute {
		t.Errorf("ttl = %v, want (0, 1m]", ttl)
	}

	if err := c.Delete(ctx, "k"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, err := c.Get(ctx, "k"); err != nil || ok {
		t.Errorf("get after delete: ok = %v, err = %v; want false, nil", ok, err)
	}
	if err := c.Delete(ctx); err != nil {
		t.Errorf("delete with no keys: %v", err)
	}
}

func TestNewRejectsUnreachableServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	server := miniredis.RunT(t)
	addr := server.Addr()
	server.Close()

	if _, err := cache.New(ctx, cache.Config{Addr: addr}); err == nil {
		t.Fatal("expected dial/ping failure against closed server")
	}
}

func TestNewRequiresAddr(t *testing.T) {
	if _, err := cache.New(context.Background(), cache.Config{}); err == nil {
		t.Fatal("expected error for empty Addr")
	}
}
