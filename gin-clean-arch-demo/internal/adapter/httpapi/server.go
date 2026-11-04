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
)

// UserService is the subset of the application service the handlers need.
type UserService = handlers.UserService

// OrderService is the subset of the order application service the handlers
// need.
type OrderService = handlers.OrderService

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
func NewServer(users UserService, orders OrderService, db, cache HealthChecker, logger zerolog.Logger) *Server {
	// The logger is kept unscoped: request-scoped lines add layer "http" and
	// the usecase adds its own, so neither can inherit the other's identity.
	return &Server{
		handlers: handlers.New(users, orders, db, cache, logger),
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

		v1.POST("/orders", s.handlers.CreateOrder)
		v1.GET("/orders", s.handlers.ListOrders)
		v1.GET("/orders/:id", s.handlers.GetOrder)
		v1.DELETE("/orders/:id", s.handlers.DeleteOrder)
		v1.PUT("/orders/:id/items", s.handlers.ReplaceOrderItems)
		v1.POST("/orders/:id/place", s.handlers.PlaceOrder)
		v1.POST("/orders/:id/cancel", s.handlers.CancelOrder)
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
