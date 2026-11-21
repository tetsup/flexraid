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

type CleanupStripeDriveRequest struct {
	Context  context.Context
	IOIndex  int
	ObjectID shared.ObjectID
	StripeID storage.StripeID
	Method   CleanupMethod
}

type CleanupStripeDriveResponse struct {
	FilesProcessed int
	Key            storage.FileKey
}

// CleanupStripeDrive processes at most one physical file for the requested
// Drive × Stripe × Method. Old versions are deleted; abnormal chunks are moved
// to the drive-local preservation directory.
func (d *Distribution) CleanupStripeDrive(request CleanupStripeDriveRequest) (response CleanupStripeDriveResponse, err error) {
	started := time.Now()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		logOperation(d, "CleanupStripeDrive", started, err,
			"io_index", request.IOIndex, "object_id", request.ObjectID.String(),
			"stripe_id", request.StripeID, "method", request.Method,
			"files_processed", response.FilesProcessed)
	}()
	if request.IOIndex < 0 || request.IOIndex >= len(d.ios) {
		return response, fmt.Errorf("I/O index %d out of range", request.IOIndex)
	}
	if request.Method != CleanupMethodDeleteOldVersion && request.Method != CleanupMethodPreserveAbnormal {
		return response, fmt.Errorf("unsupported cleanup method %d", request.Method)
	}
	if err = ctx.Err(); err != nil {
		return response, err
	}

	candidate := cleanupCandidateKey{ioIndex: request.IOIndex, objectID: request.ObjectID, stripeID: request.StripeID, method: cleanupMethod(request.Method)}
	if !d.claimCleanupCandidate(candidate) {
		return response, nil
	}
	defer d.releaseCleanupCandidate(candidate)

	stripeKey := stripeIDKey{objectID: request.ObjectID, stripeID: request.StripeID}
	d.mapMu.Lock()
	location, found := d.findCleanupLocationLocked(candidate)
	if !found {
		d.refreshStripeCleanupQueueLocked(stripeKey)
		d.mapMu.Unlock()
		return response, nil
	}
	d.mapMu.Unlock()

	// The immutable FileKey is a value snapshot. Physical I/O runs without
	// Distribution locks; only this exact key is removed from inventory.
	switch request.Method {
	case CleanupMethodDeleteOldVersion:
		_, err = d.ios[request.IOIndex].Delete(physicalio.DeleteRequest{Context: ctx, Key: location.key})
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
	case CleanupMethodPreserveAbnormal:
		_, err = d.ios[request.IOIndex].Preserve(physicalio.PreserveRequest{Context: ctx, Key: location.key})
	}
	if err != nil {
		return response, err
	}

	d.mapMu.Lock()
	d.removeCleanupLocationLocked(candidate, location)
	d.refreshStripeCleanupQueueLocked(stripeKey)
	d.mapMu.Unlock()
	response.FilesProcessed = 1
	response.Key = location.key
	return response, nil
}

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
	switch candidate.method {
	case cleanupDeleteOldVersion:
		eligible = classified.OldVersions
	case cleanupPreserveAbnormal:
		eligible = classified.Abnormal
	default:
		return chunkLocation{}, false
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
