package orchestration

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// StartCleanupWorker starts Orchestration's logical-deletion cleanup loop.
// The callback must enter through Management so its lifecycle and operation
// serialization remain authoritative.
func (layer *Orchestration) StartCleanupWorker(parent context.Context, cleanup func(context.Context) error) error {
	if parent == nil {
		return fmt.Errorf("cleanup worker context is required")
	}
	if cleanup == nil {
		return fmt.Errorf("cleanup worker callback is required")
	}
	layer.cleanupWorkerMu.Lock()
	defer layer.cleanupWorkerMu.Unlock()
	if layer.cleanupWorkerCancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	layer.cleanupWorkerCancel = cancel
	layer.cleanupWorkerDone = done
	go layer.runCleanupWorker(ctx, done, cleanup)
	return nil
}

// StopCleanupWorker stops the logical-deletion cleanup loop and waits for its
// current Management cleanup request to return.
func (layer *Orchestration) StopCleanupWorker(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("cleanup worker stop context is required")
	}
	layer.cleanupWorkerMu.Lock()
	cancel, done := layer.cleanupWorkerCancel, layer.cleanupWorkerDone
	if cancel == nil {
		layer.cleanupWorkerMu.Unlock()
		return nil
	}
	cancel()
	layer.cleanupWorkerMu.Unlock()
	select {
	case <-done:
		layer.cleanupWorkerMu.Lock()
		if layer.cleanupWorkerDone == done {
			layer.cleanupWorkerCancel = nil
			layer.cleanupWorkerDone = nil
		}
		layer.cleanupWorkerMu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (layer *Orchestration) runCleanupWorker(ctx context.Context, done chan struct{}, cleanup func(context.Context) error) {
	defer func() {
		close(done)
		layer.cleanupWorkerMu.Lock()
		if layer.cleanupWorkerDone == done {
			layer.cleanupWorkerCancel = nil
			layer.cleanupWorkerDone = nil
		}
		layer.cleanupWorkerMu.Unlock()
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := cleanup(ctx); err != nil && !errors.Is(err, context.Canceled) {
			layer.logger.Warn("logical object cleanup failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
