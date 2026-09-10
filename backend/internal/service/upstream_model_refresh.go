package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

const (
	upstreamModelRefreshInterval   = 6 * time.Hour
	upstreamModelRefreshRunTimeout = 10 * time.Minute
	upstreamModelRefreshWorkers    = 3
)

// UpstreamModelRefreshService periodically learns authoritative positive model
// capabilities. Unsupported /models endpoints fail open and leave request-time
// negative capability learning as the fallback.
type UpstreamModelRefreshService struct {
	accountRepo AccountRepository
	syncer      *AccountTestService

	startOnce sync.Once
	stopOnce  sync.Once
	cancel    context.CancelFunc
	done      chan struct{}
}

func NewUpstreamModelRefreshService(accountRepo AccountRepository, syncer *AccountTestService) *UpstreamModelRefreshService {
	return &UpstreamModelRefreshService{
		accountRepo: accountRepo,
		syncer:      syncer,
		done:        make(chan struct{}),
	}
}

func (s *UpstreamModelRefreshService) Start() {
	if s == nil || s.accountRepo == nil || s.syncer == nil {
		return
	}
	s.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		go s.run(ctx)
	})
}

func (s *UpstreamModelRefreshService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.cancel == nil {
			return
		}
		s.cancel()
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
		}
	})
}

func (s *UpstreamModelRefreshService) run(ctx context.Context) {
	defer close(s.done)
	s.refresh(ctx)
	ticker := time.NewTicker(upstreamModelRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refresh(ctx)
		}
	}
}

func (s *UpstreamModelRefreshService) refresh(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, upstreamModelRefreshRunTimeout)
	defer cancel()
	accounts, err := s.accountRepo.ListActive(ctx)
	if err != nil {
		slog.Warn("upstream_model_refresh_list_failed", "error", err)
		return
	}

	sem := make(chan struct{}, upstreamModelRefreshWorkers)
	var wg sync.WaitGroup
	for i := range accounts {
		account := &accounts[i]
		if !account.IsSchedulable() || upstreamSupportedModelsSnapshotFresh(account.GetUpstreamSupportedModelsSnapshot(), time.Now()) {
			continue
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(account *Account) {
			defer wg.Done()
			defer func() { <-sem }()
			_, syncErr := s.syncer.SyncUpstreamModelCatalog(ctx, account)
			if syncErr == nil {
				return
			}
			var classified *UpstreamModelSyncError
			if errors.As(syncErr, &classified) && (classified.Kind == UpstreamModelSyncErrorUnsupported || classified.Kind == UpstreamModelSyncErrorConfiguration) {
				slog.Debug("upstream_model_refresh_skipped", "account_id", account.ID, "kind", classified.Kind)
				return
			}
			slog.Warn("upstream_model_refresh_failed", "account_id", account.ID, "error", syncErr)
		}(account)
	}
	wg.Wait()
}
