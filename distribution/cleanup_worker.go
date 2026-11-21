package distribution

import (
	"context"
	"errors"
	"time"
)

// NotifyCleanupPermissionsChanged wakes the worker after Management updates
// the shared permission store.
func (d *Distribution) NotifyCleanupPermissionsChanged() {
	d.wakeCleanupWorker()
}

// StartCleanupWorker starts Distribution's autonomous cleanup loop. Calling it
// while the worker is running is harmless.
func (d *Distribution) StartCleanupWorker(parent context.Context) (resultErr error) {
	started := time.Now()
	defer func() { logOperation(d, "StartCleanupWorker", started, resultErr) }()
	if parent == nil {
		resultErr = errors.New("cleanup worker context is required")
		return resultErr
	}
	d.cleanupWorkerMu.Lock()
	defer d.cleanupWorkerMu.Unlock()
	if d.cleanupWorkerCancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	d.cleanupWorkerCancel = cancel
	d.cleanupWorkerDone = done
	go d.runCleanupWorker(ctx, done)
	return nil
}

// StopCleanupWorker requests a graceful stop and waits for the current physical
// file operation to finish. It is safe to call when the worker is not running.
func (d *Distribution) StopCleanupWorker(ctx context.Context) (resultErr error) {
	started := time.Now()
	defer func() { logOperation(d, "StopCleanupWorker", started, resultErr) }()
	if ctx == nil {
		resultErr = errors.New("cleanup worker stop context is required")
		return resultErr
	}
	d.cleanupWorkerMu.Lock()
	cancel, done := d.cleanupWorkerCancel, d.cleanupWorkerDone
	if cancel == nil {
		d.cleanupWorkerMu.Unlock()
		return nil
	}
	cancel()
	d.cleanupWorkerMu.Unlock()
	select {
	case <-done:
		d.cleanupWorkerMu.Lock()
		if d.cleanupWorkerDone == done {
			d.cleanupWorkerCancel = nil
			d.cleanupWorkerDone = nil
		}
		d.cleanupWorkerMu.Unlock()
		return nil
	case <-ctx.Done():
		resultErr = ctx.Err()
		return resultErr
	}
}

func (d *Distribution) wakeCleanupWorker() {
	select {
	case d.cleanupWake <- struct{}{}:
	default:
	}
}

func (d *Distribution) runCleanupWorker(ctx context.Context, done chan struct{}) {
	defer func() {
		close(done)
		d.cleanupWorkerMu.Lock()
		if d.cleanupWorkerDone == done {
			d.cleanupWorkerCancel = nil
			d.cleanupWorkerDone = nil
		}
		d.cleanupWorkerMu.Unlock()
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		processed := false
		for _, candidate := range d.cleanupCandidates() {
			if err := ctx.Err(); err != nil {
				return
			}
			permissions := d.cleanupPermissionStore.Snapshot().Drives
			allowed := candidate.ioIndex >= 0 && candidate.ioIndex < len(permissions)
			if allowed {
				permission := permissions[candidate.ioIndex]
				allowed = (candidate.method == cleanupDeleteOldVersion && permission.DeleteDeprecatedVersionsAllowed) ||
					(candidate.method == cleanupPreserveAbnormal && permission.PreserveAbnormalDataAllowed)
			}
			if !allowed {
				continue
			}
			result, err := d.CleanupStripeDrive(CleanupStripeDriveRequest{
				Context: ctx, IOIndex: candidate.ioIndex, ObjectID: candidate.objectID,
				StripeID: candidate.stripeID, Method: CleanupMethod(candidate.method),
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				d.logger.Warn("background cleanup failed", "io_index", candidate.ioIndex,
					"object_id", candidate.objectID.String(), "stripe_id", candidate.stripeID,
					"method", candidate.method, "error", err)
			}
			processed = processed || result.FilesProcessed > 0
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-d.cleanupWake:
		case <-ticker.C:
		}
	}
}
