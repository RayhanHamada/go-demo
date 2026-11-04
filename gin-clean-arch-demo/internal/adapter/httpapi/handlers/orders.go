package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"gin-clean-arch-demo/internal/domain"
	"gin-clean-arch-demo/internal/usecase"
)

// CreateOrderRequest is the accepted body for POST /v1/orders.
type CreateOrderRequest struct {
	CustomerID string             `json:"customer_id" binding:"required" example:"01a1a21a-6bf6-7eb5-9d11-8831cd41c9d9" format:"uuid"`
	Currency   string             `json:"currency" binding:"required" example:"USD" minLength:"3" maxLength:"3"`
	Items      []OrderItemRequest `json:"items" binding:"required" minItems:"1" maxItems:"100"`
}

// ReplaceOrderItemsRequest is the accepted body for PUT /v1/orders/:id/items.
type ReplaceOrderItemsRequest struct {
	Items []OrderItemRequest `json:"items" binding:"required" minItems:"1" maxItems:"100"`
}

// OrderItemRequest is one requested line.
type OrderItemRequest struct {
	SKU            string `json:"sku" binding:"required" example:"WIDGET-01" maxLength:"40"`
	Quantity       int    `json:"quantity" binding:"required" example:"2" minimum:"1" maximum:"1000"`
	UnitPriceCents int64  `json:"unit_price_cents" binding:"required" example:"1999" minimum:"0" format:"int64"`
}

// OrderItemResponse is one line of an order, including its computed total.
type OrderItemResponse struct {
	SKU            string `json:"sku" example:"WIDGET-01"`
	Quantity       int    `json:"quantity" example:"2"`
	UnitPriceCents int64  `json:"unit_price_cents" example:"1999" format:"int64"`
	LineTotalCents int64  `json:"line_total_cents" example:"3998" format:"int64"`
}

// OrderResponse is the wire representation of an order. Money is in minor
// units, so no rounding is ever implied; version is the value the ETag
// carries.
type OrderResponse struct {
	ID         string              `json:"id" example:"01a1a21a-6bf6-7eb5-9d11-8831cd41c9d9" format:"uuid"`
	Number     string              `json:"number" example:"ORD-01A1A21A6BF6"`
	CustomerID string              `json:"customer_id" example:"01a1a21a-6bf6-7eb5-9d11-8831cd41c9d9" format:"uuid"`
	Currency   string              `json:"currency" example:"USD"`
	Status     string              `json:"status" enums:"draft,placed,cancelled" example:"draft"`
	Items      []OrderItemResponse `json:"items"`
	TotalCents int64               `json:"total_cents" example:"3998" format:"int64"`
	Version    int64               `json:"version" example:"1" format:"int64"`
	CreatedAt  time.Time           `json:"created_at" example:"2026-11-03T15:47:55.843263Z"`
	UpdatedAt  time.Time           `json:"updated_at" example:"2026-11-03T15:47:55.843263Z"`
}

func toOrderResponse(v usecase.OrderView) OrderResponse {
	items := make([]OrderItemResponse, len(v.Items))
	for i, item := range v.Items {
		items[i] = OrderItemResponse{
			SKU:            item.SKU.String(),
			Quantity:       int(item.Quantity),
			UnitPriceCents: int64(item.UnitPrice),
			LineTotalCents: int64(item.LineTotal),
		}
	}
	return OrderResponse{
		ID:         v.ID.String(),
		Number:     v.Number.String(),
		CustomerID: v.CustomerID.String(),
		Currency:   v.Currency.String(),
		Status:     string(v.Status),
		Items:      items,
		TotalCents: int64(v.TotalCents),
		Version:    v.Version,
		CreatedAt:  v.CreatedAt,
		UpdatedAt:  v.UpdatedAt,
	}
}

// ListOrdersResponse is one page of orders plus the paging envelope.
type ListOrdersResponse struct {
	Items  []OrderResponse `json:"items"`
	Total  int64           `json:"total" example:"7" format:"int64"`
	Limit  int             `json:"limit" example:"20"`
	Offset int             `json:"offset" example:"0"`
}

// CreateOrder handles POST /v1/orders.
//
//	@Summary		Create an order
//	@Description	Creates a draft order for an existing active customer. Item quantities are 1–1000, unit prices are minor units, and duplicated SKUs are rejected. The response carries an `ETag` for conditional writes.
//	@Tags			Orders
//	@Accept			json
//	@Produce		json
//	@Param			request	body		CreateOrderRequest	true	"Order to create"
//	@Success		201		{object}	OrderResponse
//	@Header			201		{string}	Location	"Canonical path of the created order"
//	@Header			201		{string}	ETag		"Entity tag encoding the order version"
//	@Failure		400		{object}	ErrorResponse
//	@Failure		404		{object}	ErrorResponse
//	@Failure		409		{object}	ErrorResponse
//	@Failure		500		{object}	ErrorResponse
//	@Router			/v1/orders [post]
func (h *Handler) CreateOrder(c *gin.Context) {
	var req CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, domain.Invalidf(domain.FieldRequest, "malformed JSON body: %v", err))
		return
	}
	view, err := h.orders.CreateOrder(c.Request.Context(), usecase.CreateOrderInput{
		CustomerID: req.CustomerID,
		Currency:   req.Currency,
		Items:      toItemInputs(req.Items),
	})
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.Header("Location", "/v1/orders/"+view.ID.String())
	c.Header("ETag", etagFor(view.Version))
	c.JSON(http.StatusCreated, toOrderResponse(view))
}

