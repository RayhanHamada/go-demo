package sqlite

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"gin-clean-arch-demo/internal/domain"
)

// OrderRepository is the SQLite implementation of usecase.OrderRepository.
// Every mutation is a single transaction guarded by the version the caller
// read, so two concurrent writers cannot both win.
type OrderRepository struct {
	db *gorm.DB
}

// NewOrderRepository returns a repository bound to s.
func NewOrderRepository(s *Store) *OrderRepository { return &OrderRepository{db: s.db} }

// Create inserts the order header and its lines atomically. A missing customer
// is reported as domain.ErrCustomerNotFound, which the foreign key enforces
// without a separate read.
func (r *OrderRepository) Create(ctx context.Context, o domain.Order) (domain.Order, error) {
	header, items := fromDomainOrder(o)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Customer", "Items").Create(&header).Error; err != nil {
			if isForeignKeyViolation(err) {
				return fmt.Errorf("%w: %s", domain.ErrCustomerNotFound, o.CustomerID)
			}
			return fmt.Errorf("sqlite: insert order: %w", err)
		}
		if len(items) == 0 {
			return nil
		}
		if err := tx.Create(&items).Error; err != nil {
			if isForeignKeyViolation(err) {
				return fmt.Errorf("%w: %s", domain.ErrCustomerNotFound, o.CustomerID)
			}
			return fmt.Errorf("sqlite: insert order items: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.Order{}, err
	}
	return r.GetByID(ctx, o.ID)
}

// GetByID returns the order with id and its lines in presentation order.
func (r *OrderRepository) GetByID(ctx context.Context, id domain.OrderID) (domain.Order, error) {
	var row OrderModel
	err := r.db.WithContext(ctx).
		Preload("Items", orderedLines).
		First(&row, "id = ?", id.String()).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Order{}, fmt.Errorf("%w: order %s", domain.ErrNotFound, id)
	}
	if err != nil {
		return domain.Order{}, fmt.Errorf("sqlite: select order: %w", err)
	}
	return row.toDomain(), nil
}

// UpdateStatus persists the lifecycle state and timestamps, bumping the
// version. It writes no lines, so a state transition cannot rewrite the order
// contents.
func (r *OrderRepository) UpdateStatus(ctx context.Context, o domain.Order, expectedVersion int64) (domain.Order, error) {
	res := r.db.WithContext(ctx).Model(&OrderModel{}).
		Where("id = ? AND version = ?", o.ID.String(), expectedVersion).
		Updates(map[string]any{
			"status":     string(o.Status),
			"version":    expectedVersion + 1,
			"updated_at": o.UpdatedAt.UTC(),
		})
	if res.Error != nil {
		return domain.Order{}, fmt.Errorf("sqlite: update order status: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domain.Order{}, r.explainMiss(ctx, r.db, o.ID, expectedVersion)
	}
	return r.GetByID(ctx, o.ID)
}

// ReplaceItems persists a new line list plus the header fields, atomically and
// only when the stored version still matches expectedVersion.
func (r *OrderRepository) ReplaceItems(ctx context.Context, o domain.Order, expectedVersion int64) (domain.Order, error) {
	_, items := fromDomainOrder(o)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&OrderModel{}).
			Where("id = ? AND version = ?", o.ID.String(), expectedVersion).
			Updates(map[string]any{
				"status":     string(o.Status),
				"version":    expectedVersion + 1,
				"updated_at": o.UpdatedAt.UTC(),
			})
		if res.Error != nil {
			return fmt.Errorf("sqlite: update order: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return r.explainMiss(ctx, tx, o.ID, expectedVersion)
		}
		if err := tx.Where("order_id = ?", o.ID.String()).Delete(&OrderItemModel{}).Error; err != nil {
			return fmt.Errorf("sqlite: delete order items: %w", err)
		}
		if len(items) == 0 {
			return nil
		}
		if err := tx.Create(&items).Error; err != nil {
			return fmt.Errorf("sqlite: insert order items: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.Order{}, err
	}
	return r.GetByID(ctx, o.ID)
}

// Delete removes a draft order and its lines. The version guard applies here
// too: a delete based on a stale read must not erase someone else's change.
func (r *OrderRepository) Delete(ctx context.Context, id domain.OrderID, expectedVersion int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND version = ?", id.String(), expectedVersion).Delete(&OrderModel{})
		if res.Error != nil {
			return fmt.Errorf("sqlite: delete order: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return r.explainMiss(ctx, tx, id, expectedVersion)
		}
		if err := tx.Where("order_id = ?", id.String()).Delete(&OrderItemModel{}).Error; err != nil {
			return fmt.Errorf("sqlite: delete order items: %w", err)
		}
		return nil
	})
}

// List returns one page of orders newest first, plus the total for the same
// filters.
func (r *OrderRepository) List(ctx context.Context, q domain.ListOrdersQuery) (domain.Page[domain.Order], error) {
	if err := q.Normalize(); err != nil {
		return domain.Page[domain.Order]{}, err
	}

	scope := r.db.WithContext(ctx).Model(&OrderModel{})
	if q.Status != nil {
		scope = scope.Where("status = ?", string(*q.Status))
	}
	if q.CustomerID != nil {
		scope = scope.Where("customer_id = ?", q.CustomerID.String())
	}

	var total int64
	if err := scope.Count(&total).Error; err != nil {
		return domain.Page[domain.Order]{}, fmt.Errorf("sqlite: count orders: %w", err)
	}

	var rows []OrderModel
	if err := scope.
		Preload("Items", orderedLines).
		Order("created_at DESC, id DESC").
		Limit(q.Limit).
		Offset(q.Offset).
		Find(&rows).Error; err != nil {
		return domain.Page[domain.Order]{}, fmt.Errorf("sqlite: list orders: %w", err)
	}

	items := make([]domain.Order, len(rows))
	for i, row := range rows {
		items[i] = row.toDomain()
	}
	return domain.Page[domain.Order]{
		Items:  items,
		Total:  total,
		Limit:  q.Limit,
		Offset: q.Offset,
	}, nil
}

// orderedLines preloads lines in the order the client supplied them.
func orderedLines(db *gorm.DB) *gorm.DB { return db.Order("line_no ASC") }

// explainMiss distinguishes "no such order" from "the version was stale", so
// callers can answer 404 and 412 respectively instead of conflating them.
func (r *OrderRepository) explainMiss(ctx context.Context, db *gorm.DB, id domain.OrderID, expectedVersion int64) error {
	var current int64
	err := db.WithContext(ctx).Model(&OrderModel{}).
		Where("id = ?", id.String()).
		Select("version").
		Scan(&current).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || current == 0 {
		return fmt.Errorf("%w: order %s", domain.ErrNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("sqlite: inspect order: %w", err)
	}
	return fmt.Errorf("%w: order %s at version %d, not %d", domain.ErrVersionConflict, id, current, expectedVersion)
}
