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

// OrderService is the subset of the order application service the handlers
// need. versions are the values the caller read (ETags).
type OrderService interface {
	CreateOrder(ctx context.Context, in usecase.CreateOrderInput) (usecase.OrderView, error)
	GetOrder(ctx context.Context, id string) (usecase.OrderView, error)
	ListOrders(ctx context.Context, q domain.ListOrdersQuery) (domain.Page[usecase.OrderView], error)
	ReplaceOrderItems(ctx context.Context, id string, version *int64, in []domain.OrderItemInput) (usecase.OrderView, error)
	PlaceOrder(ctx context.Context, id string, version *int64) (usecase.OrderView, error)
	CancelOrder(ctx context.Context, id string, version *int64) (usecase.OrderView, error)
	DeleteOrder(ctx context.Context, id string, version *int64) error
}

// HealthChecker reports dependency health.
type HealthChecker interface {
	Ping(ctx context.Context) error
}

// Handler holds everything the HTTP handlers need. Handlers are methods on it
// so dependencies are explicit and testable.
type Handler struct {
	users  UserService
	orders OrderService
	db     HealthChecker
	cache  HealthChecker
	logger zerolog.Logger
}

// New builds the HTTP handlers.
func New(users UserService, orders OrderService, db, cache HealthChecker, logger zerolog.Logger) *Handler {
	// The logger is kept unscoped: request-scoped lines add layer "http" and
	// the usecase adds its own, so neither can inherit the other's identity.
	return &Handler{users: users, orders: orders, db: db, cache: cache, logger: logger}
}
