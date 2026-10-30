package gateway

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	accountapp "github.com/chenyme/grok2api/backend/internal/application/account"
	clientkeyapp "github.com/chenyme/grok2api/backend/internal/application/clientkey"
	"github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/domain/clientkey"
	egressdomain "github.com/chenyme/grok2api/backend/internal/domain/egress"
	"github.com/chenyme/grok2api/backend/internal/infra/persistence/relational"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
	"github.com/chenyme/grok2api/backend/internal/infra/runtime/memory"
)

type memNodeAvoidance struct {
	until map[uint64]time.Time
}

func (m *memNodeAvoidance) LoadNodeAvoidance(context.Context, time.Time) (map[uint64]time.Time, error) {
	return m.until, nil
}

func (m *memNodeAvoidance) SaveNodeAvoidance(_ context.Context, nodeID uint64, until time.Time) error {
	if m.until == nil {
		m.until = map[uint64]time.Time{}
	}
	m.until[nodeID] = until
	return nil
}

func TestNodeAvoidanceSurvivesReload(t *testing.T) {
	now := time.Now().UTC()
	store := &memNodeAvoidance{until: map[uint64]time.Time{7: now.Add(time.Minute)}}
	selector := NewSelector(nil, nil, nil, nil, time.Hour, time.Second, time.Minute)
	selector.nodeAvoidance = store
	if err := selector.LoadNodeAvoidance(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !selector.nodeQuality.blocks(now, 7) {
		t.Fatal("loaded avoid-until did not block the node")
	}
	selector.SetNodeQualityLimits(1, 0, time.Minute, time.Minute)
	selector.NoteNodeQualityDegraded(9, now)
	if store.until[9].IsZero() {
		t.Fatal("arming avoidance did not save avoid-until")
	}
}

func TestNodeQualityWindowAvoidsForFiveMinutes(t *testing.T) {
	avoidance := &nodeQualityAvoidance{nodes: map[uint64]*nodeQualityState{}}
	avoidance.setLimits(3, 0)
	base := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	avoidance.noteDegraded(7, base)
	avoidance.noteDegraded(7, base.Add(10*time.Second))
	if avoidance.snapshot(base.Add(10 * time.Second)).blocks(7) {
		t.Fatal("two failures inside the window must not avoid the node")
	}
	avoidance.noteDegraded(7, base.Add(20*time.Second))
	now := base.Add(20 * time.Second)
	if !avoidance.snapshot(now).blocks(7) {
		t.Fatal("third failure inside one minute must avoid the node")
	}
	state := avoidance.inspect(7)
	if !state.avoidUntil.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("avoidUntil = %s, want five minutes from the triggering failure", state.avoidUntil)
	}
	avoidance.noteDelivered(7)
	if !avoidance.snapshot(now.Add(time.Minute)).blocks(7) {
		t.Fatal("a delivered success must not cancel the five-minute avoidance")
	}
	if avoidance.snapshot(now.Add(5*time.Minute + time.Second)).blocks(7) {
		t.Fatal("avoidance must end five minutes after the triggering failure")
	}

	stale := &nodeQualityAvoidance{nodes: map[uint64]*nodeQualityState{}}
	stale.setLimits(3, 0)
	stale.noteDegraded(7, base)
	stale.noteDegraded(7, base.Add(90*time.Second))
	stale.noteDegraded(7, base.Add(100*time.Second))
	if stale.snapshot(base.Add(100 * time.Second)).blocks(7) {
		t.Fatal("a failure older than one minute must leave the window")
	}
}

func TestNodeAvoidanceUntilKeepsOnlyTimedWindows(t *testing.T) {
	now := time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC)
	timed := &nodeQualityAvoidance{nodes: map[uint64]*nodeQualityState{}}
	timed.setPolicy(1, 0, time.Minute, 30*time.Minute)
	timed.noteDegraded(7, now)
	got := timed.avoidUntil(now)
	if !got[7].Equal(now.Add(30 * time.Minute)) {
		t.Fatalf("avoidUntil = %s", got[7])
	}
	if _, ok := timed.avoidUntil(now.Add(30 * time.Minute))[7]; ok {
		t.Fatal("expired avoidance must drop out of the listing")
	}

	skip := &nodeQualityAvoidance{nodes: map[uint64]*nodeQualityState{}}
	skip.setPolicy(0, 1, time.Minute, 30*time.Minute)
	skip.noteDegraded(9, now)
	if _, ok := skip.avoidUntil(now)[9]; ok {
		t.Fatal("a one-shot skip has no avoid time")
	}
}

