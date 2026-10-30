package relational

import (
	"context"
	"time"

	"github.com/chenyme/grok2api/backend/internal/repository"
	"gorm.io/gorm/clause"
)

type accountWeightModel struct {
	AccountID      uint64 `gorm:"primaryKey;autoIncrement:false"`
	LastSelectedAt *time.Time
	Payload        string        `gorm:"type:text;not null;default:''"`
	UpdatedAt      time.Time     `gorm:"not null"`
	Account        *accountModel `gorm:"foreignKey:AccountID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

func (accountWeightModel) TableName() string { return "account_weight_stats" }

func (r *AccountRepository) ListAccountWeights(ctx context.Context) ([]repository.AccountWeightSnapshot, error) {
	var rows []accountWeightModel
	if err := r.db.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]repository.AccountWeightSnapshot, 0, len(rows))
	for _, row := range rows {
		out = append(out, repository.AccountWeightSnapshot{AccountID: row.AccountID, LastSelectedAt: row.LastSelectedAt, Payload: row.Payload})
	}
	return out, nil
}

func (r *AccountRepository) SaveAccountWeight(ctx context.Context, snapshot repository.AccountWeightSnapshot) error {
	row := accountWeightModel{AccountID: snapshot.AccountID, LastSelectedAt: snapshot.LastSelectedAt, Payload: snapshot.Payload, UpdatedAt: time.Now().UTC()}
	return r.db.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error
}
