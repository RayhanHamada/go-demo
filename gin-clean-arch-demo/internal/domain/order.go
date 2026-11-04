package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// OrderID is an opaque order identifier. The zero value is invalid.
type OrderID string

// NewOrderID mints a time-ordered identifier.
func NewOrderID() OrderID { return OrderID(uuid.Must(uuid.NewV7()).String()) }

// ParseOrderID validates an externally supplied order identifier.
func ParseOrderID(s string) (OrderID, error) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return "", Invalidf(FieldID, "must be a UUID")
	}
	return OrderID(id.String()), nil
}

func (id OrderID) String() string { return string(id) }

// orderNumberPrefix keeps display numbers distinguishable from identifiers.
const orderNumberPrefix = "ORD-"

// OrderNumber is the human-facing order reference. It is derived from the
// identifier, so it is stable and unique in practice, but clients must key on
// OrderID.
type OrderNumber string

// NewOrderNumber derives a display number from id. It uses the whole
// identifier: a UUID v7's leading hex digits are its timestamp, so a shorter
// prefix would collide for two orders created in the same millisecond.
func NewOrderNumber(id OrderID) OrderNumber {
	compact := strings.ToUpper(strings.ReplaceAll(id.String(), "-", ""))
	return OrderNumber(orderNumberPrefix + compact)
}

func (n OrderNumber) String() string { return string(n) }

// CurrencyCode is an ISO 4217-shaped code: exactly three upper-case letters.
// The service does no conversion, so an order's currency is fixed at creation.
type CurrencyCode string

var currencyShape = regexp.MustCompile(`^[A-Z]{3}$`)

// ParseCurrency validates a caller-supplied currency code.
func ParseCurrency(s string) (CurrencyCode, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if !currencyShape.MatchString(s) {
		return "", Invalidf(FieldCurrency, "must be a three-letter code such as USD")
	}
	return CurrencyCode(s), nil
}

func (c CurrencyCode) String() string { return string(c) }

// SKU identifies a sellable item. It is normalised to upper case so lookups
// are case-insensitive, and duplicates within one order are rejected.
type SKU string

const maxSKULen = 40

