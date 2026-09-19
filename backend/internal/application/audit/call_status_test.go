package audit

import (
	"context"
	"log/slog"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	auditdomain "github.com/chenyme/grok2api/backend/internal/domain/audit"
	"github.com/chenyme/grok2api/backend/internal/infra/persistence/relational"
)

func TestAccountCallStatusClassifiesAndExcludesProbes(t *testing.T) {
	ctx := context.Background()
	database, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "call-status.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	repo := relational.NewAuditRepository(database)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	accountID := uint64(2829)
	first := int64(200)
	if err := repo.CreateBatch(ctx, []auditdomain.Record{
		{RequestID: "healthy", ClientKeyID: 1, ModelRouteID: 1, AccountID: &accountID, AccountName: "hot", StatusCode: 200, Streaming: true, OutputTokens: 100, FirstTokenMS: &first, DurationMS: 2200, CreatedAt: now.Add(-3 * time.Minute)},
		{RequestID: "hard", ClientKeyID: 1, ModelRouteID: 1, AccountID: &accountID, StatusCode: 200, Streaming: true, OutputTokens: 2000, FirstTokenMS: &first, DurationMS: 1200, CreatedAt: now.Add(-2 * time.Minute)},
		{RequestID: "failed", ClientKeyID: 1, ModelRouteID: 1, AccountID: &accountID, StatusCode: 503, ErrorCode: "upstream_unavailable", CreatedAt: now.Add(-time.Minute)},
		{RequestID: "quality_skip", ClientKeyID: 1, ModelRouteID: 1, AccountID: &accountID, StatusCode: 200, Streaming: true, OutputTokens: 2000, FirstTokenMS: &first, DurationMS: 1200, CreatedAt: now.Add(-30 * time.Second)},
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(repo, slog.Default(), 8, 4, time.Second)
	service.now = func() time.Time { return now }

	batch, err := service.AccountCallStatus(ctx, []uint64{accountID, 99}, 32, CallStatusThresholds{})
	if err != nil {
		t.Fatal(err)
	}
	status := batch.Accounts[accountID]
	if status.SampleCount != 3 || status.Counts.Healthy != 1 || status.Counts.Degraded != 1 || status.Counts.Failed != 1 {
		t.Fatalf("status = %#v", status)
	}
	if len(status.Samples) != 3 || status.Samples[0].Level != int(auditdomain.CallLevelHealthy) || status.Samples[2].Level != int(auditdomain.CallLevelFailed) {
		t.Fatalf("samples = %#v", status.Samples)
	}
	empty := batch.Accounts[99]
	if empty.SampleCount != 0 || empty.Samples == nil || len(empty.Samples) != 0 {
		t.Fatalf("missing account = %#v", empty)
	}

	if _, err := service.AccountCallStatus(ctx, make([]uint64, 101), 32, CallStatusThresholds{}); err != ErrInvalidAccountIDs {
		t.Fatalf("too many ids error = %v", err)
	}

	detail, err := service.AccountCallStatusDetail(ctx, accountID, "1h", false, CallStatusThresholds{})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Totals.Calls != 3 || detail.AccountName != "hot" || len(detail.Points) != 3 {
		t.Fatalf("detail = %#v", detail)
	}
	if detail.Points[1].Level != "degraded" || detail.Points[1].Class != auditdomain.DegradeClassHard {
		t.Fatalf("hard point = %#v", detail.Points[1])
	}

	withProbes, err := service.AccountCallStatusDetail(ctx, accountID, "1h", true, CallStatusThresholds{})
	if err != nil {
		t.Fatal(err)
	}
	if withProbes.Totals.Calls != 4 {
		t.Fatalf("include probes = %#v", withProbes.Totals)
	}

	if _, err := service.AccountCallStatusDetail(ctx, accountID, "3h", false, CallStatusThresholds{}); err != ErrInvalidPeriod {
		t.Fatalf("bad window = %v", err)
	}

	trend, err := service.RequestCallStatus(ctx, "", "24h", ListFilter{}, CallStatusThresholds{})
	if err != nil {
		t.Fatal(err)
	}
	if trend.Period != Period24Hours || trend.Totals.Calls != 4 || len(trend.Points) != 4 {
		t.Fatalf("trend = %#v", trend)
	}
	if trend.Points[0].Level != "healthy" || trend.Points[0].AccountName != "hot" {
		t.Fatalf("healthy point = %#v", trend.Points[0])
	}
	if trend.Points[1].Level != "degraded" || trend.Points[1].Class != auditdomain.DegradeClassHard {
		t.Fatalf("hard point = %#v", trend.Points[1])
	}

	filtered, err := service.RequestCallStatus(ctx, "", "24h", ListFilter{Status: "5xx"}, CallStatusThresholds{})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Totals.Calls != 1 || filtered.Points[0].RequestID != "failed" {
		t.Fatalf("filtered trend = %#v", filtered)
	}

	if _, err := service.RequestCallStatus(ctx, "", "3h", ListFilter{}, CallStatusThresholds{}); err != ErrInvalidPeriod {
		t.Fatalf("bad period = %v", err)
	}
}

func TestRequestCallStatusTruncatesToHistoryCap(t *testing.T) {
	ctx := context.Background()
	database, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "call-status-cap.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	repo := relational.NewAuditRepository(database)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	accountID := uint64(41)
	records := make([]auditdomain.Record, 0, 3)
	for index := 0; index < 3; index++ {
		records = append(records, auditdomain.Record{
			RequestID: "cap-" + strconv.Itoa(index), ClientKeyID: 1, ModelRouteID: 1, AccountID: &accountID,
			StatusCode: 200, CreatedAt: now.Add(-time.Duration(3-index) * time.Minute),
		})
	}
	if err := repo.CreateBatch(ctx, records); err != nil {
		t.Fatal(err)
	}
	previousCap := callStatusHistoryCap
	callStatusHistoryCap = 2
	t.Cleanup(func() { callStatusHistoryCap = previousCap })
	service := NewService(repo, slog.Default(), 8, 4, time.Second)
	service.now = func() time.Time { return now }

	trend, err := service.RequestCallStatus(ctx, "", "24h", ListFilter{}, CallStatusThresholds{})
	if err != nil {
		t.Fatal(err)
	}
	if !trend.Truncated || trend.Totals.Calls != 2 || len(trend.Points) != 2 {
		t.Fatalf("truncated trend = %#v", trend)
	}
	if trend.Points[0].RequestID != "cap-1" || trend.Points[1].RequestID != "cap-2" {
		t.Fatalf("kept points = %s, %s", trend.Points[0].RequestID, trend.Points[1].RequestID)
	}

	detail, err := service.AccountCallStatusDetail(ctx, accountID, "1h", false, CallStatusThresholds{})
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Truncated || detail.Totals.Calls != 2 || detail.Points[0].RequestID != "cap-1" || detail.Points[1].RequestID != "cap-2" {
		t.Fatalf("truncated detail = %#v", detail)
	}
}
