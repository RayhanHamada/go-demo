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
