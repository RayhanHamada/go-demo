// Package handlers is the HTTP handler layer: it decodes requests, encodes
// responses and maps domain errors to status codes. Routing, middleware and
// the http.Server live in the parent httpapi package; application logic lives
// in the usecase package.
package handlers

import (
	"context"

	"github.com/rs/zerolog"

	"gin-clean-arch-demo/internal/domain"
	"gin-clean-arch-demo/internal/usecase"
)

// UserService is the subset of the application service the handlers need.
type UserService interface {
	CreateUser(ctx context.Context, in usecase.CreateUserInput) (usecase.UserView, error)
	GetUser(ctx context.Context, id string) (usecase.UserView, error)
	ListUsers(ctx context.Context, q domain.ListUsersQuery) (domain.Page[usecase.UserView], error)
	UpdateUser(ctx context.Context, id string, in usecase.UpdateUserInput) (usecase.UserView, error)
	DeleteUser(ctx context.Context, id string) error
}

// HealthChecker reports dependency health.
type HealthChecker interface {
	Ping(ctx context.Context) error
}

// Handler holds everything the HTTP handlers need. Handlers are methods on it
// so dependencies are explicit and testable.
type Handler struct {
	users  UserService
	db     HealthChecker
	cache  HealthChecker
	logger zerolog.Logger
}

// New builds the HTTP handlers.
func New(users UserService, db, cache HealthChecker, logger zerolog.Logger) *Handler {
	// The logger is kept unscoped: request-scoped lines add layer "http" and
	// the usecase adds its own, so neither can inherit the other's identity.
	return &Handler{users: users, db: db, cache: cache, logger: logger}
}
