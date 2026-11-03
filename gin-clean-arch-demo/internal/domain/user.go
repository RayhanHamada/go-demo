package domain

import (
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// UserID is an opaque user identifier. The zero value is invalid.
type UserID string

// NewUserID mints a time-ordered identifier.
func NewUserID() UserID { return UserID(uuid.Must(uuid.NewV7()).String()) }

// ParseUserID validates an externally supplied identifier.
func ParseUserID(s string) (UserID, error) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return "", Invalidf(FieldID, "must be a UUID")
	}
	return UserID(id.String()), nil
}

func (id UserID) String() string { return string(id) }

// Email is a normalised, validated email address.
type Email string

const maxEmailLen = 254

// ParseEmail trims, lowercases and validates an address.
func ParseEmail(s string) (Email, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", Invalidf(FieldEmail, "must not be empty")
	}
	if len(s) > maxEmailLen {
		return "", Invalidf(FieldEmail, "must be at most %d bytes", maxEmailLen)
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return "", Invalidf(FieldEmail, "must be a valid address")
	}
	return Email(s), nil
}

func (e Email) String() string { return string(e) }

// DisplayName is a human-facing name, 1 to 100 characters.
type DisplayName string

const maxDisplayNameRunes = 100

// ParseDisplayName validates a caller-supplied name.
func ParseDisplayName(s string) (DisplayName, error) {
	s = strings.TrimSpace(s)
	switch n := utf8.RuneCountInString(s); {
	case n == 0:
		return "", Invalidf(FieldDisplayName, "must not be empty")
	case n > maxDisplayNameRunes:
		return "", Invalidf(FieldDisplayName, "must be at most %d characters", maxDisplayNameRunes)
	}
	return DisplayName(s), nil
}

func (n DisplayName) String() string { return string(n) }

// UserStatus is the account lifecycle state. Only ParseUserStatus produces
// valid values from untrusted input.
type UserStatus string

const (
	StatusActive    UserStatus = "active"
	StatusSuspended UserStatus = "suspended"
)

// ParseUserStatus validates a status name.
func ParseUserStatus(s string) (UserStatus, error) {
	switch UserStatus(strings.ToLower(strings.TrimSpace(s))) {
	case StatusActive:
		return StatusActive, nil
	case StatusSuspended:
		return StatusSuspended, nil
	default:
		return "", Invalidf(FieldStatus, "must be %q or %q", StatusActive, StatusSuspended)
	}
}

// User is an account. Construct with NewUser so invariants hold.
type User struct {
	ID          UserID
	Email       Email
	DisplayName DisplayName
	Status      UserStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewUser builds a fresh active account stamped at now.
func NewUser(email Email, name DisplayName, now time.Time) User {
	return User{
		ID:          NewUserID(),
		Email:       email,
		DisplayName: name,
		Status:      StatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// Rename returns u with a new display name and refreshed UpdatedAt.
func (u User) Rename(name DisplayName, now time.Time) User {
	u.DisplayName = name
	u.UpdatedAt = now
	return u
}

// ChangeStatus returns u with a new status and refreshed UpdatedAt.
func (u User) ChangeStatus(status UserStatus, now time.Time) User {
	u.Status = status
	u.UpdatedAt = now
	return u
}

// Page is one slice of a larger result set.
type Page[T any] struct {
	Items  []T
	Total  int64
	Limit  int
	Offset int
}

// ListUsersQuery narrows a user listing. Zero Status means "any".
type ListUsersQuery struct {
	Status *UserStatus
	Limit  int
	Offset int
}

// Bounds for ListUsersQuery, also enforced by the SQLite adapter.
const (
	DefaultPageLimit = 20
	MaxPageLimit     = 100
)

// Normalize validates and fills defaults for the query.
func (q *ListUsersQuery) Normalize() error {
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
