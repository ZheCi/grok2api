package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

const (
	weightIdleFull        = 10 * time.Minute
	weightSpeedFast       = 1000.0
	weightSpeedSlow       = 20000.0
	weightCallCountFull   = 1000.0
	weightInputBandMedium = 8000
	weightInputBandLarge  = 64000
	weightHourRetain      = 24 * time.Hour
	weightDayRetain       = 90 * 24 * time.Hour
	weightSaveTimeout     = 2 * time.Second
)

// AccountWeightSettings is the routing.accountWeight block. Window is one of
// 6h, 24h, 7d, 30d, 60d, 90d; empty or 0 means 24h.
type AccountWeightSettings struct {
	Window           string
	SuccessRate      float64
	CallCount        float64
	LastSuccess      float64
	NewbieProtection float64
	IdleTime         float64
	FirstTokenSpeed  float64
	NewbieDays       int
	NewbieDailyBase  int
}

type accountWeightRuntime struct {
	window           time.Duration
	hourly           bool
	successRate      float64
	callCount        float64
	lastSuccess      float64
	newbieProtection float64
	idleTime         float64
	firstTokenSpeed  float64
	newbieDays       int
	newbieDailyBase  int
}

type accountWeightStore interface {
	ListAccountWeights(ctx context.Context) ([]repository.AccountWeightSnapshot, error)
	SaveAccountWeight(ctx context.Context, snapshot repository.AccountWeightSnapshot) error
}

type weightAccount struct {
	persistMu      sync.Mutex
	hours          map[int64]*weightBucket
	days           map[int64]*weightBucket
	lastAttempt    time.Time
	lastOK         bool
	newbieDay      int
	newbieAttempts int
	newbieReserved int
	manualSet      bool
	manual         int
	revision       uint64
	savedRevision  uint64
}

type weightBucket struct {
	ok    int
	fail  int
	speed map[string]*weightSpeed
}

type weightSpeed struct {
	ms int64
	n  int64
}

type weightPersist struct {
	Hours          map[string]weightPersistBucket `json:"h,omitempty"`
	Days           map[string]weightPersistBucket `json:"d,omitempty"`
	LastAttempt    int64                          `json:"la,omitempty"`
	LastOK         bool                           `json:"lok,omitempty"`
	NewbieDay      int                            `json:"nd,omitempty"`
	NewbieAttempts int                            `json:"na,omitempty"`
	ManualSet      bool                           `json:"ms,omitempty"`
	Manual         int                            `json:"mw,omitempty"`
}

// AccountWeightView is the weight shown for an account. Manual means an
// administrator replaced the calculated score.
type AccountWeightView struct {
	Weight int  `json:"weight"`
	Manual bool `json:"weightManual"`
}

type weightPersistBucket struct {
	OK    int                           `json:"ok,omitempty"`
	Fail  int                           `json:"fail,omitempty"`
	Speed map[string]weightPersistSpeed `json:"s,omitempty"`
}

type weightPersistSpeed struct {
	MS int64 `json:"ms"`
	N  int64 `json:"n"`
}

type weightHintKey struct{}

type weightHint struct {
	model  string
	tokens int64
}

// WithWeightHint carries this request's model and estimated input size into selection.
func WithWeightHint(ctx context.Context, model string, tokens int64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, weightHintKey{}, weightHint{model: model, tokens: tokens})
}

func weightHintFrom(ctx context.Context) weightHint {
	if ctx == nil {
		return weightHint{}
	}
	hint, _ := ctx.Value(weightHintKey{}).(weightHint)
	return hint
}

type weightAttempt struct {
	account   account.Credential
	model     string
	tokens    int64
	canceled  bool
	degraded  bool
	handedOff bool
	timer     *firstTokenTimer
	lease     *accountLease
}

func defaultAccountWeightRuntime() accountWeightRuntime {
	return accountWeightRuntime{
		window: 24 * time.Hour, hourly: true,
		successRate: 0.50, newbieProtection: 0.30, idleTime: 0.20, firstTokenSpeed: 0.30,
		newbieDays: 7, newbieDailyBase: 20,
	}
}

