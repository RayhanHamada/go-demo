package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gin-clean-arch-demo/internal/adapter/sqlite"
	"gin-clean-arch-demo/internal/domain"
)

func newStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), sqlite.Config{
		Path: filepath.Join(t.TempDir(), "test.db"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

func mustEmail(t *testing.T, s string) domain.Email {
	t.Helper()
	e, err := domain.ParseEmail(s)
	if err != nil {
		t.Fatalf("parse email: %v", err)
	}
	return e
}

func mustName(t *testing.T, s string) domain.DisplayName {
	t.Helper()
	n, err := domain.ParseDisplayName(s)
	if err != nil {
		t.Fatalf("parse name: %v", err)
	}
	return n
}

func TestUserRepositoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := sqlite.NewUserRepository(newStore(t))
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	created, err := repo.Create(ctx, domain.NewUser(mustEmail(t, "Ada@Example.com"), mustName(t, "Ada Lovelace"), now))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("create returned empty id")
	}

	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Email != "ada@example.com" {
		t.Errorf("email = %q, want normalised lowercase", got.Email)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("created_at = %v, want %v", got.CreatedAt, now)
	}
	if got.Status != domain.StatusActive {
		t.Errorf("status = %q, want %q", got.Status, domain.StatusActive)
	}

	renamed := got.Rename(mustName(t, "Ada L."), now.Add(time.Hour))
	updated, err := repo.Update(ctx, renamed)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.DisplayName != "Ada L." {
		t.Errorf("display_name = %q, want %q", updated.DisplayName, "Ada L.")
	}
	if !updated.UpdatedAt.Equal(now.Add(time.Hour)) {
		t.Errorf("updated_at = %v, want %v", updated.UpdatedAt, now.Add(time.Hour))
	}

	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get after delete: err = %v, want ErrNotFound", err)
	}
}

func TestUserRepositoryDuplicateEmail(t *testing.T) {
	ctx := context.Background()
	repo := sqlite.NewUserRepository(newStore(t))
	now := time.Now().UTC()

	if _, err := repo.Create(ctx, domain.NewUser(mustEmail(t, "a@example.com"), mustName(t, "A"), now)); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := repo.Create(ctx, domain.NewUser(mustEmail(t, "A@example.com"), mustName(t, "A again"), now))
	if !errors.Is(err, domain.ErrEmailTaken) {
		t.Fatalf("second create: err = %v, want ErrEmailTaken", err)
	}
}

func TestUserRepositoryMissingRows(t *testing.T) {
	ctx := context.Background()
	repo := sqlite.NewUserRepository(newStore(t))
	id := domain.NewUserID()

	if _, err := repo.GetByID(ctx, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get: err = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("delete: err = %v, want ErrNotFound", err)
	}
	u := domain.NewUser(mustEmail(t, "b@example.com"), mustName(t, "B"), time.Now().UTC())
	if _, err := repo.Update(ctx, u); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("update: err = %v, want ErrNotFound", err)
	}
}

func TestUserRepositoryList(t *testing.T) {
	ctx := context.Background()
	repo := sqlite.NewUserRepository(newStore(t))
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	for i, email := range []string{"c@example.com", "d@example.com", "e@example.com"} {
		u := domain.NewUser(mustEmail(t, email), mustName(t, "user"), base.Add(time.Duration(i)*time.Minute))
		if email == "d@example.com" {
			u = u.ChangeStatus(domain.StatusSuspended, u.UpdatedAt)
		}
		if _, err := repo.Create(ctx, u); err != nil {
			t.Fatalf("create %s: %v", email, err)
		}
	}

	page, err := repo.List(ctx, domain.ListUsersQuery{Limit: 2})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Total != 3 {
		t.Errorf("total = %d, want 3", page.Total)
	}
	if len(page.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(page.Items))
	}
	if page.Items[0].Email != "e@example.com" {
		t.Errorf("first item = %q, want newest email", page.Items[0].Email)
	}

	suspended := domain.StatusSuspended
	filtered, err := repo.List(ctx, domain.ListUsersQuery{Status: &suspended})
	if err != nil {
		t.Fatalf("list filtered: %v", err)
	}
	if filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].Status != domain.StatusSuspended {
		t.Errorf("filtered = %+v, want exactly the suspended user", filtered)
	}

	if _, err := repo.List(ctx, domain.ListUsersQuery{Limit: domain.MaxPageLimit + 1}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("oversized limit: err = %v, want ErrInvalid", err)
	}
}

