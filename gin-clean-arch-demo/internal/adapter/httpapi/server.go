// Package httpapi is the HTTP delivery layer: it owns routing, request
// decoding, response encoding and the mapping from domain errors to status
// codes. Application logic lives in the usecase package.
package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
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

// Server holds everything the HTTP layer needs. Handlers are methods on it so
// dependencies are explicit and testable.
type Server struct {
	users  UserService
	db     HealthChecker
	cache  HealthChecker
	logger zerolog.Logger
}

// NewServer builds the HTTP layer.
func NewServer(users UserService, db, cache HealthChecker, logger zerolog.Logger) *Server {
	// The logger is kept unscoped: request-scoped lines add layer "http" and
	// the usecase adds its own, so neither can inherit the other's identity.
	return &Server{users: users, db: db, cache: cache, logger: logger}
}

// Router returns the configured Gin engine.
func (s *Server) Router() *gin.Engine {
	r := gin.New()
	r.Use(RequestID(), RequestLogger(s.logger), gin.Recovery())

	r.GET("/healthz", s.health)

	v1 := r.Group("/v1")
	{
		v1.POST("/users", s.createUser)
		v1.GET("/users", s.listUsers)
		v1.GET("/users/:id", s.getUser)
		v1.PATCH("/users/:id", s.updateUser)
		v1.DELETE("/users/:id", s.deleteUser)
	}
	return r
}

// NewHTTPServer wraps the router in an http.Server with production timeouts.
func (s *Server) NewHTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.Router(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
