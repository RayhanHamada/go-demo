package usecase_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/rs/zerolog"

	"gin-clean-arch-demo/internal/adapter/cache"
	"gin-clean-arch-demo/internal/adapter/sqlite"
	"gin-clean-arch-demo/internal/domain"
	"gin-clean-arch-demo/internal/usecase"
)

func newService(t *testing.T) (*usecase.UserService, *countingRepo) {
	t.Helper()
	ctx := context.Background()

	store, err := sqlite.Open(ctx, sqlite.Config{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	redis := miniredis.RunT(t)
	redisStore, err := cache.New(ctx, cache.Config{Addr: redis.Addr()})
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	t.Cleanup(func() { _ = redisStore.Close() })

	repo := &countingRepo{UserRepository: sqlite.NewUserRepository(store), gets: 0}
	return usecase.NewUserService(repo, redisStore, zerolog.Nop(), time.Minute), repo
}

// countingRepo counts read-through misses so cache behaviour is observable.
type countingRepo struct {
	usecase.UserRepository
	gets int
}

func (r *countingRepo) GetByID(ctx context.Context, id domain.UserID) (domain.User, error) {
	r.gets++
	return r.UserRepository.GetByID(ctx, id)
}

func TestGetUserCachesAfterMiss(t *testing.T) {
	ctx := context.Background()
	svc, repo := newService(t)

	created, err := svc.CreateUser(ctx, usecase.CreateUserInput{Email: "ada@example.com", DisplayName: "Ada"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	first, err := svc.GetUser(ctx, created.ID.String())
	if err != nil {
		t.Fatalf("first get: %v", err)
	}
	second, err := svc.GetUser(ctx, created.ID.String())
	if err != nil {
		t.Fatalf("second get: %v", err)
	}
	if repo.gets != 1 {
		t.Errorf("repository hits = %d, want 1 (second read served from cache)", repo.gets)
	}
	if first != second {
		t.Errorf("cached view differs: %+v vs %+v", first, second)
	}
}

func TestUpdateUserRefreshesCache(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	created, err := svc.CreateUser(ctx, usecase.CreateUserInput{Email: "grace@example.com", DisplayName: "Grace"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.GetUser(ctx, created.ID.String()); err != nil {
		t.Fatalf("prime cache: %v", err)
	}

	name, status := "Grace Hopper", string(domain.StatusSuspended)
	updated, err := svc.UpdateUser(ctx, created.ID.String(), usecase.UpdateUserInput{DisplayName: &name, Status: &status})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.DisplayName != "Grace Hopper" || updated.Status != domain.StatusSuspended {
		t.Fatalf("update result = %+v", updated)
	}

	read, err := svc.GetUser(ctx, created.ID.String())
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if read.DisplayName != "Grace Hopper" || read.Status != domain.StatusSuspended {
		t.Errorf("cached read = %+v, want the updated values", read)
	}
}

func TestDeleteUserEvictsCache(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	created, err := svc.CreateUser(ctx, usecase.CreateUserInput{Email: "alan@example.com", DisplayName: "Alan"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.GetUser(ctx, created.ID.String()); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	if err := svc.DeleteUser(ctx, created.ID.String()); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetUser(ctx, created.ID.String()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get after delete: err = %v, want ErrNotFound", err)
	}
}

func TestValidationRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	cases := []struct {
		name string
		in   usecase.CreateUserInput
	}{
		{"empty email", usecase.CreateUserInput{Email: "", DisplayName: "x"}},
		{"malformed email", usecase.CreateUserInput{Email: "not-an-email", DisplayName: "x"}},
		{"empty name", usecase.CreateUserInput{Email: "x@example.com", DisplayName: "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.CreateUser(ctx, tc.in); !errors.Is(err, domain.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}

	if _, err := svc.GetUser(ctx, "not-a-uuid"); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("bad id: err = %v, want ErrInvalid", err)
	}
}

func TestCreateUserRejectsDuplicateEmail(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	if _, err := svc.CreateUser(ctx, usecase.CreateUserInput{Email: "dup@example.com", DisplayName: "One"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := svc.CreateUser(ctx, usecase.CreateUserInput{Email: "DUP@example.com", DisplayName: "Two"})
	if !errors.Is(err, domain.ErrEmailTaken) {
		t.Fatalf("err = %v, want ErrEmailTaken", err)
	}
}
