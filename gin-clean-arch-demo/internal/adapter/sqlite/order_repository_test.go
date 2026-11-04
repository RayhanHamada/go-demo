package sqlite_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gin-clean-arch-demo/internal/adapter/sqlite"
	"gin-clean-arch-demo/internal/domain"
)

// newCustomer inserts a user to own orders.
func newCustomer(t *testing.T, repo *sqlite.UserRepository, email string) domain.User {
	t.Helper()
	u, err := repo.Create(context.Background(),
		domain.NewUser(mustEmail(t, email), mustName(t, "Customer"), time.Now().UTC()))
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	return u
}

func mustItems(t *testing.T, raw []domain.OrderItemInput) []domain.OrderItem {
	t.Helper()
	items, err := domain.ParseOrderItems(raw)
	if err != nil {
		t.Fatalf("parse items: %v", err)
	}
	return items
}

func mustCurrency(t *testing.T, s string) domain.CurrencyCode {
	t.Helper()
	c, err := domain.ParseCurrency(s)
	if err != nil {
		t.Fatalf("parse currency: %v", err)
	}
	return c
}

func TestOrderRepositoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	users := sqlite.NewUserRepository(store)
	orders := sqlite.NewOrderRepository(store)
	customer := newCustomer(t, users, "order-round@example.com")
	now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

	items := mustItems(t, []domain.OrderItemInput{
		{SKU: "widget-01", Quantity: 2, UnitPrice: 1999},
		{SKU: "GADGET-02", Quantity: 1, UnitPrice: 500},
	})
	created, err := orders.Create(ctx, domain.NewOrder(customer.ID, mustCurrency(t, "usd"), items, now))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Version != 1 {
		t.Errorf("version = %d, want 1 on create", created.Version)
	}
	if created.Total() != 4498 {
		t.Errorf("total = %d, want 4498", created.Total())
	}
	if created.Currency != "USD" {
		t.Errorf("currency = %q, want normalised USD", created.Currency)
	}

	got, err := orders.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(got.Items))
	}
	// Line order must survive the round trip: the client's first line stays first.
	if got.Items[0].SKU != "WIDGET-01" || got.Items[1].SKU != "GADGET-02" {
		t.Errorf("line order = %v, want WIDGET-01 then GADGET-02", got.Items)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("created_at = %v, want %v", got.CreatedAt, now)
	}
	if got.Number.String() == "" {
		t.Error("order number is empty")
	}
}

// A concurrent writer must lose: the version guard is what makes ETag
// semantics real, so it is tested against the database, not a fake.
func TestOrderRepositoryRejectsStaleVersion(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	orders := sqlite.NewOrderRepository(store)
	customer := newCustomer(t, sqlite.NewUserRepository(store), "stale@example.com")

	items := mustItems(t, []domain.OrderItemInput{{SKU: "A1", Quantity: 1, UnitPrice: 100}})
	created, err := orders.Create(ctx, domain.NewOrder(customer.ID, mustCurrency(t, "USD"), items, time.Now().UTC()))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	placed, err := created.Place(time.Now().UTC())
	if err != nil {
		t.Fatalf("place in memory: %v", err)
	}
	updated, err := orders.UpdateStatus(ctx, placed, created.Version)
	if err != nil {
		t.Fatalf("first conditional write: %v", err)
	}
	if updated.Version != created.Version+1 {
		t.Errorf("version = %d, want %d", updated.Version, created.Version+1)
	}

	// A second writer still holding the original version must be refused.
	cancelled, err := updated.Cancel(time.Now().UTC())
	if err != nil {
		t.Fatalf("cancel in memory: %v", err)
	}
	if _, err := orders.UpdateStatus(ctx, cancelled, created.Version); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale write: err = %v, want ErrVersionConflict", err)
	}

	stored, err := orders.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Status != domain.OrderPlaced {
		t.Errorf("status = %q, want the first writer's value", stored.Status)
	}
}

