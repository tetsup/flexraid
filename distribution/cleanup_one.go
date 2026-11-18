package distribution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

// CleanupOneRequest selects one drive and one cleanup method. A successful
// call processes no more than one physical chunk file.
type CleanupOneRequest struct {
	Context context.Context
	IOIndex int
	Method  CleanupMethod
}

// CleanupOneResponse identifies the physical file processed by this call.
// Processed is false when no currently eligible file matched the request.
type CleanupOneResponse struct {
	Processed bool
	ObjectID  shared.ObjectID
	StripeID  storage.StripeID
	Key       storage.FileKey
}

// CleanupOne processes at most one currently eligible physical file on the
// requested drive. Old versions are deleted; abnormal data is moved to the
// drive-local preservation area. Queue entries are hints and are revalidated
// under the Stripe lock immediately before the physical operation.
func (d *Distribution) CleanupOne(request CleanupOneRequest) (response CleanupOneResponse, err error) {
	started := time.Now()
	defer func() {
		logOperation(d.logger, "CleanupOne", started, err,
			"io_index", request.IOIndex, "method", request.Method, "processed", response.Processed)
	}()

	if request.Context == nil {
		return response, fmt.Errorf("cleanup context is required")
	}
	if err = request.Context.Err(); err != nil {
		return response, err
	}
	if request.IOIndex < 0 || request.IOIndex >= len(d.ios) {
		return response, fmt.Errorf("I/O index %d is out of range", request.IOIndex)
	}
	if request.Method != CleanupMethodDeleteOldVersion && request.Method != CleanupMethodPreserveAbnormal {
		return response, fmt.Errorf("unsupported cleanup method %d", request.Method)
	}

	for _, candidate := range d.cleanupCandidates() {
		if candidate.ioIndex != request.IOIndex || candidate.method != cleanupMethod(request.Method) {
			continue
		}
		if err = request.Context.Err(); err != nil {
			return response, err
		}
		if !d.claimCleanupCandidate(candidate) {
			continue
		}
		processed, fileKey, processErr := d.cleanupCandidate(request.Context, candidate)
		d.releaseCleanupCandidate(candidate)
		if processErr != nil {
			return response, processErr
		}
		if processed {
			return CleanupOneResponse{
				Processed: true,
				ObjectID:  candidate.objectID,
				StripeID:  candidate.stripeID,
				Key:       fileKey,
			}, nil
		}
	}
	return response, nil
}

// cleanupCandidate locks one Stripe, reclassifies it, and performs one
// physical operation. It returns false for stale hints.
func (d *Distribution) cleanupCandidate(ctx context.Context, candidate cleanupCandidateKey) (bool, storage.FileKey, error) {
	key := stripeIDKey{objectID: candidate.objectID, stripeID: candidate.stripeID}
	unlockStripe, unlockMap := d.lockStripe(key)

	location, found := d.findCleanupLocationLocked(candidate)
	if !found {
		d.refreshStripeCleanupQueueLocked(key)
		unlockMap()
		unlockStripe()
		return false, storage.FileKey{}, nil
	}
	unlockMap()

	var operationErr error
	switch candidate.method {
	case cleanupDeleteOldVersion:
		_, operationErr = d.ios[candidate.ioIndex].Delete(physicalio.DeleteRequest{Context: ctx, Key: location.key})
		if errors.Is(operationErr, os.ErrNotExist) {
			operationErr = nil
		}
	case cleanupPreserveAbnormal:
		_, operationErr = d.ios[candidate.ioIndex].Preserve(physicalio.PreserveRequest{Context: ctx, Key: location.key})
	}
	if operationErr != nil {
		unlockStripe()
		return false, storage.FileKey{}, operationErr
	}

	d.mapMu.Lock()
	d.removeCleanupLocationLocked(candidate, location)
	d.refreshStripeCleanupQueueLocked(key)
	d.mapMu.Unlock()
	unlockStripe()
	return true, location.key, nil
}

// findCleanupLocationLocked requires mapMu and returns one matching physical
// file from the current classification, never from a stale queue snapshot.
func (d *Distribution) findCleanupLocationLocked(candidate cleanupCandidateKey) (chunkLocation, bool) {
	key := stripeIDKey{objectID: candidate.objectID, stripeID: candidate.stripeID}
	var orphans []chunkLocation
	for _, location := range d.orphans[candidate.objectID] {
		if location.key.StripeID == candidate.stripeID {
			orphans = append(orphans, location)
		}
	}
	classified, ok := classifyStripe(d.versions[key], orphans, d.objects[candidate.objectID][candidate.stripeID])
	if !ok {
		return chunkLocation{}, false
	}

	var eligible []chunkLocation
	if candidate.method == cleanupDeleteOldVersion {
		eligible = classified.OldVersions
	} else {
		eligible = classified.Abnormal
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].key.StripeVersion != eligible[j].key.StripeVersion {
			return eligible[i].key.StripeVersion < eligible[j].key.StripeVersion
		}
		if eligible[i].key.LayoutID.Type != eligible[j].key.LayoutID.Type {
			return eligible[i].key.LayoutID.Type < eligible[j].key.LayoutID.Type
		}
		if eligible[i].key.LayoutID.Index != eligible[j].key.LayoutID.Index {
			return eligible[i].key.LayoutID.Index < eligible[j].key.LayoutID.Index
		}
		return eligible[i].ioIndex < eligible[j].ioIndex
	})
	for _, location := range eligible {
		if location.ioIndex == candidate.ioIndex {
			return location, true
		}
	}
	return chunkLocation{}, false
}

func (d *Distribution) removeCleanupLocationLocked(candidate cleanupCandidateKey, location chunkLocation) {
	key := stripeIDKey{objectID: candidate.objectID, stripeID: candidate.stripeID}
	versions := d.versions[key]
	for versionNo, version := range versions {
		for no, existing := range version.chunks {
			if existing.ioIndex == location.ioIndex && existing.key == location.key {
				delete(version.chunks, no)
			}
		}
		if len(version.chunks) == 0 {
			delete(versions, versionNo)
		}
	}
	if len(versions) == 0 {
		delete(d.versions, key)
	}
	remaining := d.orphans[candidate.objectID][:0]
	for _, existing := range d.orphans[candidate.objectID] {
		if existing.ioIndex != location.ioIndex || existing.key != location.key {
			remaining = append(remaining, existing)
		}
	}
	if len(remaining) == 0 {
		delete(d.orphans, candidate.objectID)
	} else {
		d.orphans[candidate.objectID] = remaining
	}
}
