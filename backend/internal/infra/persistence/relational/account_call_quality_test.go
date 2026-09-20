package relational

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/audit"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

func TestListCallQualityFilters(t *testing.T) {
	ctx := context.Background()
	database, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "call-quality-filter.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	accounts := NewAccountRepository(database)
	audits := NewAuditRepository(database)

	neverCalled := createCallQualityAccount(t, ctx, database, "never-called", now)
	alwaysFail := createCallQualityAccount(t, ctx, database, "always-fail", now)
	lastFail := createCallQualityAccount(t, ctx, database, "last-fail", now)
	healthy := createCallQualityAccount(t, ctx, database, "healthy", now)
	mixedFail := createCallQualityAccount(t, ctx, database, "mixed-fail", now)
	degraded := createCallQualityAccount(t, ctx, database, "degraded", now)

	first := int64(200)
	if err := audits.CreateBatch(ctx, []audit.Record{
		failedAudit(alwaysFail.ID, "fail-1", now.Add(-3*time.Minute)),
		failedAudit(alwaysFail.ID, "fail-2", now.Add(-2*time.Minute)),
		failedAudit(alwaysFail.ID, "fail-3", now.Add(-time.Minute)),
		healthyAudit(lastFail.ID, "ok-old", now.Add(-2*time.Minute), first),
		failedAudit(lastFail.ID, "fail-now", now.Add(-time.Minute)),
		healthyAudit(healthy.ID, "ok-1", now.Add(-2*time.Minute), first),
		healthyAudit(healthy.ID, "ok-2", now.Add(-time.Minute), first),
		failedAudit(mixedFail.ID, "old-fail", now.Add(-2*time.Minute)),
		healthyAudit(mixedFail.ID, "recovered", now.Add(-time.Minute), first),
		{RequestID: "hard", ClientKeyID: 1, ModelRouteID: 1, AccountID: &degraded.ID, StatusCode: 200, Streaming: true, OutputTokens: 2000, FirstTokenMS: &first, DurationMS: 1200, CreatedAt: now.Add(-time.Minute)},
	}); err != nil {
		t.Fatal(err)
	}

	zero := 0
	one := 1
	two := 2
	three := 3
	fifty := 50.0
	zeroRate := 0.0
	hundred := 100.0
	assertAccountFilterCount(t, ctx, accounts, repository.AccountListFilter{CallCountMax: &zero, Now: now}, 1)
	assertAccountFilterIDs(t, ctx, accounts, repository.AccountListFilter{CallCountMax: &zero, Now: now}, neverCalled.ID)
	assertAccountFilterCount(t, ctx, accounts, repository.AccountListFilter{CallCountMin: &zero, CallCountMax: &zero, Now: now}, 1)
	assertAccountFilterIDs(t, ctx, accounts, repository.AccountListFilter{CallCountMin: &zero, CallCountMax: &zero, Now: now}, neverCalled.ID)
	assertAccountFilterCount(t, ctx, accounts, repository.AccountListFilter{CallCountMax: &one, Now: now}, 2)
	assertAccountFilterIDs(t, ctx, accounts, repository.AccountListFilter{CallCountMax: &one, Now: now}, neverCalled.ID, degraded.ID)
	assertAccountFilterCount(t, ctx, accounts, repository.AccountListFilter{CallCountMin: &one, CallCountMax: &one, Now: now}, 1)
	assertAccountFilterIDs(t, ctx, accounts, repository.AccountListFilter{CallCountMin: &one, CallCountMax: &one, Now: now}, degraded.ID)
	assertAccountFilterCount(t, ctx, accounts, repository.AccountListFilter{FailStreakMin: &three, Now: now}, 1)
	assertAccountFilterIDs(t, ctx, accounts, repository.AccountListFilter{FailStreakMin: &three, Now: now}, alwaysFail.ID)
	assertAccountFilterCount(t, ctx, accounts, repository.AccountListFilter{FailStreakMin: &one, Now: now}, 3)
	assertAccountFilterIDs(t, ctx, accounts, repository.AccountListFilter{FailStreakMin: &one, Now: now}, alwaysFail.ID, lastFail.ID, degraded.ID)
	assertAccountFilterCount(t, ctx, accounts, repository.AccountListFilter{FailStreakMax: &zero, Now: now}, 3)
	assertAccountFilterIDs(t, ctx, accounts, repository.AccountListFilter{FailStreakMax: &zero, Now: now}, neverCalled.ID, healthy.ID, mixedFail.ID)
	assertAccountFilterCount(t, ctx, accounts, repository.AccountListFilter{FailRateMin: &hundred, Now: now}, 2)
	assertAccountFilterIDs(t, ctx, accounts, repository.AccountListFilter{FailRateMin: &hundred, Now: now}, alwaysFail.ID, degraded.ID)
	assertAccountFilterCount(t, ctx, accounts, repository.AccountListFilter{FailRateMin: &fifty, Now: now}, 4)
	assertAccountFilterIDs(t, ctx, accounts, repository.AccountListFilter{FailRateMin: &fifty, Now: now}, alwaysFail.ID, lastFail.ID, mixedFail.ID, degraded.ID)
	assertAccountFilterCount(t, ctx, accounts, repository.AccountListFilter{FailRateMax: &zeroRate, Now: now}, 2)
	assertAccountFilterIDs(t, ctx, accounts, repository.AccountListFilter{FailRateMax: &zeroRate, Now: now}, neverCalled.ID, healthy.ID)
	assertAccountFilterCount(t, ctx, accounts, repository.AccountListFilter{FailStreakMin: &one, FailRateMin: &hundred, Now: now}, 2)
	assertAccountFilterIDs(t, ctx, accounts, repository.AccountListFilter{FailStreakMin: &one, FailRateMin: &hundred, Now: now}, alwaysFail.ID, degraded.ID)
	assertAccountFilterCount(t, ctx, accounts, repository.AccountListFilter{CallCountMax: &two, FailStreakMin: &one, Now: now}, 2)
	assertAccountFilterIDs(t, ctx, accounts, repository.AccountListFilter{CallCountMax: &two, FailStreakMin: &one, Now: now}, lastFail.ID, degraded.ID)
	assertAccountFilterCount(t, ctx, accounts, repository.AccountListFilter{CallCountMax: &zero, FailRateMin: &fifty, Now: now}, 0)
}

