package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"gin-clean-arch-demo/internal/domain"
	"gin-clean-arch-demo/internal/usecase"
)

// CreateUserRequest is the accepted body for POST /v1/users.
type CreateUserRequest struct {
	Email       string `json:"email" binding:"required" example:"ada@example.com" format:"email" maxLength:"254"`
	DisplayName string `json:"display_name" binding:"required" example:"Ada Lovelace" minLength:"1" maxLength:"100"`
}

// UpdateUserRequest is the accepted body for PATCH /v1/users/:id. Pointer
// fields distinguish "absent" from "empty".
type UpdateUserRequest struct {
	DisplayName *string `json:"display_name" example:"Ada, Countess of Lovelace" minLength:"1" maxLength:"100"`
	Status      *string `json:"status" enums:"active,suspended" example:"suspended"`
}

// UserResponse is the wire representation of a user.
type UserResponse struct {
	ID          string    `json:"id" example:"01a1a21a-6bf6-7eb5-9d11-8831cd41c9d9" format:"uuid"`
	Email       string    `json:"email" example:"ada@example.com" format:"email"`
	DisplayName string    `json:"display_name" example:"Ada Lovelace"`
	Status      string    `json:"status" enums:"active,suspended" example:"active"`
	CreatedAt   time.Time `json:"created_at" example:"2026-11-03T15:47:55.843263Z"`
	UpdatedAt   time.Time `json:"updated_at" example:"2026-11-03T15:47:55.843263Z"`
}

func toUserResponse(v usecase.UserView) UserResponse {
	return UserResponse{
		ID:          v.ID.String(),
		Email:       v.Email.String(),
		DisplayName: v.DisplayName.String(),
		Status:      string(v.Status),
		CreatedAt:   v.CreatedAt,
		UpdatedAt:   v.UpdatedAt,
	}
}

// ListUsersResponse is one page of users plus the paging envelope.
type ListUsersResponse struct {
	Items  []UserResponse `json:"items"`
	Total  int64          `json:"total" example:"42" format:"int64"`
	Limit  int            `json:"limit" example:"20"`
	Offset int            `json:"offset" example:"0"`
}

// CreateUser handles POST /v1/users.
//
//	@Summary		Create a user
//	@Description	Creates an active user. The email is trimmed and lower-cased before validation, so `Ada@Example.com` and `ada@example.com` collide.
//	@Tags			Users
//	@Accept			json
//	@Produce		json
//	@Param			request	body		CreateUserRequest	true	"User to create"
//	@Success		201		{object}	UserResponse
//	@Header			201		{string}	Location	"Canonical path of the created user"
//	@Failure		400		{object}	ErrorResponse
//	@Failure		409		{object}	ErrorResponse
//	@Failure		500		{object}	ErrorResponse
//	@Router			/v1/users [post]
func (h *Handler) CreateUser(c *gin.Context) {
	var req CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, domain.Invalidf(domain.FieldRequest, "malformed JSON body: %v", err))
		return
	}
	view, err := h.users.CreateUser(c.Request.Context(), usecase.CreateUserInput{
		Email:       req.Email,
		DisplayName: req.DisplayName,
	})
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.Header("Location", "/v1/users/"+view.ID.String())
	c.JSON(http.StatusCreated, toUserResponse(view))
}

// GetUser handles GET /v1/users/:id.
//
//	@Summary		Fetch a user
//	@Description	Served from the cache when possible. A malformed identifier is a client error (400), not a missing resource (404).
//	@Tags			Users
//	@Produce		json
//	@Param			id	path		string	true	"User identifier (UUID)"
//	@Success		200	{object}	UserResponse
//	@Failure		400	{object}	ErrorResponse
//	@Failure		404	{object}	ErrorResponse
//	@Failure		500	{object}	ErrorResponse
//	@Router			/v1/users/{id} [get]
func (h *Handler) GetUser(c *gin.Context) {
	view, err := h.users.GetUser(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserResponse(view))
}

// ListUsers handles GET /v1/users.
//
//	@Summary		List users
//	@Description	Returns one page of users, newest first, plus the unpaged total. The total reflects the same status filter as the page.
//	@Tags			Users
//	@Produce		json
//	@Param			limit	query		int		false	"Page size"			minimum(1)	maximum(100)	default(20)
//	@Param			offset	query		int		false	"Rows to skip"		minimum(0)	default(0)
//	@Param			status	query		string	false	"Lifecycle state"	Enums(active, suspended)
//	@Success		200		{object}	ListUsersResponse
//	@Failure		400		{object}	ErrorResponse
//	@Failure		500		{object}	ErrorResponse
//	@Router			/v1/users [get]
func (h *Handler) ListUsers(c *gin.Context) {
	q := domain.ListUsersQuery{}
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			h.respondError(c, domain.Invalidf(domain.FieldLimit, "must be an integer"))
			return
		}
		q.Limit = n
	}
	if raw := c.Query("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			h.respondError(c, domain.Invalidf(domain.FieldOffset, "must be an integer"))
			return
		}
		q.Offset = n
	}
	if raw := c.Query("status"); raw != "" {
		status, err := domain.ParseUserStatus(raw)
		if err != nil {
			h.respondError(c, err)
			return
		}
		q.Status = &status
	}
	page, err := h.users.ListUsers(c.Request.Context(), q)
	if err != nil {
		h.respondError(c, err)
		return
	}
	items := make([]UserResponse, len(page.Items))
	for i, v := range page.Items {
		items[i] = toUserResponse(v)
	}
	c.JSON(http.StatusOK, ListUsersResponse{Items: items, Total: page.Total, Limit: page.Limit, Offset: page.Offset})
}

// UpdateUser handles PATCH /v1/users/:id.
//
//	@Summary		Update a user
//	@Description	Partial update: only supplied fields change and `updated_at` moves to the write time. An empty object is rejected. Email and identifier are immutable.
//	@Tags			Users
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string				true	"User identifier (UUID)"
//	@Param			request	body		UpdateUserRequest	true	"Fields to change"
//	@Success		200		{object}	UserResponse
//	@Failure		400		{object}	ErrorResponse
//	@Failure		404		{object}	ErrorResponse
//	@Failure		500		{object}	ErrorResponse
//	@Router			/v1/users/{id} [patch]
func (h *Handler) UpdateUser(c *gin.Context) {
	var req UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, domain.Invalidf(domain.FieldRequest, "malformed JSON body: %v", err))
		return
	}
	if req.DisplayName == nil && req.Status == nil {
		h.respondError(c, domain.Invalidf(domain.FieldRequest, "at least one field must be supplied"))
		return
	}
	view, err := h.users.UpdateUser(c.Request.Context(), c.Param("id"), usecase.UpdateUserInput{
		DisplayName: req.DisplayName,
		Status:      req.Status,
	})
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserResponse(view))
}

// DeleteUser handles DELETE /v1/users/:id.
//
//	@Summary		Delete a user
//	@Description	Removes the user and its cached copy. Deleting twice yields 404 for the second attempt.
//	@Tags			Users
//	@Param			id	path	string	true	"User identifier (UUID)"
//	@Success		204	"No Content"
//	@Failure		400	{object}	ErrorResponse
//	@Failure		404	{object}	ErrorResponse
//	@Failure		500	{object}	ErrorResponse
//	@Router			/v1/users/{id} [delete]
func (h *Handler) DeleteUser(c *gin.Context) {
	if err := h.users.DeleteUser(c.Request.Context(), c.Param("id")); err != nil {
		h.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
