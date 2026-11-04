// Package sqlite implements the usecase persistence port on SQLite through
// GORM. The package owns the database schema: the models below are the single
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

// OrderModel is the persisted shape of an order header. The version column
// backs optimistic concurrency: every write increments it and only succeeds
// when the caller names the version it read.
//
// Customer carries the foreign key to users with ON DELETE RESTRICT, so the
// database refuses to orphan an order even if application code forgets to
// check. Items carries the owning association used for preloading.
type OrderModel struct {
	ID         string    `gorm:"column:id;type:text;primaryKey"`
	Number     string    `gorm:"column:number;type:text;not null;uniqueIndex:ux_orders_number"`
	CustomerID string    `gorm:"column:customer_id;type:text;not null;index:ix_orders_customer_id"`
	Currency   string    `gorm:"column:currency;type:text;not null;check:ck_orders_currency,length(currency) = 3"`
	Status     string    `gorm:"column:status;type:text;not null;check:ck_orders_status,status IN ('draft','placed','cancelled');index:ix_orders_status"`
	Version    int64     `gorm:"column:version;type:integer;not null"`
	CreatedAt  time.Time `gorm:"column:created_at;type:datetime;not null;autoCreateTime:false;index:ix_orders_created_at,sort:desc,priority:1"`
	UpdatedAt  time.Time `gorm:"column:updated_at;type:datetime;not null;autoUpdateTime:false"`

	Customer UserModel        `gorm:"foreignKey:CustomerID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
	Items    []OrderItemModel `gorm:"foreignKey:OrderID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

// TableName pins the table name independently of GORM's pluralisation.
func (OrderModel) TableName() string { return "orders" }

// OrderItemModel is one persisted line. line_no preserves the order the
// client supplied; the (order_id, sku) unique index makes a duplicated line a
// database error rather than a silently double-charged order.
type OrderItemModel struct {
	OrderID        string `gorm:"column:order_id;type:text;primaryKey;uniqueIndex:ux_order_items_sku,priority:1;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	LineNo         int    `gorm:"column:line_no;type:integer;primaryKey"`
	SKU            string `gorm:"column:sku;type:text;not null;uniqueIndex:ux_order_items_sku,priority:2"`
	Quantity       int    `gorm:"column:quantity;type:integer;not null;check:ck_order_items_quantity,quantity >= 1 AND quantity <= 1000"`
	UnitPriceCents int64  `gorm:"column:unit_price_cents;type:integer;not null;check:ck_order_items_price,unit_price_cents >= 0 AND unit_price_cents <= 99999999"`
}

// TableName pins the table name independently of GORM's pluralisation.
func (OrderItemModel) TableName() string { return "order_items" }

// Models lists every model the schema consists of. Migrations run over this
// list, so adding a table means adding it here. Order precedes order_items so
// its foreign keys exist when the line table is created.
func Models() []any { return []any{&UserModel{}, &OrderModel{}, &OrderItemModel{}} }

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
func fromDomainUser(u domain.User) UserModel {
	return UserModel{
		ID:          u.ID.String(),
		Email:       u.Email.String(),
		DisplayName: u.DisplayName.String(),
		Status:      string(u.Status),
		CreatedAt:   u.CreatedAt.UTC(),
		UpdatedAt:   u.UpdatedAt.UTC(),
	}
}

// toDomain converts an order header and its preloaded lines.
func (m OrderModel) toDomain() domain.Order {
	items := make([]domain.OrderItem, len(m.Items))
	for i, row := range m.Items {
		items[i] = row.toDomain()
	}
	return domain.Order{
		ID:         domain.OrderID(m.ID),
		Number:     domain.OrderNumber(m.Number),
		CustomerID: domain.UserID(m.CustomerID),
		Currency:   domain.CurrencyCode(m.Currency),
		Status:     domain.OrderStatus(m.Status),
		Items:      items,
		Version:    m.Version,
		CreatedAt:  m.CreatedAt.UTC(),
		UpdatedAt:  m.UpdatedAt.UTC(),
	}
}

// fromDomain converts a domain order into a header row plus line rows.
func fromDomainOrder(o domain.Order) (OrderModel, []OrderItemModel) {
	header := OrderModel{
		ID:         o.ID.String(),
		Number:     o.Number.String(),
		CustomerID: o.CustomerID.String(),
		Currency:   o.Currency.String(),
		Status:     string(o.Status),
		Version:    o.Version,
		CreatedAt:  o.CreatedAt.UTC(),
		UpdatedAt:  o.UpdatedAt.UTC(),
	}
	items := make([]OrderItemModel, len(o.Items))
	for i, item := range o.Items {
		items[i] = OrderItemModel{
			OrderID:        o.ID.String(),
			LineNo:         i,
			SKU:            item.SKU.String(),
			Quantity:       int(item.Quantity),
			UnitPriceCents: int64(item.UnitPrice),
		}
	}
	return header, items
}

// toDomain converts a line row.
func (m OrderItemModel) toDomain() domain.OrderItem {
	return domain.OrderItem{
		SKU:       domain.SKU(m.SKU),
		Quantity:  domain.Quantity(m.Quantity),
		UnitPrice: domain.Cents(m.UnitPriceCents),
	}
}