func TestNodeQualityDurationsFollowPolicy(t *testing.T) {
	avoidance := &nodeQualityAvoidance{nodes: map[uint64]*nodeQualityState{}}
	avoidance.setPolicy(2, 0, 30*time.Second, 2*time.Minute)
	base := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	avoidance.noteDegraded(7, base)
	if avoidance.snapshot(base.Add(20 * time.Second)).blocks(7) {
		t.Fatal("one failure inside the configured window must not avoid the node")
	}
	now := base.Add(20 * time.Second)
	avoidance.noteDegraded(7, now)
	if !avoidance.snapshot(now).blocks(7) {
		t.Fatal("two failures inside the configured window must avoid the node")
	}
	state := avoidance.inspect(7)
	if !state.avoidUntil.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("avoidUntil = %s, want the configured duration", state.avoidUntil)
	}
	if avoidance.snapshot(now.Add(2*time.Minute + time.Second)).blocks(7) {
		t.Fatal("avoidance must end when the configured duration elapses")
	}
}

func TestNodeQualityStreakSkipsOnlyTheNextSelection(t *testing.T) {
	avoidance := &nodeQualityAvoidance{nodes: map[uint64]*nodeQualityState{}}
	avoidance.setLimits(0, 2)
	base := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	avoidance.noteDegraded(7, base)
	if avoidance.snapshot(base).blocks(7) {
		t.Fatal("one failure must not skip the node")
	}
	avoidance.noteDegraded(7, base.Add(time.Minute))
	snap := avoidance.snapshot(base.Add(time.Minute))
	if !snap.blocks(7) || len(snap.skipStreak) != 1 {
		t.Fatal("two consecutive failures must skip the next selection")
	}
	if !avoidance.inspect(7).avoidUntil.IsZero() {
		t.Fatal("the streak rule must not start the two-minute avoidance")
	}
	avoidance.consumeSkip(snap)
	if avoidance.snapshot(base.Add(time.Minute)).blocks(7) {
		t.Fatal("using the skip must clear it")
	}
	if avoidance.inspect(7).consecutive != 0 {
		t.Fatal("using the skip must reset the streak")
	}
	avoidance.noteDegraded(7, base.Add(2*time.Minute))
	if avoidance.snapshot(base.Add(2 * time.Minute)).blocks(7) {
		t.Fatal("the streak must start again after it was consumed")
	}
}

func TestNodeQualityConsumeKeepsNewerStreak(t *testing.T) {
	avoidance := &nodeQualityAvoidance{nodes: map[uint64]*nodeQualityState{}}
	avoidance.setLimits(0, 2)
	base := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	avoidance.noteDegraded(7, base)
	avoidance.noteDegraded(7, base.Add(time.Second))
	stale := avoidance.snapshot(base.Add(time.Second))
	avoidance.noteDegraded(7, base.Add(2*time.Second))
	avoidance.consumeSkip(stale)
	state := avoidance.inspect(7)
	if !state.skipOnce || state.consecutive != 3 {
		t.Fatalf("stale selection cleared a newer streak: %+v", state)
	}
	current := avoidance.snapshot(base.Add(2 * time.Second))
	avoidance.consumeSkip(current)
	state = avoidance.inspect(7)
	if state.skipOnce || state.consecutive != 0 {
		t.Fatalf("current selection did not consume the streak it observed: %+v", state)
	}
}