// GetOrder handles GET /v1/orders/:id.
//
//	@Summary		Fetch an order
//	@Description	Returns the order and its lines. The `ETag` header carries the version a conditional write must echo back in `If-Match`.
//	@Tags			Orders
//	@Produce		json
//	@Param			id	path		string	true	"Order identifier (UUID)"
//	@Success		200	{object}	OrderResponse
//	@Header			200	{string}	ETag	"Entity tag encoding the order version"
//	@Failure		400	{object}	ErrorResponse
//	@Failure		404	{object}	ErrorResponse
//	@Failure		500	{object}	ErrorResponse
//	@Router			/v1/orders/{id} [get]
func (h *Handler) GetOrder(c *gin.Context) {
	view, err := h.orders.GetOrder(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.Header("ETag", etagFor(view.Version))
	c.JSON(http.StatusOK, toOrderResponse(view))
}

// ListOrders handles GET /v1/orders.
//
//	@Summary		List orders
//	@Description	Returns one page of orders, newest first, plus the unpaged total. Filters combine: `status` narrows the lifecycle state and `customer_id` narrows the owner.
//	@Tags			Orders
//	@Produce		json
//	@Param			limit		query		int		false	"Page size"			minimum(1)	maximum(100)	default(20)
//	@Param			offset		query		int		false	"Rows to skip"		minimum(0)	default(0)
//	@Param			status		query		string	false	"Lifecycle state"	Enums(draft, placed, cancelled)
//	@Param			customer_id	query		string	false	"Only orders of this customer"	format(uuid)
//	@Success		200			{object}	ListOrdersResponse
//	@Failure		400			{object}	ErrorResponse
//	@Failure		500			{object}	ErrorResponse
//	@Router			/v1/orders [get]
func (h *Handler) ListOrders(c *gin.Context) {
	q := domain.ListOrdersQuery{}
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
		status, err := domain.ParseOrderStatus(raw)
		if err != nil {
			h.respondError(c, err)
			return
		}
		q.Status = &status
	}
	if raw := c.Query("customer_id"); raw != "" {
		id, err := domain.ParseUserID(raw)
		if err != nil {
			h.respondError(c, err)
			return
		}
		q.CustomerID = &id
	}
	page, err := h.orders.ListOrders(c.Request.Context(), q)
	if err != nil {
		h.respondError(c, err)
		return
	}
	items := make([]OrderResponse, len(page.Items))
	for i, v := range page.Items {
		items[i] = toOrderResponse(v)
	}
	c.JSON(http.StatusOK, ListOrdersResponse{Items: items, Total: page.Total, Limit: page.Limit, Offset: page.Offset})
}

// ReplaceOrderItems handles PUT /v1/orders/:id/items.
//
//	@Summary		Replace an order's lines
//	@Description	Replaces the whole line list of a draft order. A placed or cancelled order is immutable, so this is a 409. Send `If-Match` with the ETag from your read to make the write conditional.
//	@Tags			Orders
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string						true	"Order identifier (UUID)"
//	@Param			request	body		ReplaceOrderItemsRequest	true	"New line list"
//	@Param			If-Match	header	string						false	"ETag from the last read; 412 when stale"
//	@Success		200		{object}	OrderResponse
//	@Header			200		{string}	ETag	"Entity tag encoding the new order version"
//	@Failure		400		{object}	ErrorResponse
//	@Failure		404		{object}	ErrorResponse
//	@Failure		409		{object}	ErrorResponse
//	@Failure		412		{object}	ErrorResponse
//	@Failure		500		{object}	ErrorResponse
//	@Router			/v1/orders/{id}/items [put]
func (h *Handler) ReplaceOrderItems(c *gin.Context) {
	var req ReplaceOrderItemsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, domain.Invalidf(domain.FieldRequest, "malformed JSON body: %v", err))
		return
	}
	version, err := ifMatchVersion(c)
	if err != nil {
		h.respondError(c, err)
		return
	}
	view, err := h.orders.ReplaceOrderItems(c.Request.Context(), c.Param("id"), version, toItemInputs(req.Items))
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.Header("ETag", etagFor(view.Version))
	c.JSON(http.StatusOK, toOrderResponse(view))
}

