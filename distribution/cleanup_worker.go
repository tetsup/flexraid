package distribution

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// CleanupPermissions controls autonomous cleanup for one physical drive.
// The slice order is the same as NewRequest.IOs.
type CleanupPermissions struct {
	DeleteAllowed   bool
	PreserveAllowed bool
}

// SetCleanupPermissions replaces the drive permissions observed by the cleanup
// worker. The worker reads a snapshot before starting each physical file.
func (d *Distribution) SetCleanupPermissions(permissions []CleanupPermissions) error {
	if len(permissions) != len(d.ios) {
		return fmt.Errorf("got %d cleanup permission entries; want %d", len(permissions), len(d.ios))
	}
	d.cleanupControlMu.Lock()
	d.cleanupPermissions = append([]CleanupPermissions(nil), permissions...)
	d.cleanupControlMu.Unlock()
	d.wakeCleanupWorker()
	return nil
}

// StartCleanupWorker starts Distribution's autonomous cleanup loop. Calling it
// while the worker is running is harmless.
func (d *Distribution) StartCleanupWorker(parent context.Context) error {
	if parent == nil {
		return errors.New("cleanup worker context is required")
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
func (d *Distribution) StopCleanupWorker(ctx context.Context) error {
	if ctx == nil {
		return errors.New("cleanup worker stop context is required")
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
		return ctx.Err()
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
			d.cleanupControlMu.RLock()
			allowed := candidate.ioIndex >= 0 && candidate.ioIndex < len(d.cleanupPermissions)
			if allowed {
				permission := d.cleanupPermissions[candidate.ioIndex]
				allowed = (candidate.method == cleanupDeleteOldVersion && permission.DeleteAllowed) ||
					(candidate.method == cleanupPreserveAbnormal && permission.PreserveAllowed)
			}
			d.cleanupControlMu.RUnlock()
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
