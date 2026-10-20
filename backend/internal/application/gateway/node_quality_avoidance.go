package gateway

import (
	"log/slog"
	"sync"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
)

const (
	defaultNodeQualityFailureWindow = time.Minute
	defaultNodeQualityAvoidDuration = 5 * time.Minute
)

// nodeQualityState is the in-memory avoidance record for one egress node.
// A delivered success clears the streak and the one-shot skip. It does not
// cancel avoidUntil or the timestamps already inside the one-minute window.
type nodeQualityState struct {
	failures       []time.Time
	consecutive    int
	avoidUntil     time.Time
	avoidUntilUnix int64
	skipOnce       bool
}

type nodeQualitySnapshot struct {
	blocked map[uint64]struct{}
	// skipStreak is the consecutive count observed when the one-shot skip was
	// snapshotted. A later degradation must not be erased by this selection.
	skipStreak map[uint64]int
}

func (s nodeQualitySnapshot) active() bool {
	return len(s.blocked) > 0
}

func (s nodeQualitySnapshot) blocks(nodeID uint64) bool {
	if nodeID == 0 {
		return false
	}
	_, ok := s.blocked[nodeID]
	return ok
}

// nodeQualityAvoidance counts quality-degraded attempts per egress node.
// Transport errors and upstream HTTP failures are not recorded here.
type nodeQualityAvoidance struct {
	mu            sync.Mutex
	windowLimit   int
	streakLimit   int
	failureWindow time.Duration
	avoidDuration time.Duration
	nodes         map[uint64]*nodeQualityState
}

func (a *nodeQualityAvoidance) setLimits(window, streak int) {
	a.setPolicy(window, streak, 0, 0)
}

func (a *nodeQualityAvoidance) setPolicy(window, streak int, failureWindow, avoidDuration time.Duration) {
	if window < 0 {
		window = 0
	}
	if streak < 0 {
		streak = 0
	}
	if failureWindow < 0 {
		failureWindow = 0
	}
	if avoidDuration < 0 {
		avoidDuration = 0
	}
	a.mu.Lock()
	a.windowLimit = window
	a.streakLimit = streak
	a.failureWindow = failureWindow
	a.avoidDuration = avoidDuration
	a.mu.Unlock()
}

func (a *nodeQualityAvoidance) durationsLocked() (time.Duration, time.Duration) {
	failureWindow := a.failureWindow
	if failureWindow <= 0 {
		failureWindow = defaultNodeQualityFailureWindow
	}
	avoidDuration := a.avoidDuration
	if avoidDuration <= 0 {
		avoidDuration = defaultNodeQualityAvoidDuration
	}
	return failureWindow, avoidDuration
}

