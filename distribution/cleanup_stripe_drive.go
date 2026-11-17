package distribution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

// CleanupStripeDriveRequest selects one queued cleanup candidate for one drive.
type CleanupStripeDriveRequest struct {
	Context  context.Context
	IOIndex  int
	ObjectID shared.ObjectID
	StripeID storage.StripeID
	Reason   CleanupReason
}

// CleanupStripeDriveResponse reports the number of physical files handled.
type CleanupStripeDriveResponse struct {
	FilesProcessed int
}

// CleanupStripeDrive processes one Drive × Stripe × Reason candidate.
// Old versions are deleted; abnormal versions are moved to the drive-local
// preservation directory. Physical I/O runs without map or Stripe locks.
func (d *Distribution) CleanupStripeDrive(request CleanupStripeDriveRequest) (response CleanupStripeDriveResponse, err error) {
	started := time.Now()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		logOperation(d.logger, "CleanupStripeDrive", started, err,
			"io_index", request.IOIndex, "object_id", request.ObjectID.String(),
			"stripe_id", request.StripeID, "reason", request.Reason,
			"files_processed", response.FilesProcessed)
	}()

	if request.IOIndex < 0 || request.IOIndex >= len(d.ios) {
		return response, fmt.Errorf("I/O index %d out of range", request.IOIndex)
	}
	if request.Reason != CleanupOldVersion && request.Reason != CleanupAbnormalVersion {
		return response, fmt.Errorf("unsupported cleanup reason %d", request.Reason)
	}
	if err = ctx.Err(); err != nil {
		return response, err
	}

	candidate := cleanupCandidateKey{
		ioIndex: request.IOIndex, objectID: request.ObjectID,
		stripeID: request.StripeID, reason: request.Reason,
	}
	if !d.claimCleanupCandidate(candidate) {
		return response, fmt.Errorf("cleanup candidate is not pending or is already in flight")
	}
	defer d.releaseCleanupCandidate(candidate)

	key := stripeIDKey{objectID: request.ObjectID, stripeID: request.StripeID}
	unlockStripe, unlockMap := d.lockStripe(key)
	if err = d.cacheStripeLocked(ctx, key); err != nil {
		unlockMap()
		unlockStripe()
		return response, fmt.Errorf("validate current stripe before cleanup: %w", err)
	}
	current := d.objects[key.objectID][key.stripeID]
	if current == nil {
		unlockMap()
		unlockStripe()
		return response, fmt.Errorf("stripe has no selected recoverable version; cleanup deferred")
	}

	targets := make(map[storage.FileKey]chunkLocation)
	for versionNo, version := range d.versions[key] {
		if !matchesCleanupReason(versionNo, current.version, request.Reason) {
			continue
		}
		for _, location := range version.chunks {
			if location.ioIndex == request.IOIndex {
				targets[location.key] = location
			}
		}
	}
	for _, location := range d.orphans[key.objectID] {
		if location.ioIndex != request.IOIndex || location.key.StripeID != request.StripeID {
			continue
		}
		if matchesCleanupReason(location.key.StripeVersion, current.version, request.Reason) {
			targets[location.key] = location
		}
	}
	unlockMap()
	unlockStripe()

	processed := make(map[storage.FileKey]struct{}, len(targets))
	for fileKey, location := range targets {
		if err = ctx.Err(); err != nil {
			break
		}
		switch request.Reason {
		case CleanupOldVersion:
			_, err = d.ios[location.ioIndex].Delete(physicalio.DeleteRequest{Context: ctx, Key: fileKey})
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		case CleanupAbnormalVersion:
			_, err = d.ios[location.ioIndex].Preserve(physicalio.PreserveRequest{Context: ctx, Key: fileKey})
		}
		if err != nil {
			break
		}
		processed[fileKey] = struct{}{}
		response.FilesProcessed++
	}

	// Remove only the exact physical keys successfully handled. A concurrent
	// foreground write may have published a newer Version while I/O was in
	// progress; its newly published inventory is never removed here.
	unlockStripe, unlockMap = d.lockStripe(key)
	for versionNo, version := range d.versions[key] {
		for chunkNo, location := range version.chunks {
			if _, ok := processed[location.key]; ok && location.ioIndex == request.IOIndex {
				delete(version.chunks, chunkNo)
			}
		}
		if len(version.chunks) == 0 {
			delete(d.versions[key], versionNo)
		}
	}
	if len(d.versions[key]) == 0 {
		delete(d.versions, key)
	}
	remainingOrphans := d.orphans[key.objectID][:0]
	for _, location := range d.orphans[key.objectID] {
		if location.key.StripeID == request.StripeID && location.ioIndex == request.IOIndex {
			if _, ok := processed[location.key]; ok {
				continue
			}
		}
		remainingOrphans = append(remainingOrphans, location)
	}
	if len(remainingOrphans) == 0 {
		delete(d.orphans, key.objectID)
	} else {
		d.orphans[key.objectID] = remainingOrphans
	}
	d.refreshStripeCleanupQueueLocked(key)
	unlockMap()
	unlockStripe()

	if err != nil {
		return response, err
	}
	return response, nil
}

func matchesCleanupReason(version, current storage.StripeVersion, reason CleanupReason) bool {
	switch reason {
	case CleanupOldVersion:
		return version < current
	case CleanupAbnormalVersion:
		return version > current
	default:
		return false
	}
}