func TestOrderRepositoryVersionConflictOnDeleteAndItems(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	orders := sqlite.NewOrderRepository(store)
	customer := newCustomer(t, sqlite.NewUserRepository(store), "stale-items@example.com")

	items := mustItems(t, []domain.OrderItemInput{{SKU: "A1", Quantity: 1, UnitPrice: 100}})
	created, err := orders.Create(ctx, domain.NewOrder(customer.ID, mustCurrency(t, "USD"), items, time.Now().UTC()))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	replacement := mustItems(t, []domain.OrderItemInput{{SKU: "B2", Quantity: 3, UnitPrice: 250}})
	next, err := created.ReplaceItems(replacement, time.Now().UTC())
	if err != nil {
		t.Fatalf("replace in memory: %v", err)
	}
	if _, err := orders.ReplaceItems(ctx, next, created.Version+99); !errors.Is(err, domain.ErrVersionConflict) {
		t.Errorf("stale replace: err = %v, want ErrVersionConflict", err)
	}
	if err := orders.Delete(ctx, created.ID, created.Version+99); !errors.Is(err, domain.ErrVersionConflict) {
		t.Errorf("stale delete: err = %v, want ErrVersionConflict", err)
	}
	// A stale write must not have partially applied: the original line stands.
	stored, err := orders.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(stored.Items) != 1 || stored.Items[0].SKU != "A1" {
		t.Errorf("items = %v, want the original single line", stored.Items)
	}
	if stored.Version != created.Version {
		t.Errorf("version = %d, want %d (no write should have landed)", stored.Version, created.Version)
	}
}

// Replacing items must be a replacement, not an append.
func TestOrderRepositoryReplaceItemsRewritesLines(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	orders := sqlite.NewOrderRepository(store)
	customer := newCustomer(t, sqlite.NewUserRepository(store), "replace@example.com")

	items := mustItems(t, []domain.OrderItemInput{
		{SKU: "OLD-1", Quantity: 1, UnitPrice: 100},
		{SKU: "OLD-2", Quantity: 1, UnitPrice: 100},
	})
	created, err := orders.Create(ctx, domain.NewOrder(customer.ID, mustCurrency(t, "USD"), items, time.Now().UTC()))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	replacement := mustItems(t, []domain.OrderItemInput{{SKU: "NEW-1", Quantity: 2, UnitPrice: 750}})
	next, err := created.ReplaceItems(replacement, time.Now().UTC())
	if err != nil {
		t.Fatalf("replace in memory: %v", err)
	}
	stored, err := orders.ReplaceItems(ctx, next, created.Version)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if len(stored.Items) != 1 || stored.Items[0].SKU != "NEW-1" {
		t.Errorf("items = %v, want exactly the replacement line", stored.Items)
	}
	if stored.Total() != 1500 {
		t.Errorf("total = %d, want 1500", stored.Total())
	}
	if stored.Version != created.Version+1 {
		t.Errorf("version = %d, want %d", stored.Version, created.Version+1)
	}
}

func TestOrderRepositoryMissingOrderAndCustomer(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	orders := sqlite.NewOrderRepository(store)
	items := mustItems(t, []domain.OrderItemInput{{SKU: "A1", Quantity: 1, UnitPrice: 100}})

	missing := domain.NewUserID()
	_, err := orders.Create(ctx, domain.NewOrder(missing, mustCurrency(t, "USD"), items, time.Now().UTC()))
	if !errors.Is(err, domain.ErrCustomerNotFound) {
		t.Errorf("create for unknown customer: err = %v, want ErrCustomerNotFound", err)
	}

	unknown := domain.NewOrderID()
	if _, err := orders.GetByID(ctx, unknown); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get unknown: err = %v, want ErrNotFound", err)
	}
	if err := orders.Delete(ctx, unknown, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("delete unknown: err = %v, want ErrNotFound", err)
	}
}