func accountWeightRuntimeFrom(cfg AccountWeightSettings) accountWeightRuntime {
	runtime := defaultAccountWeightRuntime()
	if window, hourly, ok := parseAccountWeightWindow(cfg.Window); ok {
		runtime.window = window
		runtime.hourly = hourly
	}
	runtime.successRate = nonNegative(cfg.SuccessRate)
	runtime.callCount = nonNegative(cfg.CallCount)
	runtime.lastSuccess = nonNegative(cfg.LastSuccess)
	runtime.newbieProtection = nonNegative(cfg.NewbieProtection)
	runtime.idleTime = nonNegative(cfg.IdleTime)
	runtime.firstTokenSpeed = nonNegative(cfg.FirstTokenSpeed)
	if cfg.NewbieDays >= 0 && cfg.NewbieDays <= 90 {
		runtime.newbieDays = cfg.NewbieDays
	}
	if cfg.NewbieDailyBase > 0 {
		runtime.newbieDailyBase = cfg.NewbieDailyBase
	}
	return runtime
}

func parseAccountWeightWindow(raw string) (time.Duration, bool, bool) {
	switch raw {
	case "", "0", "24h":
		return 24 * time.Hour, true, true
	case "6h":
		return 6 * time.Hour, true, true
	case "7d":
		return 7 * 24 * time.Hour, false, true
	case "30d":
		return 30 * 24 * time.Hour, false, true
	case "60d":
		return 60 * 24 * time.Hour, false, true
	case "90d":
		return 90 * 24 * time.Hour, false, true
	default:
		return 0, false, false
	}
}

func (s *Selector) SetAccountWeight(cfg AccountWeightSettings) {
	if s == nil {
		return
	}
	s.weightMu.Lock()
	s.weightCfg = accountWeightRuntimeFrom(cfg)
	s.weightMu.Unlock()
}

func (s *Selector) weightConfig() accountWeightRuntime {
	s.weightMu.Lock()
	cfg := s.weightCfg
	s.weightMu.Unlock()
	if cfg.window <= 0 {
		return defaultAccountWeightRuntime()
	}
	return cfg
}

func (s *Selector) LoadAccountWeights(ctx context.Context) error {
	if s == nil || s.weightStore == nil {
		return nil
	}
	rows, err := s.weightStore.ListAccountWeights(ctx)
	if err != nil {
		return err
	}
	s.selectionMu.Lock()
	s.weightMu.Lock()
	if s.weights == nil {
		s.weights = map[uint64]*weightAccount{}
	}
	for _, row := range rows {
		state := decodeWeightAccount(row.Payload)
		if state == nil {
			continue
		}
		s.weights[row.AccountID] = state
		if row.LastSelectedAt != nil && s.lastSelectedAt != nil {
			s.lastSelectedAt[row.AccountID] = row.LastSelectedAt.UTC()
		}
	}
	s.weightMu.Unlock()
	s.selectionMu.Unlock()
	return nil
}

func (s *Selector) weightCapped(credential account.Credential, now time.Time) bool {
	cfg := s.weightConfig()
	if cfg.newbieDays <= 0 || cfg.newbieDailyBase <= 0 || credential.CreatedAt.IsZero() {
		return false
	}
	day := newbieDayIndex(credential.CreatedAt, now)
	if day >= cfg.newbieDays {
		return false
	}
	s.weightMu.Lock()
	state := s.weights[credential.ID]
	inFlight := 0
	if state != nil && state.newbieDay == day {
		inFlight = state.newbieAttempts + state.newbieReserved
	}
	s.weightMu.Unlock()
	return inFlight >= cfg.newbieDailyBase*(day+1)
}

func (s *Selector) reserveNewbieAttempt(credential account.Credential) bool {
	cfg := s.weightConfig()
	if s == nil || cfg.newbieDays <= 0 || cfg.newbieDailyBase <= 0 || credential.CreatedAt.IsZero() || credential.ID == 0 {
		return false
	}
	day := newbieDayIndex(credential.CreatedAt, time.Now().UTC())
	if day >= cfg.newbieDays {
		return false
	}
	s.weightMu.Lock()
	defer s.weightMu.Unlock()
	if s.weights == nil {
		s.weights = map[uint64]*weightAccount{}
	}
	state := s.weights[credential.ID]
	if state == nil {
		state = &weightAccount{}
		s.weights[credential.ID] = state
	}
	if state.newbieDay != day {
		state.newbieDay = day
		state.newbieAttempts = 0
		state.newbieReserved = 0
	}
	state.newbieReserved++
	return true
}

