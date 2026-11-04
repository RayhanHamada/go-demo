package domain

import (
	"errors"
	"fmt"
)

// Sentinel errors the transport layer maps to protocol responses. Adapters
// return them (possibly wrapped) so callers never inspect driver errors.
var (
	ErrNotFound   = errors.New("resource not found")
	ErrEmailTaken = errors.New("email already registered")
	ErrInvalid    = errors.New("invalid argument")
	// ErrStateConflict reports an operation the resource's current state
	// forbids: an illegal order transition, editing a placed order, deleting a
	// user that still owns orders.
	ErrStateConflict = errors.New("operation not allowed in the current state")
	// ErrVersionConflict reports a write that raced a concurrent write: the
	// version the caller expected is no longer current.
	ErrVersionConflict = errors.New("resource was modified by another request")
	// ErrCustomerNotFound reports an order that names a customer which does
	// not exist.
	ErrCustomerNotFound = errors.New("customer not found")
)

// Field names a request attribute. It is a distinct type so a validation
// failure cannot be constructed with an arbitrary label.
type Field string

const (
	FieldID          Field = "id"
	FieldEmail       Field = "email"
	FieldDisplayName Field = "display_name"
	FieldStatus      Field = "status"
	FieldLimit       Field = "limit"
	FieldOffset      Field = "offset"
	FieldRequest     Field = "request"
	FieldCustomerID  Field = "customer_id"
	FieldCurrency    Field = "currency"
	FieldItems       Field = "items"
	FieldSKU         Field = "sku"
	FieldQuantity    Field = "quantity"
	FieldUnitPrice   Field = "unit_price_cents"
	FieldVersion     Field = "version"
)

// ValidationError reports which request field failed and why. errors.Is(err,
// ErrInvalid) is true for every ValidationError.
type ValidationError struct {
	Field  Field
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

func (e *ValidationError) Is(target error) bool { return target == ErrInvalid }

// Invalidf builds a ValidationError for field.
func Invalidf(field Field, format string, args ...any) error {
	return &ValidationError{Field: field, Reason: fmt.Sprintf(format, args...)}
}

// StateConflictf builds an error that satisfies errors.Is(err, ErrStateConflict).
func StateConflictf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrStateConflict, fmt.Sprintf(format, args...))
}

// VersionConflictf builds an error that satisfies errors.Is(err,
// ErrVersionConflict).
func VersionConflictf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrVersionConflict, fmt.Sprintf(format, args...))
}

// IsNotFound reports whether err is (or wraps) ErrNotFound.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
