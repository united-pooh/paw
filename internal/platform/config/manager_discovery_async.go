package config

import (
	"context"
	"fmt"
	"time"
)

func (m *Manager) startModelDiscovery(parent context.Context, document Document, activeID string) {
	targets := discoveryTargets(document)
	if len(targets) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.discoveryCancel = cancel
	m.discoveryDone = make(chan struct{})
	m.discoveryMu.Lock()
	go func() {
		defer close(m.discoveryDone)
		defer cancel()
		defer m.discoveryMu.Unlock()

		type result struct {
			providerID string
			models     []DiscoveredModel
			detail     string
		}
		startedAt := time.Now().UTC()
		results := make([]result, 0, len(targets))
		for _, providerID := range targets {
			if ctx.Err() != nil {
				return
			}
			models, detail := m.requestProviderModels(ctx, document, providerID)
			results = append(results, result{providerID, models, detail})
		}

		// Only the commit phase shares state with updates and the file watcher.
		m.updateMu.Lock()
		defer m.updateMu.Unlock()
		if ctx.Err() != nil {
			return
		}
		current := m.Snapshot()
		m.prepareModelDiscovery(document, activeID)
		m.discoveryStatus.AttemptedAt = startedAt
		activeProviderID := activeDiscoveryProvider(document, activeID)
		for _, result := range results {
			m.stageProviderDiscovery(document, result.providerID, activeProviderID, result.models, result.detail)
		}
		// Reread both files before accepting provenance or writing the cache.
		candidate, err := m.loadStartupCandidate(ctx, current.Revision+1)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			m.recordDiagnostic(fmt.Errorf("background model discovery could not reload configuration: %w", err))
			return
		}
		if !candidate.Ready && current.Ready {
			m.recordDiagnostic(fmt.Errorf("background model discovery has no usable active credential; keeping last-known-good snapshot"))
			return
		}
		if registered, changed := m.autoRegisterFromSnapshot(ctx, candidate); changed {
			candidate = registered
		}
		m.publish(m.appendAutoRegisterDiagnostic(candidate))
	}()
}

// WaitForModelDiscovery lets callers needing a usable model await cold startup.
func (m *Manager) WaitForModelDiscovery(ctx context.Context) error {
	if m.discoveryDone == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.discoveryDone:
		return ctx.Err()
	}
}
