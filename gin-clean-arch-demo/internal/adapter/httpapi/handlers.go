package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"gin-clean-arch-demo/internal/domain"
	"gin-clean-arch-demo/internal/usecase"
)

// createUserRequest is the accepted body for POST /v1/users.
type createUserRequest struct {
	Email       string `json:"email" binding:"required"`
	DisplayName string `json:"display_name" binding:"required"`
}

// updateUserRequest is the accepted body for PATCH /v1/users/:id. Pointer
// fields distinguish "absent" from "empty".
type updateUserRequest struct {
	DisplayName *string `json:"display_name"`
	Status      *string `json:"status"`
}

// userResponse is the wire representation of a user.
type userResponse struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toUserResponse(v usecase.UserView) userResponse {
	return userResponse{
		ID:          v.ID.String(),
		Email:       v.Email.String(),
		DisplayName: v.DisplayName.String(),
		Status:      string(v.Status),
		CreatedAt:   v.CreatedAt,
		UpdatedAt:   v.UpdatedAt,
	}
}

// listUsersResponse is one page of users plus the paging envelope.
type listUsersResponse struct {
	Items  []userResponse `json:"items"`
	Total  int64          `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

type healthResponse struct {
	Status string `json:"status"`
	DB     string `json:"db"`
	Cache  string `json:"cache"`
}

// createUser handles POST /v1/users.
func (s *Server) createUser(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondError(c, domain.Invalidf(domain.FieldRequest, "malformed JSON body: %v", err))
		return
	}
	view, err := s.users.CreateUser(c.Request.Context(), usecase.CreateUserInput{
		Email:       req.Email,
		DisplayName: req.DisplayName,
	})
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.Header("Location", "/v1/users/"+view.ID.String())
	c.JSON(http.StatusCreated, toUserResponse(view))
}

// getUser handles GET /v1/users/:id.
func (s *Server) getUser(c *gin.Context) {
	view, err := s.users.GetUser(c.Request.Context(), c.Param("id"))
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserResponse(view))
}

// listUsers handles GET /v1/users.
func (s *Server) listUsers(c *gin.Context) {
	q := domain.ListUsersQuery{}
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			s.respondError(c, domain.Invalidf(domain.FieldLimit, "must be an integer"))
			return
		}
		q.Limit = n
	}
	if raw := c.Query("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			s.respondError(c, domain.Invalidf(domain.FieldOffset, "must be an integer"))
			return
		}
		q.Offset = n
	}
	if raw := c.Query("status"); raw != "" {
		status, err := domain.ParseUserStatus(raw)
		if err != nil {
			s.respondError(c, err)
			return
		}
		q.Status = &status
	}
	page, err := s.users.ListUsers(c.Request.Context(), q)
	if err != nil {
		s.respondError(c, err)
		return
	}
	items := make([]userResponse, len(page.Items))
	for i, v := range page.Items {
		items[i] = toUserResponse(v)
	}
	c.JSON(http.StatusOK, listUsersResponse{Items: items, Total: page.Total, Limit: page.Limit, Offset: page.Offset})
}

// updateUser handles PATCH /v1/users/:id.
func (s *Server) updateUser(c *gin.Context) {
	var req updateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondError(c, domain.Invalidf(domain.FieldRequest, "malformed JSON body: %v", err))
		return
	}
	if req.DisplayName == nil && req.Status == nil {
		s.respondError(c, domain.Invalidf(domain.FieldRequest, "at least one field must be supplied"))
		return
	}
	view, err := s.users.UpdateUser(c.Request.Context(), c.Param("id"), usecase.UpdateUserInput{
		DisplayName: req.DisplayName,
		Status:      req.Status,
	})
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserResponse(view))
}

// deleteUser handles DELETE /v1/users/:id.
func (s *Server) deleteUser(c *gin.Context) {
	if err := s.users.DeleteUser(c.Request.Context(), c.Param("id")); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// health handles GET /healthz, reporting each dependency separately.
func (s *Server) health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	logger := s.logger.With().Str("layer", "http").Str("op", "health").Logger()

	resp := healthResponse{Status: "ok", DB: "ok", Cache: "ok"}
	code := http.StatusOK
	if err := s.db.Ping(ctx); err != nil {
		logger.Warn().Err(err).Msg("database health check failed")
		resp.DB, resp.Status, code = "down", "degraded", http.StatusServiceUnavailable
	}
	if err := s.cache.Ping(ctx); err != nil {
		logger.Warn().Err(err).Msg("cache health check failed")
		resp.Cache, resp.Status, code = "down", "degraded", http.StatusServiceUnavailable
	}
	c.JSON(code, resp)
}

// respondError maps domain errors to protocol responses, logging the
// unexpected ones so the client only sees a generic message.
func (s *Server) respondError(c *gin.Context, err error) {
	log := loggerFrom(c).With().Str("layer", "http").Logger()

	switch {
	case errors.Is(err, domain.ErrInvalid):
		body := gin.H{"error": err.Error()}
		var ve *domain.ValidationError
		if errors.As(err, &ve) {
			body["field"] = ve.Field
		}
		log.Warn().Err(err).Int("status", http.StatusBadRequest).Msg("request rejected: invalid input")
		c.AbortWithStatusJSON(http.StatusBadRequest, body)
	case errors.Is(err, domain.ErrNotFound):
		log.Info().Err(err).Int("status", http.StatusNotFound).Msg("request rejected: not found")
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrEmailTaken):
		log.Warn().Err(err).Int("status", http.StatusConflict).Msg("request rejected: conflict")
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		log.Error().Err(err).Int("status", http.StatusInternalServerError).Msg("request failed: unexpected error")
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}
