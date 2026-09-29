package service

import (
	"context"
	"time"
)

const accountModelHealthPersistTimeout = 3 * time.Second

// ModelHealthObservation records a real successful request or scheduled
// connectivity test for one account and public model ID.
type ModelHealthObservation struct {
	AccountID int64
	Model     string
	CheckedAt time.Time
}

// ModelHealthObservationReader supplies durable model-level health evidence.
// The usage repository implements this without expanding UsageLogRepository's
// broad interface and its test doubles.
type ModelHealthObservationReader interface {
	ListModelHealthObservations(ctx context.Context, groupID *int64, platform string) ([]ModelHealthObservation, error)
}

// AccountModelHealthRecorder records successful model probes without widening
// AccountRepository and every repository test double.
type AccountModelHealthRecorder interface {
	RecordAccountModelHealthSuccess(ctx context.Context, accountID int64, model string, checkedAt time.Time) error
}

// AccountModelHealthFailureRecorder persists failed probes so periodic checks
// can advance to other models instead of retrying one bad candidate forever.
type AccountModelHealthFailureRecorder interface {
	RecordAccountModelHealthFailure(ctx context.Context, accountID int64, model string, checkedAt time.Time) error
}

type AccountModelHealthState struct {
	AccountID     int64
	Model         string
	LastSuccessAt *time.Time
	LastFailureAt *time.Time
	LastProbeAt   *time.Time
}

// AccountModelHealthStateReader supplies the latest probe outcome per pair to
// the bounded background health checker.
type AccountModelHealthStateReader interface {
	ListAccountModelHealthStates(ctx context.Context) ([]AccountModelHealthState, error)
}

// 原子登记付费探测尝试；未取得占用时禁止发送请求，尝试本身不代表健康。
type AccountModelProbeClaimer interface {
	ClaimAccountModelProbe(ctx context.Context, accountID int64, model string, interval time.Duration) (bool, error)
}
