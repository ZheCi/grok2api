package relational

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestNodeAvoidanceRoundTrip(t *testing.T) {
	ctx := context.Background()
	database, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "avoid.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewAccountRepository(database)
	now := time.Now().UTC().Truncate(time.Second)
	if err := repo.SaveNodeAvoidance(ctx, 7, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveNodeAvoidance(ctx, 8, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.LoadNodeAvoidance(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[7].Unix() != now.Add(time.Minute).Unix() {
		t.Fatalf("loaded = %#v", loaded)
	}
}
