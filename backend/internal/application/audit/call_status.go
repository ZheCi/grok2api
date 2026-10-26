package audit

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	auditdomain "github.com/chenyme/grok2api/backend/internal/domain/audit"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

const (
	callStatusDefaultLimit = 32
	callStatusMinLimit     = 8
	callStatusMaxLimit     = 64
	callStatusMaxAccounts  = 100
	callStatusCacheTTL     = 10 * time.Second
)

// callStatusHistoryCap is the maximum number of audit rows returned for request
// trends and account call-status details. Tests may lower it.
var callStatusHistoryCap = 2000

var (
	ErrInvalidAccountIDs = fmt.Errorf("%w: accountIds", ErrInvalidFilter)
	ErrInvalidAccountID  = fmt.Errorf("%w: accountId", ErrInvalidFilter)
)

type CallStatusThresholds struct {
	SoftTPS    float64 `json:"softTPS"`
	HardTPS    float64 `json:"hardTPS"`
	MinGenMS   int64   `json:"minGenMs"`
	FailClosed bool    `json:"failClosed"`
}

type CallStatusCounts struct {
	Healthy  int `json:"healthy"`
	Soft     int `json:"soft"`
	Degraded int `json:"degraded"`
	Failed   int `json:"failed"`
}

type CallStatusSample struct {
	ID    uint64    `json:"id,string"`
	T     time.Time `json:"t"`
	Level int       `json:"l"`
}

type AccountCallStatus struct {
	AccountID   uint64             `json:"accountId,string"`
	SampleCount int                `json:"sampleCount"`
	Counts      CallStatusCounts   `json:"counts"`
	Last        *time.Time         `json:"last,omitempty"`
	Samples     []CallStatusSample `json:"samples"`
}

type CallStatusBatch struct {
	GeneratedAt time.Time                    `json:"generatedAt"`
	Thresholds  CallStatusThresholds         `json:"thresholds"`
	Accounts    map[uint64]AccountCallStatus `json:"-"`
}

type CallStatusPoint struct {
	ID           uint64    `json:"id,string"`
	RequestID    string    `json:"requestId"`
	T            time.Time `json:"t"`
	Level        string    `json:"level"`
	StatusCode   int       `json:"statusCode"`
	ErrorCode    string    `json:"errorCode,omitempty"`
	Class        string    `json:"class,omitempty"`
	TPS          float64   `json:"tps"`
	FirstTokenMS *int64    `json:"firstTokenMs,omitempty"`
	DurationMS   int64     `json:"durationMs"`
	OutputTokens int64     `json:"outputTokens"`
	Model        string    `json:"model,omitempty"`
	AccountName  string    `json:"accountName,omitempty"`
	NodeName     string    `json:"nodeName,omitempty"`
}

type CallStatusDetail struct {
	AccountID   uint64                 `json:"accountId,string"`
	AccountName string                 `json:"accountName,omitempty"`
	Window      string                 `json:"window"`
	Range       CallStatusRange        `json:"range"`
	Thresholds  CallStatusThresholds   `json:"thresholds"`
	Totals      CallStatusDetailTotals `json:"totals"`
	Points      []CallStatusPoint      `json:"points"`
	Truncated   bool                   `json:"truncated"`
}

type CallStatusRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type CallStatusDetailTotals struct {
	Calls    int     `json:"calls"`
	Healthy  int     `json:"healthy"`
	Soft     int     `json:"soft"`
	Degraded int     `json:"degraded"`
	Failed   int     `json:"failed"`
	MaxTPS   float64 `json:"maxTPS"`
}

type RequestCallStatus struct {
	Period     Period                 `json:"period"`
	Range      CallStatusRange        `json:"range"`
	Thresholds CallStatusThresholds   `json:"thresholds"`
	Totals     CallStatusDetailTotals `json:"totals"`
	Points     []CallStatusPoint      `json:"points"`
	Truncated  bool                   `json:"truncated"`
}

func (s *Service) AccountCallStatus(ctx context.Context, accountIDs []uint64, limit int, thresholds CallStatusThresholds) (CallStatusBatch, error) {
	ids, err := normalizeAccountIDs(accountIDs)
	if err != nil {
		return CallStatusBatch{}, err
	}
	limit = normalizeCallStatusLimit(limit)
	thresholds = normalizeCallStatusThresholds(thresholds)
	if len(ids) == 0 {
		now := s.now().UTC()
		return CallStatusBatch{GeneratedAt: now, Thresholds: thresholds, Accounts: map[uint64]AccountCallStatus{}}, nil
	}
	cacheKey := callStatusCacheKey(ids, limit, thresholds)
	return s.callStatusCache.Load(ctx, cacheKey, s.now().UTC(), func() (CallStatusBatch, error) {
		return s.loadAccountCallStatus(ctx, ids, limit, thresholds)
	})
}

