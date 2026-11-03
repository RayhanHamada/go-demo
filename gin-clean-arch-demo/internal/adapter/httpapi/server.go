// Package httpapi is the HTTP delivery layer: it owns routing, middleware and
// the http.Server lifecycle. Request decoding, response encoding and the
// mapping from domain errors to status codes live in the handlers package.
// Application logic lives in the usecase package.
package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"gin-clean-arch-demo/internal/adapter/httpapi/handlers"
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

// Server holds the HTTP layer's collaborators: the router-facing handlers and
// the process logger used by middleware.
type Server struct {
	handlers *handlers.Handler
	logger   zerolog.Logger
}

// NewServer builds the HTTP layer.
func NewServer(users UserService, db, cache HealthChecker, logger zerolog.Logger) *Server {
	// The logger is kept unscoped: request-scoped lines add layer "http" and
	// the usecase adds its own, so neither can inherit the other's identity.
	return &Server{
		handlers: handlers.New(users, db, cache, logger),
		logger:   logger,
	}
}

// Router returns the configured Gin engine.
func (s *Server) Router() *gin.Engine {
	r := gin.New()
	r.Use(RequestID(), RequestLogger(s.logger), gin.Recovery())

	r.GET("/healthz", s.handlers.Health)
	s.registerDocs(r)

	v1 := r.Group("/v1")
	{
		v1.POST("/users", s.handlers.CreateUser)
		v1.GET("/users", s.handlers.ListUsers)
		v1.GET("/users/:id", s.handlers.GetUser)
		v1.PATCH("/users/:id", s.handlers.UpdateUser)
		v1.DELETE("/users/:id", s.handlers.DeleteUser)
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
