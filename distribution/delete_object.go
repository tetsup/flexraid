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

var ErrCleanupDriveNotAllowed = errors.New("cleanup is not allowed on every drive containing the object")

type DeleteObjectRequest struct {
	Context  context.Context
	ObjectID shared.ObjectID
}

type DeleteObjectResponse struct {
	DeletedFiles int
}

type objectDeleteLocation struct {
	location chunkLocation
	current  bool
}

// DeleteObject removes one logically deleted Object as a single work unit.
// It validates all required drives before starting, then deletes invalid data
// before valid chunks. Valid chunks are deleted from the highest StripeID and
// chunk index down to Stripe 0.
func (d *Distribution) DeleteObject(request DeleteObjectRequest) (response DeleteObjectResponse, err error) {
	started := time.Now()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		logOperation(d.logger, "DeleteObject", started, err,
			"object_id", request.ObjectID.String(), "deleted_files", response.DeletedFiles)
	}()
	if err = ctx.Err(); err != nil {
		return response, err
	}

	permissions := d.cleanupPermissionStore.Snapshot()
	d.mapMu.Lock()
	locations := make(map[storage.FileKey]objectDeleteLocation)
	currentKeys := make(map[storage.FileKey]struct{})
	for _, stripes := range d.objects {
		_ = stripes
	}
	for _, stripe := range d.objects[request.ObjectID] {
		for _, location := range stripe.chunks {
			currentKeys[location.key] = struct{}{}
		}
	}
	for key, versions := range d.versions {
		if key.objectID != request.ObjectID {
			continue
		}
		for _, version := range versions {
			for _, location := range version.chunks {
				_, current := currentKeys[location.key]
				locations[location.key] = objectDeleteLocation{location: location, current: current}
			}
		}
	}
	for _, location := range d.orphans[request.ObjectID] {
		if _, exists := locations[location.key]; !exists {
			locations[location.key] = objectDeleteLocation{location: location}
		}
	}
	for _, stripe := range d.objects[request.ObjectID] {
		for _, location := range stripe.chunks {
			if _, exists := locations[location.key]; !exists {
				locations[location.key] = objectDeleteLocation{location: location, current: true}
			}
		}
	}
	d.mapMu.Unlock()

	work := make([]objectDeleteLocation, 0, len(locations))
	for _, item := range locations {
		if item.location.ioIndex < 0 || item.location.ioIndex >= len(permissions.Drives) ||
			item.location.ioIndex >= len(d.ios) {
			return response, fmt.Errorf("invalid I/O index %d for Object %s", item.location.ioIndex, request.ObjectID.String())
		}
		if !permissions.Drives[item.location.ioIndex].DeleteAllowed {
			return response, fmt.Errorf("%w: I/O index %d", ErrCleanupDriveNotAllowed, item.location.ioIndex)
		}
		work = append(work, item)
	}

	// Snapshot permissions are intentionally held for this ObjectID work unit.
	// Changes published by Management apply when the next ObjectID is selected.
	sort.Slice(work, func(i, j int) bool {
		a, b := work[i], work[j]
		if a.current != b.current {
			return !a.current
		}
		ka, kb := a.location.key, b.location.key
		if !a.current {
			if ka.StripeID != kb.StripeID {
				return ka.StripeID > kb.StripeID
			}
			if ka.StripeVersion != kb.StripeVersion {
				return ka.StripeVersion < kb.StripeVersion
			}
		} else if ka.StripeID != kb.StripeID {
			return ka.StripeID > kb.StripeID
		}
		if ka.LayoutID.Index != kb.LayoutID.Index {
			return ka.LayoutID.Index > kb.LayoutID.Index
		}
		if ka.LayoutID.Role != kb.LayoutID.Role {
			return ka.LayoutID.Role > kb.LayoutID.Role
		}
		return a.location.ioIndex < b.location.ioIndex
	})

	for _, item := range work {
		if err = ctx.Err(); err != nil {
			return response, err
		}
		location := item.location
		_, deleteErr := d.ios[location.ioIndex].Delete(physicalio.DeleteRequest{Context: ctx, Key: location.key})
		if deleteErr != nil && !errors.Is(deleteErr, os.ErrNotExist) {
			return response, fmt.Errorf("delete Object %s physical file on I/O %d: %w",
				request.ObjectID.String(), location.ioIndex, deleteErr)
		}
		if deleteErr == nil {
			response.DeletedFiles++
		}
		d.mapMu.Lock()
		d.removeObjectFileLocked(request.ObjectID, location.key)
		d.mapMu.Unlock()
	}

	d.mapMu.Lock()
	delete(d.objects, request.ObjectID)
	for key := range d.versions {
		if key.objectID == request.ObjectID {
			delete(d.versions, key)
		}
	}
	delete(d.orphans, request.ObjectID)
	d.cleanupQueue.mu.Lock()
	for candidate := range d.cleanupQueue.pending {
		if candidate.objectID == request.ObjectID {
			delete(d.cleanupQueue.pending, candidate)
		}
	}
	for candidate := range d.cleanupQueue.inFlight {
		if candidate.objectID == request.ObjectID {
			delete(d.cleanupQueue.inFlight, candidate)
		}
	}
	d.cleanupQueue.mu.Unlock()
	d.mapMu.Unlock()
	return response, nil
}

func (d *Distribution) removeObjectFileLocked(objectID shared.ObjectID, fileKey storage.FileKey) {
	for key, versions := range d.versions {
		if key.objectID != objectID {
			continue
		}
		for versionNo, version := range versions {
			for chunk, location := range version.chunks {
				if location.key == fileKey {
					delete(version.chunks, chunk)
				}
			}
			if len(version.chunks) == 0 {
				delete(versions, versionNo)
			}
		}
		if len(versions) == 0 {
			delete(d.versions, key)
		}
	}
	for stripeID, stripe := range d.objects[objectID] {
		for chunk, location := range stripe.chunks {
			if location.key == fileKey {
				delete(stripe.chunks, chunk)
			}
		}
		if len(stripe.chunks) == 0 {
			delete(d.objects[objectID], stripeID)
		}
	}
	remaining := d.orphans[objectID][:0]
	for _, location := range d.orphans[objectID] {
		if location.key != fileKey {
			remaining = append(remaining, location)
		}
	}
	if len(remaining) == 0 {
		delete(d.orphans, objectID)
	} else {
		d.orphans[objectID] = remaining
	}
}
