package repository

import "time"

// AccountWeightSnapshot is the persisted scheduling weight for one account.
// Payload is an opaque JSON document owned by the selector.
type AccountWeightSnapshot struct {
	AccountID      uint64
	LastSelectedAt *time.Time
	Payload        string
}