func createCallQualityAccount(t *testing.T, ctx context.Context, database *Database, source string, now time.Time) accountModel {
	t.Helper()
	value := accountModel{IdentityKey: testIdentityKey(source), Provider: "grok_build", Name: source, SourceKey: source, Enabled: true, AuthStatus: "active", Priority: 1}
	if err := database.db.WithContext(ctx).Create(&value).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.db.WithContext(ctx).Create(&accountCredentialModel{AccountID: value.ID, AuthType: "oauth", EncryptedPrimary: testEncryptedToken, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	return value
}

func failedAudit(accountID uint64, requestID string, createdAt time.Time) audit.Record {
	return audit.Record{RequestID: requestID, ClientKeyID: 1, ModelRouteID: 1, AccountID: &accountID, StatusCode: 503, ErrorCode: "upstream_unavailable", CreatedAt: createdAt}
}

func healthyAudit(accountID uint64, requestID string, createdAt time.Time, firstTokenMS int64) audit.Record {
	return audit.Record{RequestID: requestID, ClientKeyID: 1, ModelRouteID: 1, AccountID: &accountID, StatusCode: 200, Streaming: true, OutputTokens: 100, FirstTokenMS: &firstTokenMS, DurationMS: 2200, CreatedAt: createdAt}
}

func assertAccountFilterIDs(t *testing.T, ctx context.Context, accounts *AccountRepository, filter repository.AccountListFilter, expected ...uint64) {
	t.Helper()
	values, _, err := accounts.List(ctx, repository.AccountListQuery{
		Page:   repository.PageQuery{Limit: 20, Sort: repository.SortQuery{Field: "name", Direction: repository.SortAscending}},
		Filter: filter,
	})
	if err != nil {
		t.Fatalf("account filter %#v err = %v", filter, err)
	}
	got := make([]uint64, 0, len(values))
	for _, value := range values {
		got = append(got, value.ID)
	}
	if len(got) != len(expected) {
		t.Fatalf("account filter %#v ids = %v, want %v", filter, got, expected)
	}
	want := make(map[uint64]struct{}, len(expected))
	for _, id := range expected {
		want[id] = struct{}{}
	}
	for _, id := range got {
		if _, ok := want[id]; !ok {
			t.Fatalf("account filter %#v unexpected id %d in %v, want %v", filter, id, got, expected)
		}
	}
}