func (a *nodeQualityAvoidance) noteDegraded(nodeID uint64, now time.Time) (armedAvoid bool, armedSkip bool) {
	if a == nil || nodeID == 0 {
		return false, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.windowLimit <= 0 && a.streakLimit <= 0 {
		return false, false
	}
	state := a.stateLocked(nodeID)
	failureWindow, avoidDuration := a.durationsLocked()
	if a.windowLimit > 0 {
		cutoff := now.Add(-failureWindow)
		kept := state.failures[:0]
		for _, ts := range state.failures {
			if ts.After(cutoff) {
				kept = append(kept, ts)
			}
		}
		state.failures = append(kept, now)
		if len(state.failures) >= a.windowLimit {
			until := now.Add(avoidDuration)
			if until.UnixNano() > state.avoidUntilUnix {
				state.avoidUntil = until
				state.avoidUntilUnix = until.UnixNano()
				armedAvoid = true
			}
		}
	}
	if a.streakLimit > 0 {
		state.consecutive++
		if state.consecutive >= a.streakLimit && !state.skipOnce {
			state.skipOnce = true
			armedSkip = true
		}
	}
	return armedAvoid, armedSkip
}

func (a *nodeQualityAvoidance) noteDelivered(nodeID uint64) {
	if a == nil || nodeID == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.nodes[nodeID]
	if state == nil {
		return
	}
	state.consecutive = 0
	state.skipOnce = false
}

// consumeSkip clears a one-shot skip after a selection actually used another
// node. The streak is reset only when it still matches the snapshot, so a
// degradation that landed after the snapshot keeps its count and skip.
func (a *nodeQualityAvoidance) consumeSkip(snap nodeQualitySnapshot) {
	if a == nil || len(snap.skipStreak) == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, seen := range snap.skipStreak {
		state := a.nodes[id]
		if state == nil || !state.skipOnce || state.consecutive != seen {
			continue
		}
		state.skipOnce = false
		state.consecutive = 0
	}
}

func (a *nodeQualityAvoidance) blocks(now time.Time, nodeID uint64) bool {
	if a == nil || nodeID == 0 {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.blocksLocked(now, nodeID)
}

func nodeAvoiding(state *nodeQualityState, now time.Time) bool {
	if state == nil {
		return false
	}
	return state.skipOnce || (state.avoidUntilUnix > 0 && now.UnixNano() < state.avoidUntilUnix)
}

func (a *nodeQualityAvoidance) blocksLocked(now time.Time, nodeID uint64) bool {
	return nodeAvoiding(a.nodes[nodeID], now)
}

func (a *nodeQualityAvoidance) snapshot(now time.Time) nodeQualitySnapshot {
	snap := nodeQualitySnapshot{}
	if a == nil {
		return snap
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, state := range a.nodes {
		if state == nil {
			delete(a.nodes, id)
			continue
		}
		if !nodeAvoiding(state, now) && state.consecutive == 0 && len(state.failures) == 0 {
			delete(a.nodes, id)
			continue
		}
		if nodeAvoiding(state, now) {
			if snap.blocked == nil {
				snap.blocked = make(map[uint64]struct{})
			}
			snap.blocked[id] = struct{}{}
		}
		if state.skipOnce {
			if snap.skipStreak == nil {
				snap.skipStreak = make(map[uint64]int)
			}
			snap.skipStreak[id] = state.consecutive
		}
	}
	return snap
}

func (a *nodeQualityAvoidance) stateLocked(nodeID uint64) *nodeQualityState {
	if a.nodes == nil {
		a.nodes = make(map[uint64]*nodeQualityState)
	}
	state := a.nodes[nodeID]
	if state == nil {
		state = &nodeQualityState{}
		a.nodes[nodeID] = state
	}
	return state
}

func (a *nodeQualityAvoidance) inspect(nodeID uint64) nodeQualityState {
	if a == nil {
		return nodeQualityState{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.nodes[nodeID]
	if state == nil {
		return nodeQualityState{}
	}
	copyState := *state
	copyState.failures = append([]time.Time(nil), state.failures...)
	return copyState
}

func (s *Selector) SetNodeQualityLimits(windowFailures, consecutiveFailures int, failureWindow, avoidDuration time.Duration) {
	if s == nil {
		return
	}
	s.nodeQuality.setPolicy(windowFailures, consecutiveFailures, failureWindow, avoidDuration)
}

func (s *Selector) NoteNodeQualityDegraded(nodeID uint64, now time.Time) {
	if s == nil {
		return
	}
	armedAvoid, armedSkip := s.nodeQuality.noteDegraded(nodeID, now)
	if armedAvoid {
		s.suppressNodeAccounts(nodeID, s.nodeQuality.inspect(nodeID).avoidUntil)
	}
	if !armedAvoid && !armedSkip {
		return
	}
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("node_quality_avoid_armed", "node_id", nodeID, "window", armedAvoid, "skip_once", armedSkip)
}

func (s *Selector) NoteNodeQualityDelivered(nodeID uint64, _ time.Time) {
	if s == nil {
		return
	}
	s.nodeQuality.noteDelivered(nodeID)
}

func (s *Selector) finishNodeQualitySelection(apply, fallback bool, snap nodeQualitySnapshot) {
	if s == nil || !apply || fallback {
		return
	}
	s.nodeQuality.consumeSkip(snap)
}

func filterCandidatesByNodeQuality(values []account.RoutingCandidate, indexes []int, snap nodeQualitySnapshot) ([]int, bool) {
	if !snap.active() || len(indexes) == 0 {
		return indexes, false
	}
	preferred := make([]int, 0, len(indexes))
	for _, index := range indexes {
		if index < 0 || index >= len(values) {
			continue
		}
		if snap.blocks(values[index].Credential.EgressNodeID) {
			continue
		}
		preferred = append(preferred, index)
	}
	if len(preferred) == 0 {
		return indexes, true
	}
	return preferred, false
}
