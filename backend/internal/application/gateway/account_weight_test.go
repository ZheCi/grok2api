package gateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/infra/runtime/memory"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

func TestAccountWeightPrefersHigherSuccessRate(t *testing.T) {
	now := time.Now().UTC()
	selector := NewSelector(nil, memory.NewConcurrencyLimiter(), memory.NewStickyStore(), nil, time.Hour, time.Second, time.Minute)
	selector.SetAccountWeight(AccountWeightSettings{Window: "24h", SuccessRate: 1})
	selector.weights[1] = successWeightState(now, 1, 9)
	selector.weights[2] = successWeightState(now, 9, 1)
	values := []account.RoutingCandidate{
		{Credential: account.Credential{ID: 1, Provider: account.ProviderBuild, Enabled: true, AuthStatus: account.AuthStatusActive, MaxConcurrent: 1}},
		{Credential: account.Credential{ID: 2, Provider: account.ProviderBuild, Enabled: true, AuthStatus: account.AuthStatusActive, MaxConcurrent: 1}},
	}
	plan, err := selector.planCandidates(context.Background(), values, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, ok := plan.Next()
	if !ok || first.Credential.ID != 2 {
		t.Fatalf("first account = %d, want higher success rate 2", first.Credential.ID)
	}
}

func TestAccountWeightRanksNewbieBehindGraduated(t *testing.T) {
	now := time.Now().UTC()
	selector := NewSelector(nil, memory.NewConcurrencyLimiter(), memory.NewStickyStore(), nil, time.Hour, time.Second, time.Minute)
	selector.SetAccountWeight(AccountWeightSettings{Window: "24h", NewbieProtection: 1, NewbieDays: 7, NewbieDailyBase: 20})
	values := []account.RoutingCandidate{
		{Credential: account.Credential{ID: 1, Provider: account.ProviderBuild, Enabled: true, AuthStatus: account.AuthStatusActive, MaxConcurrent: 1, CreatedAt: now.Add(-24 * time.Hour)}},
		{Credential: account.Credential{ID: 2, Provider: account.ProviderBuild, Enabled: true, AuthStatus: account.AuthStatusActive, MaxConcurrent: 1, CreatedAt: now.Add(-30 * 24 * time.Hour)}},
	}
	plan, err := selector.planCandidates(context.Background(), values, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, ok := plan.Next()
	if !ok || first.Credential.ID != 2 {
		t.Fatalf("first account = %d, want graduated account 2", first.Credential.ID)
	}
}

func TestAccountWeightDailyCapExcludesNewbie(t *testing.T) {
	now := time.Now().UTC()
	created := now.Add(-time.Hour)
	repo := &layeredAccountRepository{bases: []account.RoutingAccountBase{{
		Credential: account.Credential{ID: 7, Provider: account.ProviderBuild, Enabled: true, AuthStatus: account.AuthStatusActive, MaxConcurrent: 1, CreatedAt: created},
	}}, overlays: map[string]account.RoutingOverlaySnapshot{"model": {}}}
	selector := NewSelector(repo, memory.NewConcurrencyLimiter(), memory.NewStickyStore(), nil, time.Hour, time.Second, time.Minute)
	selector.SetAccountWeight(AccountWeightSettings{Window: "24h", NewbieDays: 7, NewbieDailyBase: 20, SuccessRate: 1})
	selector.weights[7] = &weightAccount{newbieDay: 0, newbieAttempts: 20}
	_, err := selector.Acquire(context.Background(), account.ProviderBuild, 0, "model", "", "", nil, false)
	var unavailable *SelectionUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("capped newbie err = %v", err)
	}
}

func TestCanceledWeightAttemptDoesNotChangeRateOrDailyCount(t *testing.T) {
	now := time.Now().UTC()
	selector := NewSelector(nil, nil, nil, nil, time.Hour, time.Second, time.Minute)
	selector.SetAccountWeight(AccountWeightSettings{Window: "24h", NewbieDays: 7, NewbieDailyBase: 20})
	credential := account.Credential{ID: 4, CreatedAt: now.Add(-time.Hour)}
	selector.recordWeightAttempt(context.Background(), weightAttempt{account: credential, model: "grok-4"}, "cancel", 100)
	if len(selector.weights) != 0 {
		t.Fatalf("cancel stored weight state %#v", selector.weights[4])
	}
	selector.recordWeightAttempt(context.Background(), weightAttempt{account: credential, model: "grok-4"}, "success", 100)
	state := selector.weights[4]
	if state == nil || state.newbieAttempts != 1 {
		t.Fatalf("daily count = %#v", state)
	}
	ok, fail, _, _ := state.totals(now, 24*time.Hour, true, "grok-4", 100)
	if ok != 1 || fail != 0 {
		t.Fatalf("totals ok=%d fail=%d", ok, fail)
	}
}

func TestSetManualWeightReplacesCalculatedScore(t *testing.T) {
	selector := NewSelector(nil, nil, nil, nil, time.Hour, time.Second, time.Minute)
	selector.SetAccountWeight(AccountWeightSettings{Window: "24h", SuccessRate: 1})
	credential := account.Credential{ID: 9, Provider: account.ProviderBuild}
	view, err := selector.SetManualWeight(context.Background(), credential, intPtr(17))
	if err != nil || !view.Manual || view.Weight != 17 {
		t.Fatalf("set = %+v, err = %v", view, err)
	}
	if got := selector.accountWeight(credential, time.Now().UTC(), "grok-4", 100, time.Time{}); got != 17 {
		t.Fatalf("routing weight = %d", got)
	}
	shown := selector.AccountWeights([]account.Credential{credential})[credential.ID]
	if !shown.Manual || shown.Weight != 17 {
		t.Fatalf("shown = %+v", shown)
	}
	cleared, err := selector.SetManualWeight(context.Background(), credential, nil)
	if err != nil || cleared.Manual {
		t.Fatalf("clear = %+v, err = %v", cleared, err)
	}
}

func TestConcurrentWeightSavesKeepBothAttempts(t *testing.T) {
	selector := NewSelector(nil, nil, nil, nil, time.Hour, time.Second, time.Minute)
	selector.SetAccountWeight(AccountWeightSettings{Window: "24h", SuccessRate: 1})
	store := &memoryWeightStore{rows: map[uint64]string{}, delay: 20 * time.Millisecond}
	selector.weightStore = store
	credential := account.Credential{ID: 11, CreatedAt: time.Now().UTC().Add(-48 * time.Hour)}
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		selector.recordWeightAttempt(context.Background(), weightAttempt{account: credential, model: "grok-4"}, "success", 100)
	}()
	go func() {
		defer group.Done()
		selector.recordWeightAttempt(context.Background(), weightAttempt{account: credential, model: "grok-4"}, "success", 100)
	}()
	group.Wait()
	state := decodeWeightAccount(store.payload(11))
	ok, fail, _, _ := state.totals(time.Now().UTC(), 24*time.Hour, true, "grok-4", 100)
	if ok != 2 || fail != 0 {
		t.Fatalf("saved attempts ok=%d fail=%d", ok, fail)
	}
}

