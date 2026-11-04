package sqlite

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"gin-clean-arch-demo/internal/domain"
)

// UserRepository is the SQLite implementation of usecase.UserRepository.
type UserRepository struct {
	db *gorm.DB
}

// NewUserRepository returns a repository bound to s.
func NewUserRepository(s *Store) *UserRepository { return &UserRepository{db: s.db} }

// Create inserts u, reporting a duplicate address as domain.ErrEmailTaken.
func (r *UserRepository) Create(ctx context.Context, u domain.User) (domain.User, error) {
	row := fromDomainUser(u)
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return domain.User{}, fmt.Errorf("%w: %s", domain.ErrEmailTaken, u.Email)
		}
		return domain.User{}, fmt.Errorf("sqlite: insert user: %w", err)
	}
	return row.toDomain(), nil
}

// GetByID returns the user with id or domain.ErrNotFound.
func (r *UserRepository) GetByID(ctx context.Context, id domain.UserID) (domain.User, error) {
	var row UserModel
	err := r.db.WithContext(ctx).First(&row, "id = ?", id.String()).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.User{}, fmt.Errorf("%w: user %s", domain.ErrNotFound, id)
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("sqlite: select user: %w", err)
	}
	return row.toDomain(), nil
}

// Update persists display_name and status for an existing user.
func (r *UserRepository) Update(ctx context.Context, u domain.User) (domain.User, error) {
	res := r.db.WithContext(ctx).Model(&UserModel{}).
		Where("id = ?", u.ID.String()).
		Updates(map[string]any{
			"display_name": u.DisplayName.String(),
			"status":       string(u.Status),
			"updated_at":   u.UpdatedAt.UTC(),
		})
	if res.Error != nil {
		if errors.Is(res.Error, gorm.ErrDuplicatedKey) {
			return domain.User{}, fmt.Errorf("%w: %s", domain.ErrEmailTaken, u.Email)
		}
		return domain.User{}, fmt.Errorf("sqlite: update user: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domain.User{}, fmt.Errorf("%w: user %s", domain.ErrNotFound, u.ID)
	}
	return u, nil
}

// Delete removes the user with id or reports domain.ErrNotFound. A user that
// still owns orders is refused (domain.ErrStateConflict) by the foreign key's
// ON DELETE RESTRICT, so an order can never be orphaned.
func (r *UserRepository) Delete(ctx context.Context, id domain.UserID) error {
	res := r.db.WithContext(ctx).Delete(&UserModel{}, "id = ?", id.String())
	if res.Error != nil {
		if isForeignKeyViolation(res.Error) {
			return fmt.Errorf("%w: user %s still has orders", domain.ErrStateConflict, id)
		}
		return fmt.Errorf("sqlite: delete user: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: user %s", domain.ErrNotFound, id)
	}
	return nil
}

// List returns one page of users newest first, plus the total count.
func (r *UserRepository) List(ctx context.Context, q domain.ListUsersQuery) (domain.Page[domain.User], error) {
	if err := q.Normalize(); err != nil {
		return domain.Page[domain.User]{}, err
	}

	scope := r.db.WithContext(ctx).Model(&UserModel{})
	if q.Status != nil {
		scope = scope.Where("status = ?", string(*q.Status))
	}

	var total int64
	if err := scope.Count(&total).Error; err != nil {
		return domain.Page[domain.User]{}, fmt.Errorf("sqlite: count users: %w", err)
	}

	var rows []UserModel
	if err := scope.
		Order("created_at DESC, id DESC").
		Limit(q.Limit).
		Offset(q.Offset).
		Find(&rows).Error; err != nil {
		return domain.Page[domain.User]{}, fmt.Errorf("sqlite: list users: %w", err)
	}

	items := make([]domain.User, len(rows))
	for i, row := range rows {
		items[i] = row.toDomain()
	}
	return domain.Page[domain.User]{
		Items:  items,
		Total:  total,
		Limit:  q.Limit,
		Offset: q.Offset,
	}, nil
}