func (l *accountLease) releaseNewbieReservation() {
	if l == nil || l.selector == nil || !l.newbieReserved || l.newbieSettled {
		return
	}
	l.newbieSettled = true
	day := newbieDayIndex(l.Credential.CreatedAt, time.Now().UTC())
	l.selector.weightMu.Lock()
	if state := l.selector.weights[l.Credential.ID]; state != nil && state.newbieDay == day && state.newbieReserved > 0 {
		state.newbieReserved--
	}
	l.selector.weightMu.Unlock()
}

func (s *Selector) newbieCapRetry(credential account.Credential, now time.Time) time.Time {
	if credential.CreatedAt.IsZero() {
		return time.Time{}
	}
	day := newbieDayIndex(credential.CreatedAt, now)
	return credential.CreatedAt.UTC().Add(time.Duration(day+1) * 24 * time.Hour)
}

func (s *Selector) accountWeight(credential account.Credential, now time.Time, model string, tokens int64, lastSelected time.Time) int {
	cfg := s.weightConfig()
	s.weightMu.Lock()
	if forced, ok := s.weightOverrides[credential.ID]; ok {
		s.weightMu.Unlock()
		return forced
	}
	state := s.weights[credential.ID]
	if state != nil && state.manualSet {
		s.weightMu.Unlock()
		return state.manual
	}
	score := scoreAccountWeight(cfg, credential.CreatedAt, now, model, tokens, lastSelected, state, false)
	s.weightMu.Unlock()
	return score
}

func (s *Selector) AccountWeights(credentials []account.Credential) map[uint64]AccountWeightView {
	if s == nil {
		return nil
	}
	now := time.Now().UTC()
	cfg := s.weightConfig()
	out := make(map[uint64]AccountWeightView, len(credentials))
	s.selectionMu.RLock()
	s.weightMu.Lock()
	for _, credential := range credentials {
		if forced, ok := s.weightOverrides[credential.ID]; ok {
			out[credential.ID] = AccountWeightView{Weight: forced, Manual: true}
			continue
		}
		state := s.weights[credential.ID]
		if state != nil && state.manualSet {
			out[credential.ID] = AccountWeightView{Weight: state.manual, Manual: true}
			continue
		}
		out[credential.ID] = AccountWeightView{Weight: scoreAccountWeight(cfg, credential.CreatedAt, now, "", 0, s.lastSelectedAt[credential.ID], state, true)}
	}
	s.weightMu.Unlock()
	s.selectionMu.RUnlock()
	return out
}

func (s *Selector) SetManualWeight(ctx context.Context, credential account.Credential, weight *int) (AccountWeightView, error) {
	if s == nil || credential.ID == 0 {
		return AccountWeightView{}, errors.New("账号无效")
	}
	if weight != nil && (*weight < 0 || *weight > 100) {
		return AccountWeightView{}, errors.New("权重必须在 0 到 100 之间")
	}
	state := s.weightState(credential.ID)
	state.persistMu.Lock()
	defer state.persistMu.Unlock()
	s.weightMu.Lock()
	previousSet, previousManual := state.manualSet, state.manual
	if weight == nil {
		state.manualSet = false
		state.manual = 0
	} else {
		state.manualSet = true
		state.manual = *weight
	}
	state.revision++
	s.weightMu.Unlock()
	if err := s.flushWeight(ctx, credential.ID); err != nil {
		s.weightMu.Lock()
		state.manualSet = previousSet
		state.manual = previousManual
		state.revision++
		s.weightMu.Unlock()
		return AccountWeightView{}, err
	}
	shown := s.AccountWeights([]account.Credential{credential})
	return shown[credential.ID], nil
}

func (s *Selector) weightState(id uint64) *weightAccount {
	s.weightMu.Lock()
	defer s.weightMu.Unlock()
	if s.weights == nil {
		s.weights = map[uint64]*weightAccount{}
	}
	state := s.weights[id]
	if state == nil {
		state = &weightAccount{}
		s.weights[id] = state
	}
	return state
}

