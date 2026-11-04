// Package usecase contains the application services and the ports they depend
// on. It knows nothing about HTTP, SQL or Redis.
package usecase

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"gin-clean-arch-demo/internal/domain"
	"gin-clean-arch-demo/internal/logger"
)

// UserRepository is the persistence port for users. Implementations translate
// driver errors into the domain sentinels (domain.ErrNotFound,
// domain.ErrEmailTaken).
type UserRepository interface {
	Create(ctx context.Context, u domain.User) (domain.User, error)
	GetByID(ctx context.Context, id domain.UserID) (domain.User, error)
	Update(ctx context.Context, u domain.User) (domain.User, error)
	Delete(ctx context.Context, id domain.UserID) error
	List(ctx context.Context, q domain.ListUsersQuery) (domain.Page[domain.User], error)
}

// Cache is the key/value port used for read-through caching. ok reports a
// hit; a miss is not an error.
type Cache interface {
	Get(ctx context.Context, key string) (value []byte, ok bool, err error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
}

// UserView is the application-level projection of a domain.User: the shape
// returned to callers and the shape persisted in the cache.
type UserView struct {
	ID          domain.UserID      `json:"id"`
	Email       domain.Email       `json:"email"`
	DisplayName domain.DisplayName `json:"display_name"`
	Status      domain.UserStatus  `json:"status"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

// NewUserView projects a domain user.
func NewUserView(u domain.User) UserView {
	return UserView{
		ID:          u.ID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Status:      u.Status,
		CreatedAt:   u.CreatedAt,
		UpdatedAt:   u.UpdatedAt,
	}
}

// CreateUserInput carries raw caller input, validated by CreateUser.
type CreateUserInput struct {
	Email       string
	DisplayName string
}

// UpdateUserInput carries a partial update; nil fields are left unchanged.
type UpdateUserInput struct {
	DisplayName *string
	Status      *string
}

// UserService is the user application service.
type UserService struct {
	repo  UserRepository
	cache Cache
	log   zerolog.Logger
	ttl   time.Duration
	now   func() time.Time
}

// UserServiceOption customises a UserService.
type UserServiceOption func(*UserService)

// WithClock replaces the time source (tests).
func WithClock(now func() time.Time) UserServiceOption {
	return func(s *UserService) { s.now = now }
}

// NewUserService wires the service. ttl is the cached entry lifetime; a
// non-positive ttl disables caching.
func NewUserService(repo UserRepository, cache Cache, log zerolog.Logger, ttl time.Duration, opts ...UserServiceOption) *UserService {
	s := &UserService{repo: repo, cache: cache, log: log, ttl: ttl, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CreateUser validates input, persists a new account and returns its view.
func (s *UserService) CreateUser(ctx context.Context, in CreateUserInput) (UserView, error) {
	log := s.loggerFor(ctx, "create_user")

	email, err := domain.ParseEmail(in.Email)
	if err != nil {
		log.Warn().Err(err).Msg("user creation rejected: invalid email")
		return UserView{}, err
	}
	name, err := domain.ParseDisplayName(in.DisplayName)
	if err != nil {
		log.Warn().Err(err).Msg("user creation rejected: invalid display name")
		return UserView{}, err
	}
	u, err := s.repo.Create(ctx, domain.NewUser(email, name, s.now().UTC()))
	if err != nil {
		log.Error().Err(err).Str("email", email.String()).Msg("user creation failed")
		return UserView{}, err
	}
	log.Info().Str("user_id", u.ID.String()).Msg("user created")
	return NewUserView(u), nil
}

// GetUser returns a user, served from cache when possible.
func (s *UserService) GetUser(ctx context.Context, rawID string) (UserView, error) {
	log := s.loggerFor(ctx, "get_user")

	id, err := domain.ParseUserID(rawID)
	if err != nil {
		log.Warn().Err(err).Str("raw_id", rawID).Msg("user lookup rejected: invalid id")
		return UserView{}, err
	}
	log = log.With().Str("user_id", id.String()).Logger()

	key := cacheKey(id)
	if view, ok := cacheRead[UserView](ctx, s.cache, s.ttl, log, key); ok {
		log.Debug().Msg("user served from cache")
		return view, nil
	}
	u, err := s.repo.GetByID(ctx, id)
	if err != nil {
		log.Warn().Err(err).Msg("user lookup failed")
		return UserView{}, err
	}
	view := NewUserView(u)
	cacheWrite(ctx, s.cache, s.ttl, log, key, view)
	log.Debug().Msg("user loaded from database")
	return view, nil
}

// ListUsers returns one page of users filtered by q.
func (s *UserService) ListUsers(ctx context.Context, q domain.ListUsersQuery) (domain.Page[UserView], error) {
	log := s.loggerFor(ctx, "list_users")

	if err := q.Normalize(); err != nil {
		log.Warn().Err(err).Msg("user listing rejected: invalid query")
		return domain.Page[UserView]{}, err
	}
	page, err := s.repo.List(ctx, q)
	if err != nil {
		log.Error().Err(err).Msg("user listing failed")
		return domain.Page[UserView]{}, err
	}
	views := make([]UserView, len(page.Items))
	for i, u := range page.Items {
		views[i] = NewUserView(u)
	}
	log.Debug().Int("returned", len(views)).Int64("total", page.Total).
		Int("limit", page.Limit).Int("offset", page.Offset).Msg("users listed")
	return domain.Page[UserView]{
		Items:  views,
		Total:  page.Total,
		Limit:  page.Limit,
		Offset: page.Offset,
	}, nil
}

// UpdateUser applies the supplied fields to an existing user.
func (s *UserService) UpdateUser(ctx context.Context, rawID string, in UpdateUserInput) (UserView, error) {
	log := s.loggerFor(ctx, "update_user")

	id, err := domain.ParseUserID(rawID)
	if err != nil {
		log.Warn().Err(err).Str("raw_id", rawID).Msg("user update rejected: invalid id")
		return UserView{}, err
	}
	log = log.With().Str("user_id", id.String()).Logger()

	var name *domain.DisplayName
	if in.DisplayName != nil {
		n, err := domain.ParseDisplayName(*in.DisplayName)
		if err != nil {
			log.Warn().Err(err).Msg("user update rejected: invalid display name")
			return UserView{}, err
		}
		name = &n
	}
	var status *domain.UserStatus
	if in.Status != nil {
		st, err := domain.ParseUserStatus(*in.Status)
		if err != nil {
			log.Warn().Err(err).Msg("user update rejected: invalid status")
			return UserView{}, err
		}
		status = &st
	}

	u, err := s.repo.GetByID(ctx, id)
	if err != nil {
		log.Warn().Err(err).Msg("user update failed: lookup")
		return UserView{}, err
	}
	now := s.now().UTC()
	if name != nil {
		u = u.Rename(*name, now)
	}
	if status != nil {
		u = u.ChangeStatus(*status, now)
	}
	u, err = s.repo.Update(ctx, u)
	if err != nil {
		log.Error().Err(err).Msg("user update failed: persist")
		return UserView{}, err
	}
	s.invalidate(ctx, log, id)
	view := NewUserView(u)
	cacheWrite(ctx, s.cache, s.ttl, log, cacheKey(id), view)
	log.Info().Str("status", string(u.Status)).Msg("user updated")
	return view, nil
}

// DeleteUser removes an account.
func (s *UserService) DeleteUser(ctx context.Context, rawID string) error {
	log := s.loggerFor(ctx, "delete_user")

	id, err := domain.ParseUserID(rawID)
	if err != nil {
		log.Warn().Err(err).Str("raw_id", rawID).Msg("user deletion rejected: invalid id")
		return err
	}
	log = log.With().Str("user_id", id.String()).Logger()

	if err := s.repo.Delete(ctx, id); err != nil {
		log.Warn().Err(err).Msg("user deletion failed")
		return err
	}
	s.invalidate(ctx, log, id)
	log.Info().Msg("user deleted")
	return nil
}

// loggerFor builds the operation logger: request correlation fields from ctx
// (when present) plus this layer's own identity.
func (s *UserService) loggerFor(ctx context.Context, op string) zerolog.Logger {
	return logger.FromContext(ctx, s.log).With().
		Str("layer", "usecase").
		Str("op", op).
		Logger()
}

// cacheKey namespaces entries by schema version so a view change cannot be
// served from stale JSON.
func cacheKey(id domain.UserID) string { return "user:v1:" + id.String() }

func (s *UserService) invalidate(ctx context.Context, log zerolog.Logger, id domain.UserID) {
	deleteCacheKeys(ctx, s.cache, log, cacheKey(id))
}