// An in-memory database exists inside a single connection. Pooling more than
// one connection surfaces an empty database ("no such table") as soon as two
// queries run concurrently.
func TestMemoryDatabaseSharedAcrossConnections(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, sqlite.Config{Path: ":memory:", MaxOpenConns: 4})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo := sqlite.NewUserRepository(store)

	now := time.Now().UTC()
	first, err := repo.Create(ctx, domain.NewUser(mustEmail(t, "m1@example.com"), mustName(t, "M1"), now))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			if _, err := repo.GetByID(ctx, first.ID); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent get on pooled connection: %v", err)
	}

	page, err := repo.List(ctx, domain.ListUsersQuery{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Total != 1 {
		t.Errorf("total = %d, want 1", page.Total)
	}
}

// The schema is GORM-owned, so it must be reproducible from the models alone:
// indexes and the CHECK constraint have to exist after migration, and a
// second migration must be a no-op.
func TestSchemaMatchesModels(t *testing.T) {
	store := newStore(t)

	var ddl string
	if err := store.DB().Raw(`SELECT COALESCE(group_concat(sql, ';'), '') FROM sqlite_master
		WHERE tbl_name = 'users' AND sql IS NOT NULL`).Scan(&ddl).Error; err != nil {
		t.Fatalf("read schema: %v", err)
	}
	for _, want := range []string{
		"CONSTRAINT `ck_users_status` CHECK (status IN ('active','suspended'))",
		"CREATE UNIQUE INDEX `ux_users_email`",
		"CREATE INDEX `ix_users_created_at`",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("schema missing %q\nschema: %s", want, ddl)
		}
	}

	// Re-running the migrator against an up-to-date database must succeed.
	if err := store.DB().AutoMigrate(sqlite.Models()...); err != nil {
		t.Fatalf("second migration: %v", err)
	}
}

// The status CHECK constraint is the database-level guard behind the domain
// enum: writing an unknown status through GORM must fail even if a caller
// bypasses validation.
func TestDatabaseRejectsUnknownStatus(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	err := store.DB().WithContext(ctx).Exec(
		`INSERT INTO users (id, email, display_name, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"bad", "bad@example.com", "Bad", "zombie", time.Now().UTC(), time.Now().UTC(),
	).Error
	if err == nil {
		t.Fatal("insert with status 'zombie' succeeded; CHECK constraint missing")
	}
	if !strings.Contains(err.Error(), "ck_users_status") {
		t.Errorf("error = %v, want ck_users_status violation", err)
	}
}

// Timestamps are application-owned: the model must not let GORM overwrite the
// values the domain decided on.
func TestTimestampsAreNotOverwritten(t *testing.T) {
	ctx := context.Background()
	repo := sqlite.NewUserRepository(newStore(t))
	stamp := time.Date(2020, 3, 4, 5, 6, 7, 123456789, time.UTC)

	created, err := repo.Create(ctx, domain.NewUser(mustEmail(t, "ts@example.com"), mustName(t, "Ts"), stamp))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !created.CreatedAt.Equal(stamp) {
		t.Errorf("created_at = %v, want %v", created.CreatedAt, stamp)
	}

	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.CreatedAt.Equal(stamp) {
		t.Errorf("round-tripped created_at = %v, want %v", got.CreatedAt, stamp)
	}
	// Sub-second precision must survive the round trip.
	if got.CreatedAt.Nanosecond() != stamp.Nanosecond() {
		t.Errorf("nanoseconds lost: got %d, want %d", got.CreatedAt.Nanosecond(), stamp.Nanosecond())
	}
}