// flushWeight writes the account until the saved row matches memory.
// Caller must hold state.persistMu so two writers cannot store stale copies.
func (s *Selector) flushWeight(ctx context.Context, id uint64) error {
	for {
		s.selectionMu.RLock()
		selected := time.Time{}
		if s.lastSelectedAt != nil {
			selected = s.lastSelectedAt[id]
		}
		s.selectionMu.RUnlock()
		s.weightMu.Lock()
		state := s.weights[id]
		if state == nil || state.revision == state.savedRevision {
			s.weightMu.Unlock()
			return nil
		}
		payload := encodeWeightAccount(state)
		revision := state.revision
		s.weightMu.Unlock()
		if s.weightStore == nil {
			s.weightMu.Lock()
			if current := s.weights[id]; current != nil && current.revision == revision {
				current.savedRevision = revision
			}
			s.weightMu.Unlock()
			return nil
		}
		snapshot := repository.AccountWeightSnapshot{AccountID: id, Payload: payload}
		if !selected.IsZero() {
			value := selected
			snapshot.LastSelectedAt = &value
		}
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), weightSaveTimeout)
		err := s.weightStore.SaveAccountWeight(writeCtx, snapshot)
		cancel()
		if err != nil {
			return err
		}
		s.weightMu.Lock()
		current := s.weights[id]
		if current != nil && current.revision == revision {
			current.savedRevision = revision
		}
		done := current == nil || current.revision == current.savedRevision
		s.weightMu.Unlock()
		if done {
			return nil
		}
	}
}

func (s *Service) AccountWeights(credentials []account.Credential) map[uint64]AccountWeightView {
	if s == nil || s.selector == nil {
		return nil
	}
	return s.selector.AccountWeights(credentials)
}

func (s *Service) SetManualAccountWeight(ctx context.Context, credential account.Credential, weight *int) (AccountWeightView, error) {
	if s == nil || s.selector == nil {
		return AccountWeightView{}, errors.New("权重服务不可用")
	}
	return s.selector.SetManualWeight(ctx, credential, weight)
}

func (s *Selector) recordWeightAttempt(ctx context.Context, attempt weightAttempt, outcome string, tokens int64) {
	if s == nil || attempt.account.ID == 0 || outcome == "cancel" {
		return
	}
	if tokens <= 0 {
		tokens = attempt.tokens
	}
	now := time.Now().UTC()
	var first time.Duration
	hasFirst := false
	if attempt.timer != nil {
		if ms := attempt.timer.milliseconds(); ms != nil {
			hasFirst = true
			first = time.Duration(*ms) * time.Millisecond
		}
	}
	state := s.weightState(attempt.account.ID)
	state.persistMu.Lock()
	defer state.persistMu.Unlock()
	s.weightMu.Lock()
	cfg := s.weightCfg
	if cfg.window <= 0 {
		cfg = defaultAccountWeightRuntime()
	}
	state.add(cfg, attempt.account.CreatedAt, now, outcome == "success", attempt.model, tokens, hasFirst, first)
	state.revision++
	s.weightMu.Unlock()
	if err := s.flushWeight(ctx, attempt.account.ID); err != nil && s.logger != nil {
		s.logger.Warn("account_weight_save_failed", "account_id", attempt.account.ID, "error", err)
	}
}

func (s *Service) openWeightAttempt(slot **weightAttempt, lease *accountLease, credential account.Credential, model string, tokens int64, timer *firstTokenTimer) {
	if s == nil || slot == nil {
		return
	}
	if lease != nil {
		lease.weightOwned = true
	}
	if *slot != nil && !(*slot).handedOff {
		s.commitWeight(context.Background(), *slot, "fail", 0)
	}
	*slot = &weightAttempt{account: credential, model: model, tokens: tokens, timer: timer, lease: lease}
}

func (s *Service) commitWeight(ctx context.Context, attempt *weightAttempt, outcome string, tokens int64) {
	if s == nil || s.selector == nil || attempt == nil {
		return
	}
	if attempt.lease != nil {
		attempt.lease.weightFinished = true
		if outcome != "cancel" {
			attempt.lease.releaseNewbieReservation()
		}
	}
	s.selector.recordWeightAttempt(ctx, *attempt, outcome, tokens)
}