var skuShape = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]*$`)

// ParseSKU validates and normalises a caller-supplied SKU.
func ParseSKU(s string) (SKU, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	switch {
	case s == "":
		return "", Invalidf(FieldSKU, "must not be empty")
	case len(s) > maxSKULen:
		return "", Invalidf(FieldSKU, "must be at most %d characters", maxSKULen)
	case !skuShape.MatchString(s):
		return "", Invalidf(FieldSKU, "must start with a letter or digit and contain only letters, digits, '.', '_' or '-'")
	}
	return SKU(s), nil
}

func (s SKU) String() string { return string(s) }

// Quantity is a positive number of units. It is an int so a caller cannot
// smuggle in a fraction or a float rounding artefact.
type Quantity int

// Bounds for Quantity, also enforced by the SQLite CHECK constraint.
const (
	MinQuantity = 1
	MaxQuantity = 1000
)

// ParseQuantity validates a caller-supplied quantity.
func ParseQuantity(n int) (Quantity, error) {
	if n < MinQuantity || n > MaxQuantity {
		return 0, Invalidf(FieldQuantity, "must be between %d and %d", MinQuantity, MaxQuantity)
	}
	return Quantity(n), nil
}

// Cents is an amount of minor currency units. Money is never a float: the
// whole price path is integer arithmetic, so totals are exact and comparable.
type Cents int64

// MaxUnitPriceCents bounds a single unit price so a line total and an order
// total stay far away from int64 overflow.
const MaxUnitPriceCents Cents = 99_999_999

// ParseCents validates a caller-supplied unit price in minor units.
func ParseCents(n int64) (Cents, error) {
	if n < 0 {
		return 0, Invalidf(FieldUnitPrice, "must not be negative")
	}
	if n > int64(MaxUnitPriceCents) {
		return 0, Invalidf(FieldUnitPrice, "must be at most %d", MaxUnitPriceCents)
	}
	return Cents(n), nil
}

// OrderStatus is the order lifecycle state. Only ParseOrderStatus produces
// valid values from untrusted input.
type OrderStatus string

const (
	OrderDraft     OrderStatus = "draft"
	OrderPlaced    OrderStatus = "placed"
	OrderCancelled OrderStatus = "cancelled"
)

// OrderStatuses lists every valid state, in lifecycle order.
var OrderStatuses = []OrderStatus{OrderDraft, OrderPlaced, OrderCancelled}

// ParseOrderStatus validates a status name.
func ParseOrderStatus(s string) (OrderStatus, error) {
	switch v := OrderStatus(strings.ToLower(strings.TrimSpace(s))); v {
	case OrderDraft, OrderPlaced, OrderCancelled:
		return v, nil
	default:
		return "", Invalidf(FieldStatus, "must be one of %q, %q or %q", OrderDraft, OrderPlaced, OrderCancelled)
	}
}

// OrderItem is one line of an order. Quantity and unit price are fixed once
// the line is constructed, so a line total cannot be inconsistent.
type OrderItem struct {
	SKU       SKU
	Quantity  Quantity
	UnitPrice Cents
}

// LineTotal is quantity × unit price. Both factors are bounded by Parse*, so
// the product cannot overflow.
func (i OrderItem) LineTotal() Cents { return Cents(int64(i.Quantity) * int64(i.UnitPrice)) }

// MaxOrderItems bounds the number of lines in a single order.
const MaxOrderItems = 100

// Order is an aggregate: its items are only reachable through the order, and
// every mutation goes through a method that enforces the lifecycle rules.
type Order struct {
	ID         OrderID
	Number     OrderNumber
	CustomerID UserID
	Currency   CurrencyCode
	Status     OrderStatus
	Items      []OrderItem
	// Version increments on every persisted change and backs optimistic
	// concurrency: a write that names a stale version is rejected.
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewOrder builds a draft order for customer with a validated item list.
func NewOrder(customer UserID, currency CurrencyCode, items []OrderItem, now time.Time) Order {
	id := NewOrderID()
	return Order{
		ID:         id,
		Number:     NewOrderNumber(id),
		CustomerID: customer,
		Currency:   currency,
		Status:     OrderDraft,
		Items:      items,
		Version:    1,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

// Total is the sum of every line total.
func (o Order) Total() Cents {
	var total Cents
	for _, item := range o.Items {
		total += item.LineTotal()
	}
	return total
}

// CanEditItems reports whether the line list may still change.
func (o Order) CanEditItems() bool { return o.Status == OrderDraft }

// ReplaceItems returns o with a new line list. Draft orders only: a placed
// order's contents are what the customer agreed to.
func (o Order) ReplaceItems(items []OrderItem, now time.Time) (Order, error) {
	if !o.CanEditItems() {
		return Order{}, illegalTransition(o.Status, "replace items")
	}
	o.Items = items
	return o.touched(now), nil
}

// Place returns o in the placed state. Placing is a one-way transition from
// draft; placing twice is a conflict rather than a no-op so a retried request
// cannot be mistaken for a second successful placement.
func (o Order) Place(now time.Time) (Order, error) {
	if o.Status != OrderDraft {
		return Order{}, illegalTransition(o.Status, "place")
	}
	o.Status = OrderPlaced
	return o.touched(now), nil
}

// Cancel returns o in the cancelled state. Both draft and placed orders can be
// cancelled; a cancelled order is terminal.
func (o Order) Cancel(now time.Time) (Order, error) {
	if o.Status == OrderCancelled {
		return Order{}, illegalTransition(o.Status, "cancel")
	}
	o.Status = OrderCancelled
	return o.touched(now), nil
}

// CanDelete reports whether an order may be removed. Only drafts: erasing a
// placed order would destroy a record of what the customer bought. Cancel it
// instead.
func (o Order) CanDelete() bool { return o.Status == OrderDraft }

// touched stamps a mutation.
func (o Order) touched(now time.Time) Order {
	o.UpdatedAt = now
	return o
}

// illegalTransition builds the conflict error for an operation the current
// state forbids.
func illegalTransition(status OrderStatus, op string) error {
	return fmt.Errorf("%w: cannot perform %q on an order in state %q", ErrStateConflict, op, status)
}

// ParseOrderItems validates a whole line list at once: at most MaxOrderItems
// lines, no duplicate SKU, and every SKU reported by its position so a client
// can point at the offending entry.
func ParseOrderItems(raw []OrderItemInput) ([]OrderItem, error) {
	if len(raw) == 0 {
		return nil, Invalidf(FieldItems, "must contain at least one line")
	}
	if len(raw) > MaxOrderItems {
		return nil, Invalidf(FieldItems, "must contain at most %d lines", MaxOrderItems)
	}
	items := make([]OrderItem, 0, len(raw))
	seen := make(map[SKU]int, len(raw))
	for i, in := range raw {
		sku, err := ParseSKU(in.SKU)
		if err != nil {
			return nil, itemError(i, err)
		}
		if first, dup := seen[sku]; dup {
			return nil, itemError(i, Invalidf(FieldSKU, "duplicates line %d", first))
		}
		seen[sku] = i
		qty, err := ParseQuantity(in.Quantity)
		if err != nil {
			return nil, itemError(i, err)
		}
		price, err := ParseCents(in.UnitPrice)
		if err != nil {
			return nil, itemError(i, err)
		}
		items = append(items, OrderItem{SKU: sku, Quantity: qty, UnitPrice: price})
	}
	return items, nil
}

// OrderItemInput carries one untrusted line, validated by ParseOrderItems.
type OrderItemInput struct {
	SKU       string
	Quantity  int
	UnitPrice int64
}

// itemError prefixes a validation failure with the line position, keeping the
// failing field so the transport layer can still name it.
func itemError(index int, err error) error {
	var ve *ValidationError
	if !errors.As(err, &ve) {
		return err
	}
	return &ValidationError{Field: ve.Field, Reason: fmt.Sprintf("items[%d]: %s", index, ve.Reason)}
}

// ListOrdersQuery narrows an order listing. Zero Status and nil CustomerID
// mean "any".
type ListOrdersQuery struct {
	Status     *OrderStatus
	CustomerID *UserID
	Limit      int
	Offset     int
}

// Normalize validates and fills defaults for the query.
func (q *ListOrdersQuery) Normalize() error {
	if q.Limit == 0 {
		q.Limit = DefaultPageLimit
	}
	if q.Limit < 1 || q.Limit > MaxPageLimit {
		return Invalidf(FieldLimit, "must be between 1 and %d", MaxPageLimit)
	}
	if q.Offset < 0 {
		return Invalidf(FieldOffset, "must not be negative")
	}
	return nil
}