// PlaceOrder handles POST /v1/orders/:id/place.
//
//	@Summary		Place an order
//	@Description	Draft → placed. Placement is one-way and not idempotent: placing an already placed order is a 409, so a retry cannot be mistaken for a second placement.
//	@Tags			Orders
//	@Produce		json
//	@Param			id			path	string	true	"Order identifier (UUID)"
//	@Param			If-Match	header	string	false	"ETag from the last read; 412 when stale"
//	@Success		200			{object}	OrderResponse
//	@Header			200			{string}	ETag	"Entity tag encoding the new order version"
//	@Failure		400			{object}	ErrorResponse
//	@Failure		404			{object}	ErrorResponse
//	@Failure		409			{object}	ErrorResponse
//	@Failure		412			{object}	ErrorResponse
//	@Failure		500			{object}	ErrorResponse
//	@Router			/v1/orders/{id}/place [post]
func (h *Handler) PlaceOrder(c *gin.Context) {
	h.transition(c, h.orders.PlaceOrder)
}

// CancelOrder handles POST /v1/orders/:id/cancel.
//
//	@Summary		Cancel an order
//	@Description	Draft or placed → cancelled. Cancellation is terminal; cancelling twice is a 409.
//	@Tags			Orders
//	@Produce		json
//	@Param			id			path	string	true	"Order identifier (UUID)"
//	@Param			If-Match	header	string	false	"ETag from the last read; 412 when stale"
//	@Success		200			{object}	OrderResponse
//	@Header			200			{string}	ETag	"Entity tag encoding the new order version"
//	@Failure		400			{object}	ErrorResponse
//	@Failure		404			{object}	ErrorResponse
//	@Failure		409			{object}	ErrorResponse
//	@Failure		412			{object}	ErrorResponse
//	@Failure		500			{object}	ErrorResponse
//	@Router			/v1/orders/{id}/cancel [post]
func (h *Handler) CancelOrder(c *gin.Context) {
	h.transition(c, h.orders.CancelOrder)
}

// transition applies a lifecycle action with the shared precondition handling.
func (h *Handler) transition(c *gin.Context, action func(ctx context.Context, id string, version *int64) (usecase.OrderView, error)) {
	version, err := ifMatchVersion(c)
	if err != nil {
		h.respondError(c, err)
		return
	}
	view, err := action(c.Request.Context(), c.Param("id"), version)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.Header("ETag", etagFor(view.Version))
	c.JSON(http.StatusOK, toOrderResponse(view))
}

// DeleteOrder handles DELETE /v1/orders/:id.
//
//	@Summary		Delete a draft order
//	@Description	Removes a draft order and its lines. Placed and cancelled orders are records, not scratch data: deleting them is a 409. Send `If-Match` to make the delete conditional.
//	@Tags			Orders
//	@Param			id			path	string	true	"Order identifier (UUID)"
//	@Param			If-Match	header	string	false	"ETag from the last read; 412 when stale"
//	@Success		204			"No Content"
//	@Failure		400			{object}	ErrorResponse
//	@Failure		404			{object}	ErrorResponse
//	@Failure		409			{object}	ErrorResponse
//	@Failure		412			{object}	ErrorResponse
//	@Failure		500			{object}	ErrorResponse
//	@Router			/v1/orders/{id} [delete]
func (h *Handler) DeleteOrder(c *gin.Context) {
	version, err := ifMatchVersion(c)
	if err != nil {
		h.respondError(c, err)
		return
	}
	if err := h.orders.DeleteOrder(c.Request.Context(), c.Param("id"), version); err != nil {
		h.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// toItemInputs maps the wire lines onto the domain input.
func toItemInputs(in []OrderItemRequest) []domain.OrderItemInput {
	out := make([]domain.OrderItemInput, len(in))
	for i, item := range in {
		out[i] = domain.OrderItemInput{SKU: item.SKU, Quantity: item.Quantity, UnitPrice: item.UnitPriceCents}
	}
	return out
}

// etagFor renders a version as a strong entity tag.
func etagFor(version int64) string { return `"v` + strconv.FormatInt(version, 10) + `"` }

// ifMatchVersion parses the If-Match header. An absent header means "no
// precondition", which the service still guards with the version it observes.
func ifMatchVersion(c *gin.Context) (*int64, error) {
	raw := strings.TrimSpace(c.GetHeader("If-Match"))
	switch raw {
	case "":
		return nil, nil
	case "*":
		return nil, domain.Invalidf(domain.FieldVersion, `must name a version, not "*"`)
	}
	s := strings.TrimPrefix(raw, `"v`)
	s = strings.TrimSuffix(s, `"`)
	version, err := strconv.ParseInt(s, 10, 64)
	if err != nil || version < 1 {
		return nil, domain.Invalidf(domain.FieldVersion, `must be an ETag of the form "v<version>"`)
	}
	return &version, nil
}
