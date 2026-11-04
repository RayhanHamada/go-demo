package usecase

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"gin-clean-arch-demo/internal/domain"
	"gin-clean-arch-demo/internal/logger"
)

// OrderRepository is the persistence port for orders. Implementations must
// apply each conditional write atomically: expectedVersion is compared and the
// version incremented inside the same transaction.
type OrderRepository interface {
	Create(ctx context.Context, o domain.Order) (domain.Order, error)
	GetByID(ctx context.Context, id domain.OrderID) (domain.Order, error)
	UpdateStatus(ctx context.Context, o domain.Order, expectedVersion int64) (domain.Order, error)
	ReplaceItems(ctx context.Context, o domain.Order, expectedVersion int64) (domain.Order, error)
	Delete(ctx context.Context, id domain.OrderID, expectedVersion int64) error
	List(ctx context.Context, q domain.ListOrdersQuery) (domain.Page[domain.Order], error)
}

// CustomerRepository is the read-only customer port orders need: an order
// names an existing, active user.
type CustomerRepository interface {
	GetByID(ctx context.Context, id domain.UserID) (domain.User, error)
}

// OrderItemView is the wire-level projection of one order line.
type OrderItemView struct {
	SKU       domain.SKU      `json:"sku"`
	Quantity  domain.Quantity `json:"quantity"`
	UnitPrice domain.Cents    `json:"unit_price_cents"`
	LineTotal domain.Cents    `json:"line_total_cents"`
}

// OrderView is the application-level projection of a domain.Order.
type OrderView struct {
	ID         domain.OrderID      `json:"id"`
	Number     domain.OrderNumber  `json:"number"`
	CustomerID domain.UserID       `json:"customer_id"`
	Currency   domain.CurrencyCode `json:"currency"`
	Status     domain.OrderStatus  `json:"status"`
	Items      []OrderItemView     `json:"items"`
	TotalCents domain.Cents        `json:"total_cents"`
	Version    int64               `json:"version"`
	CreatedAt  time.Time           `json:"created_at"`
	UpdatedAt  time.Time           `json:"updated_at"`
}

// NewOrderView projects a domain order. Line totals and the order total are
// computed here from integer minor units, so a client never has to multiply.
func NewOrderView(o domain.Order) OrderView {
	items := make([]OrderItemView, len(o.Items))
	for i, item := range o.Items {
		items[i] = OrderItemView{
			SKU:       item.SKU,
			Quantity:  item.Quantity,
			UnitPrice: item.UnitPrice,
			LineTotal: item.LineTotal(),
		}
	}
	return OrderView{
		ID:         o.ID,
		Number:     o.Number,
		CustomerID: o.CustomerID,
		Currency:   o.Currency,
		Status:     o.Status,
		Items:      items,
		TotalCents: o.Total(),
		Version:    o.Version,
		CreatedAt:  o.CreatedAt,
		UpdatedAt:  o.UpdatedAt,
	}
}

// CreateOrderInput carries raw caller input, validated by CreateOrder.
type CreateOrderInput struct {
	CustomerID string
	Currency   string
	Items      []domain.OrderItemInput
}

// OrderService is the order application service. Order writes are conditional
// on the version the caller read, which is what the HTTP layer exposes as an
// ETag.
type OrderService struct {
	orders    OrderRepository
	customers CustomerRepository
	cache     Cache
	log       zerolog.Logger
	ttl       time.Duration
	now       func() time.Time
}

// OrderServiceOption customises an OrderService.
type OrderServiceOption func(*OrderService)

// WithOrderClock replaces the time source (tests).
func WithOrderClock(now func() time.Time) OrderServiceOption {
	return func(s *OrderService) { s.now = now }
}

