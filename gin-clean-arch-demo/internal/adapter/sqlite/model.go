// Package sqlite implements the usecase persistence port on SQLite through
// GORM. The package owns the database schema: the model below is the single
// source of truth, applied with GORM's migrator.
package sqlite

import (
	"time"

	"gin-clean-arch-demo/internal/domain"
)

// UserModel is the persisted shape of a user. Column names are explicit so a
// rename in Go cannot silently rewrite the schema, and timestamps are
// application-owned (autoCreateTime/autoUpdateTime off) so the domain decides
// when a write happened.
type UserModel struct {
	ID          string    `gorm:"column:id;type:text;primaryKey"`
	Email       string    `gorm:"column:email;type:text;not null;uniqueIndex:ux_users_email"`
	DisplayName string    `gorm:"column:display_name;type:text;not null"`
	Status      string    `gorm:"column:status;type:text;not null;check:ck_users_status,status IN ('active','suspended')"`
	CreatedAt   time.Time `gorm:"column:created_at;type:datetime;not null;autoCreateTime:false;index:ix_users_created_at,sort:desc,priority:1"`
	UpdatedAt   time.Time `gorm:"column:updated_at;type:datetime;not null;autoUpdateTime:false"`
}

// TableName pins the table name independently of GORM's pluralisation.
func (UserModel) TableName() string { return "users" }

// Models lists every model the schema consists of. Migrations run over this
// list, so adding a table means adding it here.
func Models() []any { return []any{&UserModel{}} }

// toDomain converts a row into the domain type.
func (m UserModel) toDomain() domain.User {
	return domain.User{
		ID:          domain.UserID(m.ID),
		Email:       domain.Email(m.Email),
		DisplayName: domain.DisplayName(m.DisplayName),
		Status:      domain.UserStatus(m.Status),
		CreatedAt:   m.CreatedAt.UTC(),
		UpdatedAt:   m.UpdatedAt.UTC(),
	}
}

// fromDomain converts a domain user into a row.
func fromDomain(u domain.User) UserModel {
	return UserModel{
		ID:          u.ID.String(),
		Email:       u.Email.String(),
		DisplayName: u.DisplayName.String(),
		Status:      string(u.Status),
		CreatedAt:   u.CreatedAt.UTC(),
		UpdatedAt:   u.UpdatedAt.UTC(),
	}
}