func TestNodeQualitySelectionSkipsAndFallsOpen(t *testing.T) {
	ctx := context.Background()
	database, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "node-quality.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	accounts := relational.NewAccountRepository(database)
	blockedNode, openNode := createBuildNodes(t, ctx, database)
	high, err := insertNodeAccount(ctx, accounts, "high", 100, blockedNode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := insertNodeAccount(ctx, accounts, "low", 10, openNode); err != nil {
		t.Fatal(err)
	}
	selector := NewSelector(accounts, memory.NewConcurrencyLimiter(), memory.NewStickyStore(), nil, time.Hour, time.Second, time.Minute)
	selector.SetNodeQualityLimits(0, 2, 0, 0)
	now := time.Now().UTC()
	selector.NoteNodeQualityDegraded(blockedNode, now)
	selector.NoteNodeQualityDegraded(blockedNode, now)

	session, err := selector.beginSelectionSession(ctx, account.ProviderBuild, 0, "model", "", "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := session.Acquire(ctx, map[uint64]bool{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Credential.EgressNodeID != openNode {
		t.Fatalf("first switch picked node %d, want %d", lease.Credential.EgressNodeID, openNode)
	}
	lease.Release()
	if selector.nodeQuality.snapshot(time.Now().UTC()).blocks(blockedNode) {
		t.Fatal("the one-shot skip must be consumed after another node was selected")
	}

	next, err := session.Acquire(ctx, map[uint64]bool{lease.Credential.ID: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if next.Credential.ID != high.ID {
		t.Fatalf("account after the skip was consumed = %d, want %d", next.Credential.ID, high.ID)
	}
	next.Release()

	fallback := NewSelector(accounts, memory.NewConcurrencyLimiter(), memory.NewStickyStore(), nil, time.Hour, time.Second, time.Minute)
	fallback.SetNodeQualityLimits(3, 0, 0, 0)
	for range 3 {
		fallback.NoteNodeQualityDegraded(blockedNode, now)
		fallback.NoteNodeQualityDegraded(openNode, now)
	}
	fallbackSession, err := fallback.beginSelectionSession(ctx, account.ProviderBuild, 0, "model", "", "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	released, err := fallbackSession.Acquire(ctx, map[uint64]bool{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if released.Credential.ID != high.ID {
		t.Fatalf("fallback released account %d, want highest priority %d", released.Credential.ID, high.ID)
	}
	released.Release()
}

func TestNodeQualityStickyYieldsWhileNodeIsAvoided(t *testing.T) {
	ctx := context.Background()
	database, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "node-quality-sticky.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	accounts := relational.NewAccountRepository(database)
	blockedNode, openNode := createBuildNodes(t, ctx, database)
	if _, err := insertNodeAccount(ctx, accounts, "sticky", 100, blockedNode); err != nil {
		t.Fatal(err)
	}
	if _, err := insertNodeAccount(ctx, accounts, "other", 10, openNode); err != nil {
		t.Fatal(err)
	}
	selector := NewSelector(accounts, memory.NewConcurrencyLimiter(), memory.NewStickyStore(), nil, time.Hour, time.Second, time.Minute)
	session, err := selector.beginSelectionSession(ctx, account.ProviderBuild, 0, "model", "", "affinity", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	first, err := session.Acquire(ctx, map[uint64]bool{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Credential.EgressNodeID != blockedNode {
		t.Fatalf("initial sticky account is on node %d", first.Credential.EgressNodeID)
	}
	first.Release()
	selector.SetNodeQualityLimits(0, 1, 0, 0)
	selector.NoteNodeQualityDegraded(blockedNode, time.Now().UTC())

	nextSession, err := selector.beginSelectionSession(ctx, account.ProviderBuild, 0, "model", "", "affinity", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	next, err := nextSession.Acquire(ctx, map[uint64]bool{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if next.Credential.EgressNodeID != openNode {
		t.Fatalf("sticky account stayed on avoided node %d", next.Credential.EgressNodeID)
	}
	next.Release()
}

func TestNodeQualityWindowKeepsLaterSessionsOffTheNode(t *testing.T) {
	ctx := context.Background()
	database, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "node-quality-window.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	accounts := relational.NewAccountRepository(database)
	blockedNode, openNode := createBuildNodes(t, ctx, database)
	high, err := insertNodeAccount(ctx, accounts, "high", 100, blockedNode)
	if err != nil {
		t.Fatal(err)
	}
	mid, err := insertNodeAccount(ctx, accounts, "mid", 50, blockedNode)
	if err != nil {
		t.Fatal(err)
	}
	open, err := insertNodeAccount(ctx, accounts, "open", 10, openNode)
	if err != nil {
		t.Fatal(err)
	}
	selector := NewSelector(accounts, memory.NewConcurrencyLimiter(), memory.NewStickyStore(), nil, time.Hour, time.Second, time.Minute)
	selector.SetNodeQualityLimits(3, 2, time.Minute, 5*time.Minute)
	session, err := selector.beginSelectionSession(ctx, account.ProviderBuild, 0, "model", "", "affinity", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	first, err := session.Acquire(ctx, map[uint64]bool{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Credential.ID != high.ID {
		t.Fatalf("first account = %d, want %d", first.Credential.ID, high.ID)
	}
	first.Release()
	now := time.Now().UTC()
	for range 3 {
		selector.NoteNodeQualityDegraded(blockedNode, now)
	}
	if !selector.nodeQuality.snapshot(now.Add(time.Second)).blocks(blockedNode) {
		t.Fatal("three failures did not arm the avoid window")
	}
	again, err := session.Acquire(ctx, map[uint64]bool{high.ID: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Credential.EgressNodeID != openNode || again.Credential.ID != open.ID {
		t.Fatalf("same session picked account %d on node %d after the window armed", again.Credential.ID, again.Credential.EgressNodeID)
	}
	again.Release()

	nextSession, err := selector.beginSelectionSession(ctx, account.ProviderBuild, 0, "model", "", "affinity", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	next, err := nextSession.Acquire(ctx, map[uint64]bool{high.ID: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if next.Credential.ID != open.ID {
		t.Fatalf("later session picked account %d on node %d, want %d", next.Credential.ID, next.Credential.EgressNodeID, open.ID)
	}
	next.Release()
	if override, ok := selector.healthOverrides[mid.ID]; ok {
		t.Fatalf("node avoidance must not write a health override: %+v", override)
	}
}

func TestNodeQualitySkipSurvivesStickyBindFailure(t *testing.T) {
	ctx := context.Background()
	database, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "node-quality-bind.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	accounts := relational.NewAccountRepository(database)
	blockedNode, openNode := createBuildNodes(t, ctx, database)
	if _, err := insertNodeAccount(ctx, accounts, "blocked", 100, blockedNode); err != nil {
		t.Fatal(err)
	}
	if _, err := insertNodeAccount(ctx, accounts, "open", 10, openNode); err != nil {
		t.Fatal(err)
	}
	sticky := &bindFailSticky{StickyStore: memory.NewStickyStore(), fail: true}
	selector := NewSelector(accounts, memory.NewConcurrencyLimiter(), sticky, nil, time.Hour, time.Second, time.Minute)
	selector.SetNodeQualityLimits(0, 2, 0, 0)
	now := time.Now().UTC()
	selector.NoteNodeQualityDegraded(blockedNode, now)
	selector.NoteNodeQualityDegraded(blockedNode, now)

	session, err := selector.beginSelectionSession(ctx, account.ProviderBuild, 0, "model", "", "affinity", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Acquire(ctx, map[uint64]bool{}, false); err == nil {
		t.Fatal("sticky bind failure was ignored")
	}
	if !selector.nodeQuality.snapshot(time.Now().UTC()).blocks(blockedNode) {
		t.Fatal("failed sticky bind consumed the one-shot node skip")
	}
}

type bindFailSticky struct {
	*memory.StickyStore
	fail bool
}

func (s *bindFailSticky) Bind(ctx context.Context, key string, accountID uint64, now, expiresAt time.Time) (uint64, error) {
	if s.fail {
		return 0, errors.New("sticky bind failed")
	}
	return s.StickyStore.Bind(ctx, key, accountID, now, expiresAt)
}

func createBuildNodes(t *testing.T, ctx context.Context, database *relational.Database) (uint64, uint64) {
	t.Helper()
	nodes := relational.NewEgressRepository(database)
	blocked, err := nodes.CreateEgressNode(ctx, egressdomain.Node{Name: "blocked", Scope: egressdomain.ScopeBuild, Enabled: true, EncryptedProxyURL: "blocked"})
	if err != nil {
		t.Fatal(err)
	}
	open, err := nodes.CreateEgressNode(ctx, egressdomain.Node{Name: "open", Scope: egressdomain.ScopeBuild, Enabled: true, EncryptedProxyURL: "open"})
	if err != nil {
		t.Fatal(err)
	}
	return blocked.ID, open.ID
}

func insertNodeAccount(ctx context.Context, accounts *relational.AccountRepository, name string, priority int, nodeID uint64) (account.Credential, error) {
	_ = priority
	credential, _, err := accounts.UpsertByIdentity(ctx, account.Credential{
		Provider: account.ProviderBuild, Name: name, SourceKey: name, EncryptedAccessToken: name,
		EncryptedRefreshToken: "refresh-" + name, ExpiresAt: time.Now().Add(time.Hour),
		Enabled: true, AuthStatus: account.AuthStatusActive, MaxConcurrent: 1,
		EgressNodeID: nodeID,
	})
	return credential, err
}

func TestNodeQualityIgnoresUpstreamFailureAndCountsDegraded(t *testing.T) {
	noThink := sse(
		`data: {"choices":[{"delta":{"content":"`+repeatForQuality("abcd", 40)+`"}}]}`,
		`data: {"usage":{"completion_tokens":40,"completion_tokens_details":{"reasoning_tokens":0}}}`,
		"data: [DONE]",
	)
	thinking := sse(
		`data: {"choices":[{"delta":{"thinking_content":"plan the game"}}]}`,
		`data: {"choices":[{"delta":{"content":"good game after retry"}}]}`,
		`data: {"usage":{"completion_tokens":80,"completion_tokens_details":{"reasoning_tokens":40}}}`,
		"data: [DONE]",
	)
	t.Run("http 502", func(t *testing.T) {
		high, low, _, attempts := runNodeQualityChat(t, func(high, low, other account.Credential) provider.Adapter {
			return &scriptedBuildAdapter{responses: map[uint64][]scriptedBuildResponse{
				high.ID:  {{status: http.StatusBadGateway, body: "bad gateway"}},
				low.ID:   {{status: http.StatusOK, body: thinking}},
				other.ID: {{status: http.StatusOK, body: thinking}},
			}}
		})
		if len(attempts) < 2 || attempts[0] != high.ID || attempts[1] != low.ID {
			t.Fatalf("attempts = %v, want %d then same-node %d", attempts, high.ID, low.ID)
		}
	})
	t.Run("transport", func(t *testing.T) {
		high, low, _, attempts := runNodeQualityChat(t, func(high, low, other account.Credential) provider.Adapter {
			return &transportOnceAdapter{failID: high.ID, scriptedBuildAdapter: scriptedBuildAdapter{responses: map[uint64][]scriptedBuildResponse{
				low.ID:   {{status: http.StatusOK, body: thinking}},
				other.ID: {{status: http.StatusOK, body: thinking}},
			}}}
		})
		if len(attempts) < 2 || attempts[0] != high.ID || attempts[1] != low.ID {
			t.Fatalf("attempts = %v, want %d then same-node %d", attempts, high.ID, low.ID)
		}
	})
	t.Run("quality degraded", func(t *testing.T) {
		high, low, other, attempts := runNodeQualityChat(t, func(high, low, other account.Credential) provider.Adapter {
			return &scriptedBuildAdapter{responses: map[uint64][]scriptedBuildResponse{
				high.ID:  {{status: http.StatusOK, body: noThink}},
				low.ID:   {{status: http.StatusOK, body: thinking}},
				other.ID: {{status: http.StatusOK, body: thinking}},
			}}
		})
		if len(attempts) < 2 || attempts[0] != high.ID || attempts[1] != other.ID {
			t.Fatalf("attempts = %v, want %d then other-node %d, not same-node %d", attempts, high.ID, other.ID, low.ID)
		}
	})
}

func repeatForQuality(value string, count int) string {
	out := ""
	for range count {
		out += value
	}
	return out
}

type transportOnceAdapter struct {
	scriptedBuildAdapter
	failID uint64
	failed atomic.Bool
}

func (a *transportOnceAdapter) ForwardResponse(ctx context.Context, request provider.ResponseResourceRequest) (*provider.Response, error) {
	if request.Credential.ID == a.failID && a.failed.CompareAndSwap(false, true) {
		a.mu.Lock()
		a.attempts = append(a.attempts, request.Credential.ID)
		a.mu.Unlock()
		return nil, errors.New("dial tcp 10.0.0.1:443: connect: connection refused")
	}
	return a.scriptedBuildAdapter.ForwardResponse(ctx, request)
}

func runNodeQualityChat(t *testing.T, adapterFor func(high, low, other account.Credential) provider.Adapter) (account.Credential, account.Credential, account.Credential, []uint64) {
	t.Helper()
	ctx := context.Background()
	database, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "node-quality-chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	accountRepo := relational.NewAccountRepository(database)
	modelRepo := relational.NewModelRepository(database)
	blockedNode, openNode := createBuildNodes(t, ctx, database)
	high, err := insertNodeAccount(ctx, accountRepo, "high", 300, blockedNode)
	if err != nil {
		t.Fatal(err)
	}
	low, err := insertNodeAccount(ctx, accountRepo, "low", 200, blockedNode)
	if err != nil {
		t.Fatal(err)
	}
	other, err := insertNodeAccount(ctx, accountRepo, "other", 100, openNode)
	if err != nil {
		t.Fatal(err)
	}
	if err := modelRepo.UpsertDiscovered(ctx, account.ProviderBuild, []string{"grok-4.6"}); err != nil {
		t.Fatal(err)
	}
	for _, credential := range []account.Credential{high, low, other} {
		if err := modelRepo.ReplaceAccountCapabilities(ctx, credential.ID, []string{"grok-4.6"}, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	keyRepo := relational.NewClientKeyRepository(database)
	clientKey, err := keyRepo.Create(ctx, clientkey.Key{
		Name: "node-quality-key", Prefix: "nq", SecretHash: repeatForQuality("f", 64), EncryptedSecret: "encrypted",
		Enabled: true, RPMLimit: 120, MaxConcurrent: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := adapterFor(high, low, other)
	registry := provider.NewRegistry(adapter)
	sticky := memory.NewStickyStore()
	accountService := accountapp.NewService(accountRepo, relational.NewAuditRepository(database), memory.NewDeviceSessionStore(), sticky, registry, testCipher(t), nil)
	selector := NewSelector(accountRepo, memory.NewConcurrencyLimiter(), sticky, registry, time.Hour, time.Second, time.Minute)
	service := NewService(modelRepo, relational.NewAuditRepository(database), accountService, clientkeyapp.NewService(nil, nil, nil, 60, 4, nil), registry, selector, relational.NewResponseRepository(database), 3)
	service.UpdateQualityRetry(QualityRetryRuntime{
		Enabled: true, MaxAttempts: 3, MinOutputTokens: 8, OnExhausted: qualityRetryFailOpen, HoldTimeout: time.Second,
		WindowFailures: 1, ConsecutiveFailures: 1,
	})
	result, err := service.CreateChatCompletion(ctx, Input{
		RequestID: "req-node-quality", ClientKey: clientKey, PublicModel: "grok-4.6", Streaming: true,
		Body: []byte(`{"model":"grok-4.6","messages":[{"role":"user","content":"write a game"}],"stream":true}`),
	})
	if err != nil {
		t.Fatalf("chat completion: %v", err)
	}
	if result.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", result.StatusCode)
	}
	scripted, ok := adapter.(*scriptedBuildAdapter)
	if !ok {
		if wrapped, wrappedOK := adapter.(*transportOnceAdapter); wrappedOK {
			return high, low, other, wrapped.Attempts()
		}
		t.Fatalf("adapter type %T has no attempts", adapter)
	}
	return high, low, other, scripted.Attempts()
}

func TestNodeQualityZeroLimitsClearExistingAvoidance(t *testing.T) {
	avoidance := &nodeQualityAvoidance{nodes: map[uint64]*nodeQualityState{}}
	avoidance.setLimits(1, 0)
	now := time.Date(2026, 10, 23, 3, 0, 0, 0, time.UTC)
	avoidance.noteDegraded(7, now)
	if !avoidance.blocks(now, 7) {
		t.Fatal("one failure at the limit must avoid the node")
	}
	avoidance.setLimits(0, 0)
	if avoidance.blocks(now.Add(time.Second), 7) {
		t.Fatal("setting both limits to zero must clear an avoidance that is already armed")
	}
}

func TestNodeAvoidanceDoesNotReplaceHealthOverride(t *testing.T) {
	now := time.Now().UTC()
	selector := &Selector{healthOverrides: map[uint64]routingHealthOverride{
		9: {
			provider: account.ProviderBuild, lastError: account.LastErrorMissingThinking,
			revision: 4, updatedAt: now, expiresAt: now.Add(time.Hour),
		},
	}}
	selector.SetNodeQualityLimits(1, 0, time.Minute, 5*time.Minute)
	selector.NoteNodeQualityDegraded(7, now)
	override := selector.healthOverrides[9]
	if override.lastError != account.LastErrorMissingThinking || override.revision != 4 {
		t.Fatalf("health override changed: %+v", override)
	}
}
