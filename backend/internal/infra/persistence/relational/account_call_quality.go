package relational

import (
	"fmt"

	"github.com/chenyme/grok2api/backend/internal/domain/audit"
	"github.com/chenyme/grok2api/backend/internal/repository"
	"gorm.io/gorm"
)

const accountCallQualitySampleLimit = 32

func (r *AccountRepository) applyAccountCallQualityFilter(query *gorm.DB, filter repository.AccountListFilter) *gorm.DB {
	if !hasCallQualityFilter(filter) {
		return query
	}
	includeNeverCalled := includesNeverCalled(filter)
	onlyNeverCalled := includeNeverCalled && excludesCalledAccounts(filter)
	probePattern := audit.QualityProbeRequestPrefix + "%"
	neverCalled := `NOT EXISTS (
SELECT 1 FROM request_audits a
WHERE a.account_id = provider_accounts.id AND a.request_id NOT LIKE ?
)`
	if onlyNeverCalled {
		return query.Where(neverCalled, probePattern)
	}
	stats := r.accountCallQualityStatsQuery(query, filter)
	filtered := r.callQualityDB(query).Table("(?) AS s", stats)
	filtered = applyCallCountRange(filtered, filter)
	filtered = applyFailStreakRange(filtered, filter)
	filtered = applyFailRateRange(filtered, filter)
	ids := filtered.Select("s.account_id")
	if includeNeverCalled {
		return query.Where("provider_accounts.id IN (?) OR "+neverCalled, ids, probePattern)
	}
	return query.Where("provider_accounts.id IN (?)", ids)
}

func hasCallQualityFilter(filter repository.AccountListFilter) bool {
	return filter.CallCountMin != nil || filter.CallCountMax != nil ||
		filter.FailStreakMin != nil || filter.FailStreakMax != nil ||
		filter.FailRateMin != nil || filter.FailRateMax != nil
}

func includesNeverCalled(filter repository.AccountListFilter) bool {
	if filter.CallCountMin != nil && *filter.CallCountMin > 0 {
		return false
	}
	if filter.FailStreakMin != nil && *filter.FailStreakMin > 0 {
		return false
	}
	if filter.FailRateMin != nil && *filter.FailRateMin > 0 {
		return false
	}
	return true
}

func excludesCalledAccounts(filter repository.AccountListFilter) bool {
	return filter.CallCountMax != nil && *filter.CallCountMax == 0
}

func applyCallCountRange(query *gorm.DB, filter repository.AccountListFilter) *gorm.DB {
	if filter.CallCountMin != nil {
		query = query.Where("s.calls >= ?", *filter.CallCountMin)
	}
	if filter.CallCountMax != nil {
		query = query.Where("s.calls <= ?", *filter.CallCountMax)
	}
	return query
}

func applyFailStreakRange(query *gorm.DB, filter repository.AccountListFilter) *gorm.DB {
	if filter.FailStreakMin != nil {
		query = query.Where("s.fail_streak >= ?", *filter.FailStreakMin)
	}
	if filter.FailStreakMax != nil {
		query = query.Where("s.fail_streak <= ?", *filter.FailStreakMax)
	}
	return query
}

func applyFailRateRange(query *gorm.DB, filter repository.AccountListFilter) *gorm.DB {
	rate := "(CAST(s.failed AS REAL) * 100.0 / NULLIF(s.calls, 0))"
	if filter.FailRateMin != nil {
		query = query.Where(rate+" >= ?", *filter.FailRateMin)
	}
	if filter.FailRateMax != nil {
		query = query.Where(rate+" <= ?", *filter.FailRateMax)
	}
	return query
}

func (r *AccountRepository) callQualityDB(query *gorm.DB) *gorm.DB {
	if query != nil && query.Statement != nil && query.Statement.Context != nil {
		return r.db.db.WithContext(query.Statement.Context)
	}
	return r.db.db
}

func (r *AccountRepository) accountCallQualityStatsQuery(query *gorm.DB, filter repository.AccountListFilter) *gorm.DB {
	classified := r.accountCallQualityClassifiedQuery(query, filter)
	return r.callQualityDB(query).Table("(?) AS q", classified).
		Select(`q.account_id,
COUNT(*) AS calls,
COALESCE(SUM(CASE WHEN q.failed = 1 THEN 1 ELSE 0 END), 0) AS failed,
COALESCE(MIN(CASE WHEN q.failed = 0 THEN q.rn END), COUNT(*) + 1) - 1 AS fail_streak`).
		Group("q.account_id")
}

func (r *AccountRepository) accountCallQualityClassifiedQuery(query *gorm.DB, filter repository.AccountListFilter) *gorm.DB {
	db := r.callQualityDB(query)
	castType := "REAL"
	if r.db.dialect == "postgres" {
		castType = "DOUBLE PRECISION"
	}
	generationExpression := fmt.Sprintf(
		"(CASE WHEN a.reasoning_tokens > 0 AND a.duration_ms - a.first_token_ms < a.first_token_ms AND a.duration_ms - a.first_token_ms < %d THEN a.duration_ms ELSE a.duration_ms - a.first_token_ms END)",
		audit.DefaultDegradeMinGenMS,
	)
	tpsExpression := fmt.Sprintf("(CAST(a.output_tokens AS %s) * 1000.0 / NULLIF(%s, 0))", castType, generationExpression)
	softTPS := audit.DefaultDegradeSoftTPS
	if filter.SoftTPS != nil {
		softTPS = *filter.SoftTPS
	}
	hardTPS := audit.DefaultDegradeHardTPS
	if filter.HardTPS != nil {
		hardTPS = *filter.HardTPS
	}
	minGenMS := audit.DefaultDegradeMinGenMS
	if filter.MinGenMS != nil {
		minGenMS = *filter.MinGenMS
	}
	failClosed := false
	if filter.FailClosed != nil {
		failClosed = *filter.FailClosed
	}
	failedExpr := fmt.Sprintf(`(CASE
WHEN a.error_code = ? THEN 1
WHEN a.status_code >= 500 THEN 1
WHEN a.status_code >= 400 THEN 1
WHEN a.error_code IS NOT NULL AND a.error_code <> '' THEN 1
WHEN a.status_code >= 200 AND a.status_code < 300
AND a.output_tokens > 0
AND a.first_token_ms IS NOT NULL
AND a.duration_ms > a.first_token_ms
AND (
(? AND %s > 0 AND %s < ? AND %s >= ?)
OR %s >= ?
)
THEN 1
ELSE 0
END)`, generationExpression, generationExpression, tpsExpression, tpsExpression)

	ranked := db.Table("request_audits AS a").
		Select(
			`a.account_id, a.created_at, ROW_NUMBER() OVER (PARTITION BY a.account_id ORDER BY a.created_at DESC, a.id DESC) AS rn, `+failedExpr+` AS failed`,
			audit.ErrorQualityDegraded,
			failClosed,
			minGenMS,
			softTPS,
			hardTPS,
		).
		Where("a.account_id IS NOT NULL").
		Where("a.request_id NOT LIKE ?", audit.QualityProbeRequestPrefix+"%")
	return db.Table("(?) AS r", ranked).
		Select("r.account_id, r.rn, r.failed, r.created_at").
		Where("r.rn <= ?", accountCallQualitySampleLimit)
}
