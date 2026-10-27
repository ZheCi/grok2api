package relational

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

func TestInventoryHidesStockedAccountsFromScheduling(t *testing.T) {
	ctx := context.Background()
	database, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "inventory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewAccountRepository(database)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	open := accountModel{IdentityKey: testIdentityKey("inventory-open"), Provider: "grok_build", Name: "open", SourceKey: "inventory-open", Enabled: true, AuthStatus: "active", Priority: 1, CreatedAt: now, UpdatedAt: now}
	held := accountModel{IdentityKey: testIdentityKey("inventory-held"), Provider: "grok_build", Name: "held", SourceKey: "inventory-held", Enabled: true, AuthStatus: "active", Priority: 1, CreatedAt: now, UpdatedAt: now}
	for _, row := range []*accountModel{&open, &held} {
		if err := database.db.WithContext(ctx).Create(row).Error; err != nil {
			t.Fatal(err)
		}
		if err := database.db.WithContext(ctx).Create(&accountCredentialModel{AccountID: row.ID, AuthType: "oauth", EncryptedPrimary: testEncryptedToken, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	groups, err := repo.ListInventoryGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].Name != "Default" {
		t.Fatalf("groups = %#v, err = %v", groups, err)
	}
	if err := repo.StockAccounts(ctx, []uint64{held.ID}, groups[0].ID, now); err != nil {
		t.Fatal(err)
	}
	other, err := repo.CreateInventoryGroup(ctx, "备用")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.StockAccounts(ctx, []uint64{held.ID}, other.ID, now); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.Get(ctx, held.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.InventoryGroupID != groups[0].ID || stored.QualityVerdict != "" {
		t.Fatalf("restock changed group or verdict: %#v", stored.InventoryGroupID)
	}
	visible, _, err := repo.List(ctx, repository.AccountListQuery{Page: repository.PageQuery{Limit: 20}, Filter: repository.AccountListFilter{Provider: string(account.ProviderBuild)}})
	if err != nil || len(visible) != 1 || visible[0].ID != open.ID {
		t.Fatalf("accounts page = %#v, err = %v", idsOf(visible), err)
	}
	stocked, _, err := repo.List(ctx, repository.AccountListQuery{Page: repository.PageQuery{Limit: 20}, Filter: repository.AccountListFilter{Inventory: "stocked"}})
	if err != nil || len(stocked) != 1 || stocked[0].ID != held.ID {
		t.Fatalf("inventory page = %#v, err = %v", idsOf(stocked), err)
	}
	bases, err := repo.ListRoutingAccountBases(ctx, account.ProviderBuild, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range bases {
		if base.Credential.ID == held.ID {
			t.Fatal("stocked account is still routable")
		}
	}
	enabledIDs, err := repo.ListEnabledAccountIDs(ctx, account.ProviderBuild, false)
	if err != nil || len(enabledIDs) != 1 || enabledIDs[0] != open.ID {
		t.Fatalf("enabled ids = %#v, err = %v", enabledIDs, err)
	}
	if err := repo.SetQualityVerdict(ctx, held.ID, "degraded"); err != nil {
		t.Fatal(err)
	}
	if err := repo.UnstockAccounts(ctx, []uint64{held.ID}, now); err != nil {
		t.Fatal(err)
	}
	restored, err := repo.Get(ctx, held.ID)
	if err != nil || restored.InventoryGroupID != 0 || restored.QualityVerdict != "degraded" {
		t.Fatalf("unstock = group %d verdict %q err %v", restored.InventoryGroupID, restored.QualityVerdict, err)
	}
}

func TestDeleteInventoryGroupKeepsDefaultAndOccupiedGroups(t *testing.T) {
	ctx := context.Background()
	database, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "inventory-delete.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewAccountRepository(database)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	groups, err := repo.ListInventoryGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].Name != "Default" {
		t.Fatalf("groups = %#v, err = %v", groups, err)
	}
	if err := repo.DeleteInventoryGroup(ctx, groups[0].ID); !errors.Is(err, repository.ErrInvalidRecord) {
		t.Fatalf("delete default = %v", err)
	}
	empty, err := repo.CreateInventoryGroup(ctx, "空组")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteInventoryGroup(ctx, empty.ID); err != nil {
		t.Fatal(err)
	}
	occupied, err := repo.CreateInventoryGroup(ctx, "有账号")
	if err != nil {
		t.Fatal(err)
	}
	held := accountModel{IdentityKey: testIdentityKey("inventory-delete"), Provider: "grok_build", Name: "held", SourceKey: "inventory-delete", Enabled: true, AuthStatus: "active", Priority: 1, CreatedAt: now, UpdatedAt: now}
	if err := database.db.WithContext(ctx).Create(&held).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.StockAccounts(ctx, []uint64{held.ID}, occupied.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteInventoryGroup(ctx, occupied.ID); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("delete occupied = %v", err)
	}
	left, err := repo.ListInventoryGroups(ctx)
	if err != nil || len(left) != 2 || left[0].Name != "Default" || left[1].Name != "有账号" {
		t.Fatalf("groups after delete = %#v, err = %v", left, err)
	}
}

func idsOf(values []account.Credential) []uint64 {
	out := make([]uint64, len(values))
	for i, value := range values {
		out[i] = value.ID
	}
	return out
}