func (state *weightAccount) add(cfg accountWeightRuntime, createdAt, now time.Time, success bool, model string, tokens int64, hasFirst bool, first time.Duration) {
	var buckets map[int64]*weightBucket
	var key int64
	if cfg.hourly {
		if state.hours == nil {
			state.hours = map[int64]*weightBucket{}
		}
		buckets = state.hours
		key = now.UTC().Truncate(time.Hour).Unix()
		pruneWeightBuckets(buckets, now.Add(-weightHourRetain).Unix())
	} else {
		if state.days == nil {
			state.days = map[int64]*weightBucket{}
		}
		buckets = state.days
		key = now.UTC().Truncate(24 * time.Hour).Unix()
		pruneWeightBuckets(buckets, now.Add(-weightDayRetain).Unix())
	}
	bucket := buckets[key]
	if bucket == nil {
		bucket = &weightBucket{}
		buckets[key] = bucket
	}
	if success {
		bucket.ok++
	} else {
		bucket.fail++
	}
	if hasFirst {
		if bucket.speed == nil {
			bucket.speed = map[string]*weightSpeed{}
		}
		speedKey := weightSpeedKey(model, tokens)
		speed := bucket.speed[speedKey]
		if speed == nil {
			speed = &weightSpeed{}
			bucket.speed[speedKey] = speed
		}
		speed.ms += first.Milliseconds()
		speed.n++
	}
	state.lastAttempt = now
	state.lastOK = success
	day := newbieDayIndex(createdAt, now)
	if state.newbieDay != day {
		state.newbieDay = day
		state.newbieAttempts = 0
	}
	state.newbieAttempts++
}

