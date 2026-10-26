package relational

import (
	"context"
	"time"
)

func (r *AccountRepository) LoadNodeAvoidance(ctx context.Context, now time.Time) (map[uint64]time.Time, error) {
	if err := r.db.db.WithContext(ctx).Where("avoid_until <= ?", now).Delete(&nodeQualityAvoidanceModel{}).Error; err != nil {
		return nil, err
	}
	var rows []nodeQualityAvoidanceModel
	if err := r.db.db.WithContext(ctx).Where("avoid_until > ?", now).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[uint64]time.Time, len(rows))
	for _, row := range rows {
		out[row.NodeID] = row.AvoidUntil
	}
	return out, nil
}

func (r *AccountRepository) SaveNodeAvoidance(ctx context.Context, nodeID uint64, until time.Time) error {
	if nodeID == 0 {
		return nil
	}
	row := nodeQualityAvoidanceModel{NodeID: nodeID, AvoidUntil: until.UTC()}
	return r.db.db.WithContext(ctx).Save(&row).Error
}
