package usecase_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/rs/zerolog"

	"gin-clean-arch-demo/internal/adapter/cache"
	"gin-clean-arch-demo/internal/adapter/sqlite"
	"gin-clean-arch-demo/internal/domain"
	"gin-clean-arch-demo/internal/usecase"
)

// orderFixture is the order stack plus a real user service, so tests can drive
// customer state through the HTTP-facing API rather than inserting rows.
type orderFixture struct {
	users  *usecase.UserService
	orders *usecase.OrderService
}

func newOrderFixture(t *testing.T) orderFixture {
	t.Helper()
	ctx := context.Background()

	store, err := sqlite.Open(ctx, sqlite.Config{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	redis := miniredis.RunT(t)
	cacheStore, err := cache.New(ctx, cache.Config{Addr: redis.Addr()})
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	t.Cleanup(func() { _ = cacheStore.Close() })

	userRepo := sqlite.NewUserRepository(store)
	log := zerolog.Nop()
	ttl := time.Minute
	return orderFixture{
		users:  usecase.NewUserService(userRepo, cacheStore, log, ttl),
		orders: usecase.NewOrderService(sqlite.NewOrderRepository(store), userRepo, cacheStore, log, ttl),
	}
}

func (f orderFixture) customer(t *testing.T, email string) usecase.UserView {
	t.Helper()
	u, err := f.users.CreateUser(context.Background(), usecase.CreateUserInput{Email: email, DisplayName: "Customer"})
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	return u
}

func (f orderFixture) draft(t *testing.T, customerID string) usecase.OrderView {
	t.Helper()
	o, err := f.orders.CreateOrder(context.Background(), usecase.CreateOrderInput{
		CustomerID: customerID,
		Currency:   "usd",
		Items:      []domain.OrderItemInput{{SKU: "WIDGET-01", Quantity: 2, UnitPrice: 1999}},
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	return o
}

func TestCreateOrderComputesTotals(t *testing.T) {
	ctx := context.Background()
	f := newOrderFixture(t)
	customer := f.customer(t, "totals@example.com")

	view, err := f.orders.CreateOrder(ctx, usecase.CreateOrderInput{
		CustomerID: customer.ID.String(),
		Currency:   "eur",
		Items: []domain.OrderItemInput{
			{SKU: "widget-01", Quantity: 3, UnitPrice: 1250},
			{SKU: "gadget-02", Quantity: 1, UnitPrice: 99},
		},
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	if view.TotalCents != 3*1250+99 {
		t.Errorf("total = %d, want %d", view.TotalCents, 3*1250+99)
	}
	if view.Items[0].LineTotal != 3750 {
		t.Errorf("line total = %d, want 3750", view.Items[0].LineTotal)
	}
	if view.Items[0].SKU != "WIDGET-01" {
		t.Errorf("sku = %q, want normalised upper case", view.Items[0].SKU)
	}
	if view.Currency != "EUR" {
		t.Errorf("currency = %q, want normalised EUR", view.Currency)
	}
	if view.Status != domain.OrderDraft || view.Version != 1 {
		t.Errorf("order = %+v, want a draft at version 1", view)
	}
}

// The checkout gate: a customer that is not active cannot start an order.
func TestCreateOrderRejectsInactiveCustomer(t *testing.T) {
	ctx := context.Background()
	f := newOrderFixture(t)
	customer := f.customer(t, "suspended@example.com")

	suspended := string(domain.StatusSuspended)
	if _, err := f.users.UpdateUser(ctx, customer.ID.String(), usecase.UpdateUserInput{Status: &suspended}); err != nil {
		t.Fatalf("suspend customer: %v", err)
	}

	in := usecase.CreateOrderInput{
		CustomerID: customer.ID.String(),
		Currency:   "USD",
		Items:      []domain.OrderItemInput{{SKU: "A1", Quantity: 1, UnitPrice: 100}},
	}
	if _, err := f.orders.CreateOrder(ctx, in); !errors.Is(err, domain.ErrStateConflict) {
		t.Fatalf("err = %v, want ErrStateConflict", err)
	}

	// Reactivating the account allows the same request through, so the gate is
	// the status and not some sticky failure.
	active := string(domain.StatusActive)
	if _, err := f.users.UpdateUser(ctx, customer.ID.String(), usecase.UpdateUserInput{Status: &active}); err != nil {
		t.Fatalf("reactivate customer: %v", err)
	}
	if _, err := f.orders.CreateOrder(ctx, in); err != nil {
		t.Fatalf("create after reactivation: %v", err)
	}
}

func TestCreateOrderRejectsUnknownCustomer(t *testing.T) {
	f := newOrderFixture(t)
	_, err := f.orders.CreateOrder(context.Background(), usecase.CreateOrderInput{
		CustomerID: domain.NewUserID().String(),
		Currency:   "USD",
		Items:      []domain.OrderItemInput{{SKU: "A1", Quantity: 1, UnitPrice: 100}},
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCreateOrderValidatesInput(t *testing.T) {
	f := newOrderFixture(t)
	customer := f.customer(t, "validate@example.com")
	valid := []domain.OrderItemInput{{SKU: "A1", Quantity: 1, UnitPrice: 100}}

	cases := []struct {
		name string
		in   usecase.CreateOrderInput
	}{
		{"no items", usecase.CreateOrderInput{CustomerID: customer.ID.String(), Currency: "USD"}},
		{"bad currency", usecase.CreateOrderInput{CustomerID: customer.ID.String(), Currency: "us", Items: valid}},
		{"zero quantity", usecase.CreateOrderInput{CustomerID: customer.ID.String(), Currency: "USD", Items: []domain.OrderItemInput{{SKU: "A1", Quantity: 0, UnitPrice: 1}}}},
		{"negative price", usecase.CreateOrderInput{CustomerID: customer.ID.String(), Currency: "USD", Items: []domain.OrderItemInput{{SKU: "A1", Quantity: 1, UnitPrice: -1}}}},
		{"duplicate sku", usecase.CreateOrderInput{CustomerID: customer.ID.String(), Currency: "USD", Items: []domain.OrderItemInput{
			{SKU: "A1", Quantity: 1, UnitPrice: 1},
			{SKU: "a1", Quantity: 1, UnitPrice: 1},
		}}},
		{"bad sku", usecase.CreateOrderInput{CustomerID: customer.ID.String(), Currency: "USD", Items: []domain.OrderItemInput{{SKU: " bad sku", Quantity: 1, UnitPrice: 1}}}},
		{"bad customer id", usecase.CreateOrderInput{CustomerID: "nope", Currency: "USD", Items: valid}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.orders.CreateOrder(context.Background(), tc.in); !errors.Is(err, domain.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

// The lifecycle rules, driven through the service: place is one-way, cancel is
// terminal, and contents freeze on placement.
func TestOrderLifecycleRules(t *testing.T) {
	ctx := context.Background()
	f := newOrderFixture(t)
	customer := f.customer(t, "lifecycle@example.com")
	order := f.draft(t, customer.ID.String())

	placed, err := f.orders.PlaceOrder(ctx, order.ID.String(), nil)
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	if placed.Status != domain.OrderPlaced || placed.Version != order.Version+1 {
		t.Fatalf("placed = %+v, want placed at version %d", placed, order.Version+1)
	}

	if _, err := f.orders.PlaceOrder(ctx, order.ID.String(), nil); !errors.Is(err, domain.ErrStateConflict) {
		t.Errorf("place twice: err = %v, want ErrStateConflict", err)
	}
	if _, err := f.orders.ReplaceOrderItems(ctx, order.ID.String(), nil,
		[]domain.OrderItemInput{{SKU: "B2", Quantity: 1, UnitPrice: 1}}); !errors.Is(err, domain.ErrStateConflict) {
		t.Errorf("edit placed order: err = %v, want ErrStateConflict", err)
	}
	if err := f.orders.DeleteOrder(ctx, order.ID.String(), nil); !errors.Is(err, domain.ErrStateConflict) {
		t.Errorf("delete placed order: err = %v, want ErrStateConflict", err)
	}

	cancelled, err := f.orders.CancelOrder(ctx, order.ID.String(), nil)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != domain.OrderCancelled {
		t.Errorf("status = %q, want cancelled", cancelled.Status)
	}
	if _, err := f.orders.CancelOrder(ctx, order.ID.String(), nil); !errors.Is(err, domain.ErrStateConflict) {
		t.Errorf("cancel twice: err = %v, want ErrStateConflict", err)
	}
	if _, err := f.orders.PlaceOrder(ctx, order.ID.String(), nil); !errors.Is(err, domain.ErrStateConflict) {
		t.Errorf("place cancelled: err = %v, want ErrStateConflict", err)
	}
}

func TestDraftOrderCanBeDeleted(t *testing.T) {
	ctx := context.Background()
	f := newOrderFixture(t)
	customer := f.customer(t, "draft-delete@example.com")
	order := f.draft(t, customer.ID.String())

	if err := f.orders.DeleteOrder(ctx, order.ID.String(), nil); err != nil {
		t.Fatalf("delete draft: %v", err)
	}
	if _, err := f.orders.GetOrder(ctx, order.ID.String()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get after delete: err = %v, want ErrNotFound", err)
	}
}

// A stale ETag must lose the write, and a fresh one must win: this is the
// contract the HTTP layer exposes as If-Match/412.
func TestStaleVersionRejected(t *testing.T) {
	ctx := context.Background()
	f := newOrderFixture(t)
	customer := f.customer(t, "stale-etag@example.com")
	order := f.draft(t, customer.ID.String())
	stale := order.Version

	if _, err := f.orders.CancelOrder(ctx, order.ID.String(), &stale); err != nil {
		t.Fatalf("first conditional cancel: %v", err)
	}
	// The caller still holding the original ETag tries to place.
	if _, err := f.orders.PlaceOrder(ctx, order.ID.String(), &stale); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale place: err = %v, want ErrVersionConflict", err)
	}

	current := stale + 1
	if _, err := f.orders.PlaceOrder(ctx, order.ID.String(), &current); !errors.Is(err, domain.ErrStateConflict) {
		t.Errorf("fresh-version place on a cancelled order: err = %v, want ErrStateConflict", err)
	}
}

// Reads must be cached and writes must refresh the cached copy, otherwise a
// client sees its own update disappear.
func TestOrderCacheRefreshedOnWrite(t *testing.T) {
	ctx := context.Background()
	f := newOrderFixture(t)
	customer := f.customer(t, "cache-order@example.com")
	order := f.draft(t, customer.ID.String())

	if _, err := f.orders.GetOrder(ctx, order.ID.String()); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	if _, err := f.orders.ReplaceOrderItems(ctx, order.ID.String(), nil,
		[]domain.OrderItemInput{{SKU: "NEW-1", Quantity: 1, UnitPrice: 4200}}); err != nil {
		t.Fatalf("replace items: %v", err)
	}
	read, err := f.orders.GetOrder(ctx, order.ID.String())
	if err != nil {
		t.Fatalf("get after write: %v", err)
	}
	if read.TotalCents != 4200 || len(read.Items) != 1 || read.Items[0].SKU != "NEW-1" {
		t.Errorf("cached read = %+v, want the replacement line", read)
	}
}

func TestListOrdersFilters(t *testing.T) {
	ctx := context.Background()
	f := newOrderFixture(t)
	first := f.customer(t, "filter-a@example.com")
	second := f.customer(t, "filter-b@example.com")
	f.draft(t, first.ID.String())
	placed := f.draft(t, first.ID.String())
	f.draft(t, second.ID.String())
	if _, err := f.orders.PlaceOrder(ctx, placed.ID.String(), nil); err != nil {
		t.Fatalf("place: %v", err)
	}

	page, err := f.orders.ListOrders(ctx, domain.ListOrdersQuery{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Total != 3 {
		t.Errorf("total = %d, want 3", page.Total)
	}

	status := domain.OrderPlaced
	byStatus, err := f.orders.ListOrders(ctx, domain.ListOrdersQuery{Status: &status})
	if err != nil {
		t.Fatalf("list by status: %v", err)
	}
	if byStatus.Total != 1 || byStatus.Items[0].ID != placed.ID {
		t.Errorf("status filter = %+v, want only %s", byStatus, placed.ID)
	}

	owner := first.ID
	byCustomer, err := f.orders.ListOrders(ctx, domain.ListOrdersQuery{CustomerID: &owner})
	if err != nil {
		t.Fatalf("list by customer: %v", err)
	}
	if byCustomer.Total != 2 {
		t.Errorf("customer total = %d, want 2", byCustomer.Total)
	}
}