// The foreign key's ON DELETE RESTRICT is the database-level guarantee behind
// the service's check that a customer with orders cannot be removed.
func TestUserDeleteBlockedByOrders(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	users := sqlite.NewUserRepository(store)
	orders := sqlite.NewOrderRepository(store)
	customer := newCustomer(t, users, "blocked@example.com")

	items := mustItems(t, []domain.OrderItemInput{{SKU: "A1", Quantity: 1, UnitPrice: 100}})
	if _, err := orders.Create(ctx, domain.NewOrder(customer.ID, mustCurrency(t, "USD"), items, time.Now().UTC())); err != nil {
		t.Fatalf("create order: %v", err)
	}

	err := users.Delete(ctx, customer.ID)
	if !errors.Is(err, domain.ErrStateConflict) {
		t.Fatalf("delete customer with orders: err = %v, want ErrStateConflict", err)
	}
	if _, err := users.GetByID(ctx, customer.ID); err != nil {
		t.Errorf("customer was removed despite the restriction: %v", err)
	}
}

func TestOrderRepositoryListFilters(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	users := sqlite.NewUserRepository(store)
	orders := sqlite.NewOrderRepository(store)

	first := newCustomer(t, users, "list-a@example.com")
	second := newCustomer(t, users, "list-b@example.com")
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	items := mustItems(t, []domain.OrderItemInput{{SKU: "A1", Quantity: 1, UnitPrice: 100}})

	var placedID domain.OrderID
	for i, owner := range []domain.UserID{first.ID, first.ID, second.ID} {
		order := domain.NewOrder(owner, mustCurrency(t, "USD"), items, base.Add(time.Duration(i)*time.Minute))
		if i == 2 {
			var err error
			if order, err = order.Place(base); err != nil {
				t.Fatalf("place: %v", err)
			}
		}
		created, err := orders.Create(ctx, order)
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		placedID = created.ID
	}

	page, err := orders.List(ctx, domain.ListOrdersQuery{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Total != 3 || len(page.Items) != 3 {
		t.Fatalf("page = %+v, want 3 orders", page)
	}
	if page.Items[0].ID != placedID {
		t.Errorf("first item = %s, want the newest order", page.Items[0].ID)
	}

	byCustomer, err := orders.List(ctx, domain.ListOrdersQuery{CustomerID: &first.ID})
	if err != nil {
		t.Fatalf("list by customer: %v", err)
	}
	if byCustomer.Total != 2 {
		t.Errorf("customer total = %d, want 2", byCustomer.Total)
	}

	placed := domain.OrderPlaced
	byStatus, err := orders.List(ctx, domain.ListOrdersQuery{Status: &placed})
	if err != nil {
		t.Fatalf("list by status: %v", err)
	}
	if byStatus.Total != 1 || byStatus.Items[0].ID != placedID {
		t.Errorf("status filter = %+v, want only the placed order", byStatus)
	}

	// Filters combine rather than replacing each other.
	combined, err := orders.List(ctx, domain.ListOrdersQuery{Status: &placed, CustomerID: &first.ID})
	if err != nil {
		t.Fatalf("list combined: %v", err)
	}
	if combined.Total != 0 {
		t.Errorf("combined total = %d, want 0", combined.Total)
	}
}

// The schema constraints are the last line of defence behind domain
// validation, so they must exist after migration.
func TestOrderSchemaMatchesModels(t *testing.T) {
	store := newStore(t)

	var ddl string
	if err := store.DB().Raw(`SELECT COALESCE(group_concat(sql, ';'), '') FROM sqlite_master
		WHERE tbl_name IN ('orders','order_items') AND sql IS NOT NULL`).Scan(&ddl).Error; err != nil {
		t.Fatalf("read schema: %v", err)
	}
	for _, want := range []string{
		"CONSTRAINT `ck_orders_status` CHECK (status IN ('draft','placed','cancelled'))",
		"CONSTRAINT `ck_order_items_quantity` CHECK (quantity >= 1 AND quantity <= 1000)",
		"CONSTRAINT `ck_order_items_price` CHECK (unit_price_cents >= 0 AND unit_price_cents <= 99999999)",
		"CREATE UNIQUE INDEX `ux_orders_number`",
		"CREATE UNIQUE INDEX `ux_order_items_sku`",
		"CREATE INDEX `ix_orders_customer_id`",
		"ON DELETE RESTRICT",
		"ON DELETE CASCADE",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("schema missing %q\nschema: %s", want, ddl)
		}
	}

	if err := store.DB().AutoMigrate(sqlite.Models()...); err != nil {
		t.Fatalf("second migration: %v", err)
	}
}
