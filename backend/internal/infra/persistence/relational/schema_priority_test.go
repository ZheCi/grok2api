package relational

import (
	"context"
	"testing"
)

func TestDropAccountPriorityRemovesDependentIndexes(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t)
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.db.Exec("ALTER TABLE provider_accounts ADD COLUMN priority integer NOT NULL DEFAULT 1").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.db.Exec("CREATE INDEX idx_accounts_pool_routing ON provider_accounts(priority DESC, id ASC)").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.dropAccountPriority(ctx); err != nil {
		t.Fatal(err)
	}
	if database.db.Migrator().HasColumn("provider_accounts", "priority") {
		t.Fatal("priority column still exists")
	}
	var names []string
	if err := database.db.Raw(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_accounts_pool_routing'`).Scan(&names).Error; err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Fatalf("dependent index remains: %#v", names)
	}
}