func (s *Service) loadAccountCallStatus(ctx context.Context, ids []uint64, limit int, thresholds CallStatusThresholds) (CallStatusBatch, error) {
	records, err := s.audits.ListRecentByAccounts(ctx, repository.AccountCallStatusQuery{
		AccountIDs: ids, Limit: limit, IncludeProbes: false,
	})
	if err != nil {
		return CallStatusBatch{}, err
	}
	grouped := make(map[uint64][]auditdomain.Record, len(ids))
	for _, record := range records {
		if record.AccountID == nil {
			continue
		}
		grouped[*record.AccountID] = append(grouped[*record.AccountID], record)
	}
	accounts := make(map[uint64]AccountCallStatus, len(ids))
	for _, id := range ids {
		rows := grouped[id]
		// Repository returns newest first; sparkline is oldest → newest.
		samples := make([]CallStatusSample, 0, len(rows))
		counts := CallStatusCounts{}
		var last *time.Time
		for index := len(rows) - 1; index >= 0; index-- {
			record := rows[index]
			classified := auditdomain.ClassifyCall(record, thresholds.SoftTPS, thresholds.HardTPS, thresholds.MinGenMS, thresholds.FailClosed)
			incrementCallCounts(&counts, classified.Level)
			created := record.CreatedAt.UTC()
			if last == nil || created.After(*last) {
				lastCopy := created
				last = &lastCopy
			}
			samples = append(samples, CallStatusSample{ID: record.ID, T: created, Level: int(classified.Level)})
		}
		accounts[id] = AccountCallStatus{AccountID: id, SampleCount: len(samples), Counts: counts, Last: last, Samples: samples}
	}
	return CallStatusBatch{GeneratedAt: s.now().UTC(), Thresholds: thresholds, Accounts: accounts}, nil
}

func (s *Service) AccountCallStatusDetail(ctx context.Context, accountID uint64, window string, includeProbes bool, thresholds CallStatusThresholds) (CallStatusDetail, error) {
	if accountID == 0 {
		return CallStatusDetail{}, ErrInvalidAccountID
	}
	window, start, end, err := resolveDegradeWindow(window, s.now().UTC())
	if err != nil {
		return CallStatusDetail{}, err
	}
	thresholds = normalizeCallStatusThresholds(thresholds)
	records, truncated, err := loadCallStatusHistory(s.audits.ListByAccountSince(ctx, repository.AccountCallHistoryQuery{
		AccountID: accountID, Start: start, End: end, Limit: callStatusHistoryLimit(), IncludeProbes: includeProbes,
	}))
	if err != nil {
		return CallStatusDetail{}, err
	}
	points, totals := classifyCallStatusRecords(records, thresholds)
	accountName := ""
	if len(records) > 0 {
		accountName = records[0].AccountName
		for _, record := range records {
			if record.AccountName != "" {
				accountName = record.AccountName
				break
			}
		}
	}
	return CallStatusDetail{
		AccountID: accountID, AccountName: accountName, Window: window,
		Range: CallStatusRange{Start: start, End: end}, Thresholds: thresholds,
		Totals: totals, Points: points, Truncated: truncated,
	}, nil
}

func (s *Service) RequestCallStatus(ctx context.Context, search, rawPeriod string, filter ListFilter, thresholds CallStatusThresholds) (RequestCallStatus, error) {
	if !validAuditFilter(filter.Status, "", "success", "clientError", "serverError", "2xx", "4xx", "5xx", "other") || !validAuditFilter(filter.Mode, "", "stream", "nonStream") {
		return RequestCallStatus{}, ErrInvalidFilter
	}
	period, start, end, err := s.resolvePeriod(rawPeriod)
	if err != nil {
		return RequestCallStatus{}, err
	}
	thresholds = normalizeCallStatusThresholds(thresholds)
	cacheKey := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%g\x00%g\x00%d\x00%t", period, search, filter.Model, filter.Status, filter.Mode, filter.Key, filter.Account, thresholds.SoftTPS, thresholds.HardTPS, thresholds.MinGenMS, thresholds.FailClosed)
	return s.requestCallStatusCache.Load(ctx, cacheKey, end, func() (RequestCallStatus, error) {
		return s.loadRequestCallStatus(ctx, search, filter, period, start, end, thresholds)
	})
}

func (s *Service) loadRequestCallStatus(ctx context.Context, search string, filter ListFilter, period Period, start, end time.Time, thresholds CallStatusThresholds) (RequestCallStatus, error) {
	records, err := s.audits.ListRecentSince(ctx, repository.AuditHistoryQuery{
		Start: start, End: end, Search: search, RequestLimit: callStatusHistoryLimit(),
		Filter: repository.AuditListFilter{Model: filter.Model, Status: filter.Status, Mode: filter.Mode, Key: filter.Key, Account: filter.Account},
	})
	if err != nil {
		return RequestCallStatus{}, err
	}
	records, truncated := keepNewestRequests(records, callStatusHistoryCap)
	points, totals := classifyCallStatusRecords(records, thresholds)
	return RequestCallStatus{
		Period: period, Range: CallStatusRange{Start: start, End: end}, Thresholds: thresholds,
		Totals: totals, Points: points, Truncated: truncated,
	}, nil
}

