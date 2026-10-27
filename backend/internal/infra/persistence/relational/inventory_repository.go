package relational

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

const defaultInventoryGroupName = "Default"

func (d *Database) ensureDefaultInventoryGroup(ctx context.Context) error {
	var count int64
	if err := d.db.WithContext(ctx).Model(&inventoryGroupModel{}).Where("name = ?", defaultInventoryGroupName).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return d.db.WithContext(ctx).Create(&inventoryGroupModel{Name: defaultInventoryGroupName, CreatedAt: time.Now().UTC()}).Error
}

func (r *AccountRepository) ListInventoryGroups(ctx context.Context) ([]repository.InventoryGroup, error) {
	var rows []inventoryGroupModel
	if err := r.db.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]repository.InventoryGroup, 0, len(rows))
	for _, row := range rows {
		out = append(out, repository.InventoryGroup{ID: row.ID, Name: row.Name})
	}
	return out, nil
}

func (r *AccountRepository) CreateInventoryGroup(ctx context.Context, name string) (repository.InventoryGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return repository.InventoryGroup{}, repository.ErrInvalidRecord
	}
	row := inventoryGroupModel{Name: name, CreatedAt: time.Now().UTC()}
	if err := r.db.db.WithContext(ctx).Create(&row).Error; err != nil {
		return repository.InventoryGroup{}, mapError(err)
	}
	return repository.InventoryGroup{ID: row.ID, Name: row.Name}, nil
}

func (r *AccountRepository) InventoryGroupByName(ctx context.Context, name string) (repository.InventoryGroup, error) {
	var row inventoryGroupModel
	err := r.db.db.WithContext(ctx).Where("name = ?", strings.TrimSpace(name)).Take(&row).Error
	if err != nil {
		return repository.InventoryGroup{}, mapError(err)
	}
	return repository.InventoryGroup{ID: row.ID, Name: row.Name}, nil
}

// DeleteInventoryGroup removes an empty group. Default cannot be deleted.
// A group that still holds accounts is left unchanged.
func (r *AccountRepository) DeleteInventoryGroup(ctx context.Context, id uint64) error {
	if id == 0 {
		return repository.ErrInvalidRecord
	}
	return r.db.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row inventoryGroupModel
		if err := tx.Where("id = ?", id).Take(&row).Error; err != nil {
			return mapError(err)
		}
		if row.Name == defaultInventoryGroupName {
			return repository.ErrInvalidRecord
		}
		var count int64
		if err := tx.Model(&accountModel{}).Where("inventory_group_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return repository.ErrConflict
		}
		return tx.Delete(&inventoryGroupModel{}, id).Error
	})
}

// StockAccounts puts accounts that are not already stocked into the group.
// Accounts already in a group keep that group.
func (r *AccountRepository) StockAccounts(ctx context.Context, ids []uint64, groupID uint64, now time.Time) error {
	if len(ids) == 0 || groupID == 0 {
		return repository.ErrInvalidRecord
	}
	result := r.db.db.WithContext(ctx).Model(&accountModel{}).
		Where("id IN ? AND inventory_group_id IS NULL", ids).
		Updates(map[string]any{"inventory_group_id": groupID, "stocked_at": now.UTC(), "updated_at": now.UTC()})
	if result.Error != nil {
		return result.Error
	}
	r.notifyInventoryChanged(ctx, ids)
	return nil
}

// UnstockAccounts returns stocked accounts to the schedulable pool.
func (r *AccountRepository) UnstockAccounts(ctx context.Context, ids []uint64, now time.Time) error {
	if len(ids) == 0 {
		return repository.ErrInvalidRecord
	}
	result := r.db.db.WithContext(ctx).Model(&accountModel{}).
		Where("id IN ? AND inventory_group_id IS NOT NULL", ids).
		Updates(map[string]any{"inventory_group_id": nil, "stocked_at": nil, "updated_at": now.UTC()})
	if result.Error != nil {
		return result.Error
	}
	r.notifyInventoryChanged(ctx, ids)
	return nil
}

func (r *AccountRepository) SetQualityVerdict(ctx context.Context, id uint64, verdict string) error {
	if verdict != "ok" && verdict != "degraded" {
		return repository.ErrInvalidRecord
	}
	result := r.db.db.WithContext(ctx).Model(&accountModel{}).Where("id = ?", id).
		Update("quality_verdict", verdict)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (r *AccountRepository) notifyInventoryChanged(ctx context.Context, ids []uint64) {
	var providers []string
	if err := r.db.db.WithContext(ctx).Model(&accountModel{}).Where("id IN ?", ids).Distinct().Pluck("provider", &providers).Error; err != nil {
		return
	}
	for _, provider := range providers {
		r.notifyInvalidation(ctx, repository.InvalidationEvent{
			Kind: repository.InvalidationAccountStateChanged, Provider: account.Provider(provider),
		})
	}
}