func scoreAccountWeight(cfg accountWeightRuntime, createdAt, now time.Time, model string, tokens int64, lastSelected time.Time, state *weightAccount, neutralSpeed bool) int {
	ratios := []float64{cfg.successRate, cfg.callCount, cfg.lastSuccess, cfg.newbieProtection, cfg.idleTime, cfg.firstTokenSpeed}
	total := 0.0
	for _, ratio := range ratios {
		total += ratio
	}
	if total <= 0 {
		return 0
	}
	ok, fail, speedMS, speedN := 0, 0, int64(0), int64(0)
	if state != nil {
		ok, fail, speedMS, speedN = state.totals(now, cfg.window, cfg.hourly, model, tokens)
	}
	attempts := ok + fail
	successScore := 0.5
	callScore := 0.5
	lastScore := 0.5
	if attempts > 0 {
		successScore = float64(ok+2) / float64(attempts+4)
		callScore = math.Log(1+float64(attempts)) / math.Log(1+weightCallCountFull)
		if callScore > 1 {
			callScore = 1
		}
	}
	if state != nil && !state.lastAttempt.IsZero() && !state.lastAttempt.Before(now.Add(-cfg.window)) {
		lastScore = 0
		if state.lastOK {
			lastScore = 1
		}
	}
	newbieScore := 1.0
	if cfg.newbieDays > 0 && !createdAt.IsZero() {
		day := newbieDayIndex(createdAt, now)
		if day < cfg.newbieDays {
			newbieScore = float64(day) / float64(cfg.newbieDays)
		}
	}
	idleScore := 1.0
	if !lastSelected.IsZero() {
		idle := now.Sub(lastSelected)
		if idle < 0 {
			idle = 0
		}
		idleScore = idle.Seconds() / weightIdleFull.Seconds()
		if idleScore > 1 {
			idleScore = 1
		}
	}
	speedScore := 0.5
	if !neutralSpeed && speedN > 0 {
		average := float64(speedMS) / float64(speedN)
		speedScore = (weightSpeedSlow - average) / (weightSpeedSlow - weightSpeedFast)
		if speedScore < 0 {
			speedScore = 0
		}
		if speedScore > 1 {
			speedScore = 1
		}
	}
	parts := []float64{successScore, callScore, lastScore, newbieScore, idleScore, speedScore}
	weighted := 0.0
	for index, ratio := range ratios {
		weighted += (ratio / total) * parts[index]
	}
	score := int(math.Round(100 * weighted))
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func (state *weightAccount) totals(now time.Time, window time.Duration, hourly bool, model string, tokens int64) (ok int, fail int, speedMS int64, speedN int64) {
	if state == nil {
		return 0, 0, 0, 0
	}
	cutoff := now.Add(-window).Unix()
	buckets := state.days
	if hourly {
		buckets = state.hours
	}
	key := weightSpeedKey(model, tokens)
	for stamp, bucket := range buckets {
		if stamp < cutoff || bucket == nil {
			continue
		}
		ok += bucket.ok
		fail += bucket.fail
		if speed := bucket.speed[key]; speed != nil {
			speedMS += speed.ms
			speedN += speed.n
		}
	}
	return ok, fail, speedMS, speedN
}

func newbieDayIndex(createdAt, now time.Time) int {
	if createdAt.IsZero() {
		return 0
	}
	age := now.Sub(createdAt.UTC())
	if age < 0 {
		return 0
	}
	return int(age / (24 * time.Hour))
}

func weightSpeedKey(model string, tokens int64) string {
	band := 0
	if tokens >= weightInputBandLarge {
		band = 2
	} else if tokens >= weightInputBandMedium {
		band = 1
	}
	return model + "|" + strconv.Itoa(band)
}

func pruneWeightBuckets(buckets map[int64]*weightBucket, cutoff int64) {
	for key := range buckets {
		if key < cutoff {
			delete(buckets, key)
		}
	}
}

func nonNegative(value float64) float64 {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func encodeWeightAccount(state *weightAccount) string {
	if state == nil {
		return ""
	}
	payload := weightPersist{NewbieDay: state.newbieDay, NewbieAttempts: state.newbieAttempts, LastOK: state.lastOK, ManualSet: state.manualSet, Manual: state.manual}
	if !state.lastAttempt.IsZero() {
		payload.LastAttempt = state.lastAttempt.UTC().Unix()
	}
	payload.Hours = encodeWeightBuckets(state.hours)
	payload.Days = encodeWeightBuckets(state.days)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func encodeWeightBuckets(buckets map[int64]*weightBucket) map[string]weightPersistBucket {
	if len(buckets) == 0 {
		return nil
	}
	out := make(map[string]weightPersistBucket, len(buckets))
	for key, bucket := range buckets {
		if bucket == nil {
			continue
		}
		item := weightPersistBucket{OK: bucket.ok, Fail: bucket.fail}
		if len(bucket.speed) > 0 {
			item.Speed = make(map[string]weightPersistSpeed, len(bucket.speed))
			for speedKey, speed := range bucket.speed {
				if speed != nil && speed.n > 0 {
					item.Speed[speedKey] = weightPersistSpeed{MS: speed.ms, N: speed.n}
				}
			}
		}
		out[strconv.FormatInt(key, 10)] = item
	}
	return out
}

func decodeWeightAccount(payload string) *weightAccount {
	if payload == "" {
		return &weightAccount{}
	}
	var stored weightPersist
	if json.Unmarshal([]byte(payload), &stored) != nil {
		return &weightAccount{}
	}
	state := &weightAccount{hours: decodeWeightBuckets(stored.Hours), days: decodeWeightBuckets(stored.Days), lastOK: stored.LastOK, newbieDay: stored.NewbieDay, newbieAttempts: stored.NewbieAttempts, manualSet: stored.ManualSet, manual: stored.Manual}
	if stored.LastAttempt > 0 {
		state.lastAttempt = time.Unix(stored.LastAttempt, 0).UTC()
	}
	return state
}

func decodeWeightBuckets(raw map[string]weightPersistBucket) map[int64]*weightBucket {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[int64]*weightBucket, len(raw))
	for key, item := range raw {
		stamp, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			continue
		}
		bucket := &weightBucket{ok: item.OK, fail: item.Fail}
		if len(item.Speed) > 0 {
			bucket.speed = make(map[string]*weightSpeed, len(item.Speed))
			for speedKey, speed := range item.Speed {
				bucket.speed[speedKey] = &weightSpeed{ms: speed.MS, n: speed.N}
			}
		}
		out[stamp] = bucket
	}
	return out
}