func callStatusHistoryLimit() int {
	if callStatusHistoryCap < 1 {
		return 2
	}
	return callStatusHistoryCap + 1
}

// keepNewestRequests drops the oldest request when records, newest first,
// cover more than cap distinct request ids. Every row of a kept request stays.
func keepNewestRequests(records []auditdomain.Record, cap int) ([]auditdomain.Record, bool) {
	if cap < 1 || len(records) == 0 {
		return records, false
	}
	seen := make(map[string]struct{})
	order := make([]string, 0)
	for _, record := range records {
		if _, ok := seen[record.RequestID]; ok {
			continue
		}
		seen[record.RequestID] = struct{}{}
		order = append(order, record.RequestID)
	}
	if len(order) <= cap {
		return records, false
	}
	drop := order[len(order)-1]
	kept := make([]auditdomain.Record, 0, len(records))
	for _, record := range records {
		if record.RequestID != drop {
			kept = append(kept, record)
		}
	}
	return kept, true
}

func loadCallStatusHistory(records []auditdomain.Record, err error) ([]auditdomain.Record, bool, error) {
	if err != nil {
		return nil, false, err
	}
	truncated := callStatusHistoryCap > 0 && len(records) > callStatusHistoryCap
	if truncated {
		records = records[:callStatusHistoryCap]
	}
	return records, truncated, nil
}

func classifyCallStatusRecords(records []auditdomain.Record, thresholds CallStatusThresholds) ([]CallStatusPoint, CallStatusDetailTotals) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID < records[j].ID
		}
		return records[i].CreatedAt.Before(records[j].CreatedAt)
	})
	points := make([]CallStatusPoint, 0, len(records))
	totals := CallStatusDetailTotals{}
	for _, record := range records {
		classified := auditdomain.ClassifyCall(record, thresholds.SoftTPS, thresholds.HardTPS, thresholds.MinGenMS, thresholds.FailClosed)
		switch classified.Level {
		case auditdomain.CallLevelHealthy:
			totals.Healthy++
		case auditdomain.CallLevelSoft:
			totals.Soft++
		case auditdomain.CallLevelDegraded:
			totals.Degraded++
		default:
			totals.Failed++
		}
		if classified.TPS > totals.MaxTPS {
			totals.MaxTPS = classified.TPS
		}
		model := record.ModelUpstreamModel
		if model == "" {
			model = record.ModelPublicID
		}
		points = append(points, CallStatusPoint{
			ID: record.ID, RequestID: record.RequestID, T: record.CreatedAt.UTC(),
			Level: classified.Level.String(), StatusCode: record.StatusCode, ErrorCode: record.ErrorCode,
			Class: classified.Class, TPS: round(classified.TPS, 2), FirstTokenMS: record.FirstTokenMS,
			DurationMS: record.DurationMS, OutputTokens: record.OutputTokens, Model: model,
			AccountName: record.AccountName, NodeName: record.EgressNodeName,
		})
	}
	totals.Calls = len(points)
	totals.MaxTPS = round(totals.MaxTPS, 2)
	return points, totals
}

func incrementCallCounts(counts *CallStatusCounts, level auditdomain.CallLevel) {
	switch level {
	case auditdomain.CallLevelHealthy:
		counts.Healthy++
	case auditdomain.CallLevelSoft:
		counts.Soft++
	case auditdomain.CallLevelDegraded:
		counts.Degraded++
	default:
		counts.Failed++
	}
}

func normalizeCallStatusLimit(limit int) int {
	if limit < callStatusMinLimit || limit > callStatusMaxLimit {
		return callStatusDefaultLimit
	}
	return limit
}

func normalizeCallStatusThresholds(value CallStatusThresholds) CallStatusThresholds {
	normalized := normalizeDegradeThresholds(DegradeThresholds{
		SoftTPS: value.SoftTPS, HardTPS: value.HardTPS, MinGenMS: value.MinGenMS, FailClosed: value.FailClosed,
	})
	return CallStatusThresholds{SoftTPS: normalized.SoftTPS, HardTPS: normalized.HardTPS, MinGenMS: normalized.MinGenMS, FailClosed: normalized.FailClosed}
}

func normalizeAccountIDs(values []uint64) ([]uint64, error) {
	if len(values) > callStatusMaxAccounts {
		return nil, ErrInvalidAccountIDs
	}
	seen := make(map[uint64]struct{}, len(values))
	out := make([]uint64, 0, len(values))
	for _, id := range values {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func callStatusCacheKey(ids []uint64, limit int, thresholds CallStatusThresholds) string {
	parts := make([]string, 0, len(ids)+4)
	for _, id := range ids {
		parts = append(parts, strconv.FormatUint(id, 10))
	}
	return fmt.Sprintf("%s\x00%d\x00%g\x00%g\x00%d\x00%t", strings.Join(parts, ","), limit, thresholds.SoftTPS, thresholds.HardTPS, thresholds.MinGenMS, thresholds.FailClosed)
}