func TestManualWeightSaveFailureRestoresThePreviousValue(t *testing.T) {
	selector := NewSelector(nil, nil, nil, nil, time.Hour, time.Second, time.Minute)
	selector.weightStore = failWeightStore{}
	credential := account.Credential{ID: 12}
	if _, err := selector.SetManualWeight(context.Background(), credential, intPtr(17)); err == nil {
		t.Fatal("expected save error")
	}
	if got := selector.AccountWeights([]account.Credential{credential})[credential.ID]; got.Manual || got.Weight == 17 {
		t.Fatalf("weight stayed manual after a failed save: %+v", got)
	}
}

func TestNewbieReservationHoldsAndReleasesTheDailyCap(t *testing.T) {
	now := time.Now().UTC()
	selector := NewSelector(nil, nil, nil, nil, time.Hour, time.Second, time.Minute)
	selector.SetAccountWeight(AccountWeightSettings{Window: "24h", NewbieDays: 7, NewbieDailyBase: 1})
	credential := account.Credential{ID: 13, CreatedAt: now.Add(-time.Hour)}
	if !selector.reserveNewbieAttempt(credential) {
		t.Fatal("expected a reservation")
	}
	if !selector.weightCapped(credential, now) {
		t.Fatal("in-flight reservation did not fill the daily cap")
	}
	lease := &accountLease{Credential: credential, selector: selector, newbieReserved: true}
	lease.releaseNewbieReservation()
	if selector.weightCapped(credential, now) {
		t.Fatal("releasing the lease kept the daily cap")
	}
}

func TestDisplayedWeightIgnoresFirstTokenSpeed(t *testing.T) {
	now := time.Now().UTC()
	selector := NewSelector(nil, nil, nil, nil, time.Hour, time.Second, time.Minute)
	selector.SetAccountWeight(AccountWeightSettings{Window: "24h", FirstTokenSpeed: 1})
	selector.weights[14] = &weightAccount{hours: map[int64]*weightBucket{now.UTC().Truncate(time.Hour).Unix(): {
		speed: map[string]*weightSpeed{"grok-4|2": {ms: 20000, n: 1}},
	}}}
	credential := account.Credential{ID: 14, CreatedAt: now.Add(-48 * time.Hour)}
	routed := selector.accountWeight(credential, now, "grok-4", 80000, time.Time{})
	shown := selector.AccountWeights([]account.Credential{credential})[credential.ID].Weight
	if routed >= shown {
		t.Fatalf("slow long-request weight %d should be below the displayed weight %d", routed, shown)
	}
}

type memoryWeightStore struct {
	mu    sync.Mutex
	rows  map[uint64]string
	delay time.Duration
}

func (s *memoryWeightStore) ListAccountWeights(context.Context) ([]repository.AccountWeightSnapshot, error) {
	return nil, nil
}

func (s *memoryWeightStore) SaveAccountWeight(_ context.Context, snapshot repository.AccountWeightSnapshot) error {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.mu.Lock()
	s.rows[snapshot.AccountID] = snapshot.Payload
	s.mu.Unlock()
	return nil
}

func (s *memoryWeightStore) payload(id uint64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[id]
}

type failWeightStore struct{}

func (failWeightStore) ListAccountWeights(context.Context) ([]repository.AccountWeightSnapshot, error) {
	return nil, nil
}

func (failWeightStore) SaveAccountWeight(context.Context, repository.AccountWeightSnapshot) error {
	return errors.New("disk")
}

func intPtr(value int) *int { return &value }

func successWeightState(now time.Time, okCount, failCount int) *weightAccount {
	bucket := &weightBucket{ok: okCount, fail: failCount}
	return &weightAccount{
		hours:       map[int64]*weightBucket{now.UTC().Truncate(time.Hour).Unix(): bucket},
		lastAttempt: now,
		lastOK:      okCount > 0,
	}
}