// NewOrderService wires the service. A non-positive ttl disables caching.
func NewOrderService(orders OrderRepository, customers CustomerRepository, cache Cache, log zerolog.Logger, ttl time.Duration, opts ...OrderServiceOption) *OrderService {
	s := &OrderService{orders: orders, customers: customers, cache: cache, log: log, ttl: ttl, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CreateOrder validates the customer and the line list, then persists a draft.
// A suspended customer cannot order: the account is read-only by definition.
func (s *OrderService) CreateOrder(ctx context.Context, in CreateOrderInput) (OrderView, error) {
	log := s.loggerFor(ctx, "create_order")

	customerID, err := domain.ParseUserID(in.CustomerID)
	if err != nil {
		log.Warn().Err(err).Msg("order creation rejected: invalid customer id")
		return OrderView{}, err
	}
	log = log.With().Str("customer_id", customerID.String()).Logger()

	currency, err := domain.ParseCurrency(in.Currency)
	if err != nil {
		log.Warn().Err(err).Msg("order creation rejected: invalid currency")
		return OrderView{}, err
	}
	items, err := domain.ParseOrderItems(in.Items)
	if err != nil {
		log.Warn().Err(err).Msg("order creation rejected: invalid items")
		return OrderView{}, err
	}

	customer, err := s.customers.GetByID(ctx, customerID)
	if err != nil {
		if domain.IsNotFound(err) {
			log.Warn().Err(err).Msg("order creation rejected: unknown customer")
			return OrderView{}, err
		}
		log.Error().Err(err).Msg("order creation failed: customer lookup")
		return OrderView{}, err
	}
	if customer.Status != domain.StatusActive {
		err := domain.StateConflictf("customer %s is %s and cannot place orders", customerID, customer.Status)
		log.Warn().Err(err).Msg("order creation rejected: inactive customer")
		return OrderView{}, err
	}

	order, err := s.orders.Create(ctx, domain.NewOrder(customerID, currency, items, s.now().UTC()))
	if err != nil {
		log.Error().Err(err).Msg("order creation failed")
		return OrderView{}, err
	}
	log.Info().Str("order_id", order.ID.String()).Str("order_number", order.Number.String()).
		Int64("total_cents", int64(order.Total())).Msg("order created")
	return NewOrderView(order), nil
}

// GetOrder returns an order, served from cache when possible.
func (s *OrderService) GetOrder(ctx context.Context, rawID string) (OrderView, error) {
	log := s.loggerFor(ctx, "get_order")

	id, err := domain.ParseOrderID(rawID)
	if err != nil {
		log.Warn().Err(err).Str("raw_id", rawID).Msg("order lookup rejected: invalid id")
		return OrderView{}, err
	}
	log = log.With().Str("order_id", id.String()).Logger()

	if view, ok := cacheRead[OrderView](ctx, s.cache, s.ttl, log, orderCacheKey(id)); ok {
		log.Debug().Msg("order served from cache")
		return view, nil
	}
	order, err := s.orders.GetByID(ctx, id)
	if err != nil {
		log.Warn().Err(err).Msg("order lookup failed")
		return OrderView{}, err
	}
	view := NewOrderView(order)
	cacheWrite(ctx, s.cache, s.ttl, log, orderCacheKey(id), view)
	log.Debug().Msg("order loaded from database")
	return view, nil
}

// ListOrders returns one page of orders filtered by q.
func (s *OrderService) ListOrders(ctx context.Context, q domain.ListOrdersQuery) (domain.Page[OrderView], error) {
	log := s.loggerFor(ctx, "list_orders")

	if err := q.Normalize(); err != nil {
		log.Warn().Err(err).Msg("order listing rejected: invalid query")
		return domain.Page[OrderView]{}, err
	}
	page, err := s.orders.List(ctx, q)
	if err != nil {
		log.Error().Err(err).Msg("order listing failed")
		return domain.Page[OrderView]{}, err
	}
	views := make([]OrderView, len(page.Items))
	for i, order := range page.Items {
		views[i] = NewOrderView(order)
	}
	log.Debug().Int("returned", len(views)).Int64("total", page.Total).
		Int("limit", page.Limit).Int("offset", page.Offset).Msg("orders listed")
	return domain.Page[OrderView]{
		Items:  views,
		Total:  page.Total,
		Limit:  page.Limit,
		Offset: page.Offset,
	}, nil
}

// PlaceOrder moves a draft to placed. storedVersion is the version the caller
// read (the ETag it echoed back); when it is nil the version observed by this
// call is used, so a concurrent write still loses.
func (s *OrderService) PlaceOrder(ctx context.Context, rawID string, storedVersion *int64) (OrderView, error) {
	return s.transition(ctx, "place_order", rawID, storedVersion, domain.Order.Place, "placed")
}

// CancelOrder moves a draft or placed order to cancelled.
func (s *OrderService) CancelOrder(ctx context.Context, rawID string, storedVersion *int64) (OrderView, error) {
	return s.transition(ctx, "cancel_order", rawID, storedVersion, domain.Order.Cancel, "cancelled")
}

// transition reads the order, applies a lifecycle transition and persists it
// under the version guard.
func (s *OrderService) transition(
	ctx context.Context,
	op, rawID string,
	storedVersion *int64,
	apply func(domain.Order, time.Time) (domain.Order, error),
	state string,
) (OrderView, error) {
	log := s.loggerFor(ctx, op)

	order, id, err := s.loadForWrite(ctx, log, rawID, storedVersion)
	if err != nil {
		return OrderView{}, err
	}
	now := s.now().UTC()
	order, err = apply(order, now)
	if err != nil {
		log.Warn().Err(err).Msg("order transition rejected")
		return OrderView{}, err
	}
	order, err = s.orders.UpdateStatus(ctx, order, order.Version)
	if err != nil {
		log.Warn().Err(err).Msg("order transition failed")
		return OrderView{}, err
	}
	s.evict(ctx, log, id)
	view := NewOrderView(order)
	cacheWrite(ctx, s.cache, s.ttl, log, orderCacheKey(id), view)
	log.Info().Str("status", state).Int64("version", order.Version).Msg("order updated")
	return view, nil
}

// ReplaceOrderItems replaces the line list of a draft order.
func (s *OrderService) ReplaceOrderItems(ctx context.Context, rawID string, storedVersion *int64, in []domain.OrderItemInput) (OrderView, error) {
	log := s.loggerFor(ctx, "replace_order_items")

	items, err := domain.ParseOrderItems(in)
	if err != nil {
		log.Warn().Err(err).Msg("order item replacement rejected: invalid items")
		return OrderView{}, err
	}
	order, id, err := s.loadForWrite(ctx, log, rawID, storedVersion)
	if err != nil {
		return OrderView{}, err
	}
	order, err = order.ReplaceItems(items, s.now().UTC())
	if err != nil {
		log.Warn().Err(err).Msg("order item replacement rejected")
		return OrderView{}, err
	}
	order, err = s.orders.ReplaceItems(ctx, order, order.Version)
	if err != nil {
		log.Warn().Err(err).Msg("order item replacement failed")
		return OrderView{}, err
	}
	s.evict(ctx, log, id)
	view := NewOrderView(order)
	cacheWrite(ctx, s.cache, s.ttl, log, orderCacheKey(id), view)
	log.Info().Int("lines", len(order.Items)).Int64("total_cents", int64(order.Total())).Msg("order items replaced")
	return view, nil
}

// DeleteOrder removes a draft order. A placed or cancelled order is a record,
// not scratch data, so deleting it is a state conflict.
func (s *OrderService) DeleteOrder(ctx context.Context, rawID string, storedVersion *int64) error {
	log := s.loggerFor(ctx, "delete_order")

	order, id, err := s.loadForWrite(ctx, log, rawID, storedVersion)
	if err != nil {
		return err
	}
	if !order.CanDelete() {
		err := domain.StateConflictf("cannot delete an order in state %q; cancel it instead", order.Status)
		log.Warn().Err(err).Msg("order deletion rejected")
		return err
	}
	if err := s.orders.Delete(ctx, id, order.Version); err != nil {
		log.Warn().Err(err).Msg("order deletion failed")
		return err
	}
	s.evict(ctx, log, id)
	log.Info().Msg("order deleted")
	return nil
}

// loadForWrite resolves the identifier, reads the stored order and checks any
// caller-supplied version. The returned order's Version is the authoritative
// one the conditional write uses.
func (s *OrderService) loadForWrite(ctx context.Context, log zerolog.Logger, rawID string, storedVersion *int64) (domain.Order, domain.OrderID, error) {
	id, err := domain.ParseOrderID(rawID)
	if err != nil {
		log.Warn().Err(err).Str("raw_id", rawID).Msg("request rejected: invalid id")
		return domain.Order{}, "", err
	}
	log = log.With().Str("order_id", id.String()).Logger()

	order, err := s.orders.GetByID(ctx, id)
	if err != nil {
		log.Warn().Err(err).Msg("order lookup failed")
		return domain.Order{}, "", err
	}
	if storedVersion != nil && *storedVersion != order.Version {
		err := domain.VersionConflictf("order %s is at version %d, not %d", id, order.Version, *storedVersion)
		log.Warn().Err(err).Msg("request rejected: stale version")
		return domain.Order{}, "", err
	}
	return order, id, nil
}

// loggerFor builds the operation logger from the request context.
func (s *OrderService) loggerFor(ctx context.Context, op string) zerolog.Logger {
	return logger.FromContext(ctx, s.log).With().
		Str("layer", "usecase").
		Str("op", op).
		Logger()
}

// evict drops the cached copy after a write.
func (s *OrderService) evict(ctx context.Context, log zerolog.Logger, id domain.OrderID) {
	deleteCacheKeys(ctx, s.cache, log, orderCacheKey(id))
}

// orderCacheKey namespaces entries by schema version, like cacheKey does for
// users: a view change must not be served from stale JSON.
func orderCacheKey(id domain.OrderID) string { return "order:v1:" + id.String() }
