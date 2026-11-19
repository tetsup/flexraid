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

var ErrLogicalObjectDeletionNotAllowed = errors.New("logical Object deletion is not allowed on every drive containing the object")

type DeleteObjectRequest struct {
	Context  context.Context
	ObjectID shared.ObjectID
}

type DeleteObjectResponse struct {
	DeletedFiles int
}

type objectDeleteFileKey struct {
	ioIndex int
	key     storage.FileKey
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
	locations := make(map[objectDeleteFileKey]objectDeleteLocation)
	currentKeys := make(map[objectDeleteFileKey]struct{})
	for _, stripe := range d.objects[request.ObjectID] {
		for _, location := range stripe.chunks {
			currentKeys[objectDeleteFileKey{ioIndex: location.ioIndex, key: location.key}] = struct{}{}
		}
	}
	for key, versions := range d.versions {
		if key.objectID != request.ObjectID {
			continue
		}
		for _, version := range versions {
			for _, location := range version.chunks {
				identity := objectDeleteFileKey{ioIndex: location.ioIndex, key: location.key}
				_, current := currentKeys[identity]
				locations[identity] = objectDeleteLocation{location: location, current: current}
			}
		}
	}
	for _, location := range d.orphans[request.ObjectID] {
		identity := objectDeleteFileKey{ioIndex: location.ioIndex, key: location.key}
		if _, exists := locations[identity]; !exists {
			locations[identity] = objectDeleteLocation{location: location}
		}
	}
	for _, stripe := range d.objects[request.ObjectID] {
		for _, location := range stripe.chunks {
			identity := objectDeleteFileKey{ioIndex: location.ioIndex, key: location.key}
			if _, exists := locations[identity]; !exists {
				locations[identity] = objectDeleteLocation{location: location, current: true}
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
		if !permissions.Drives[item.location.ioIndex].DeleteLogicalObjectsAllowed {
			return response, fmt.Errorf("%w: I/O index %d", ErrLogicalObjectDeletionNotAllowed, item.location.ioIndex)
		}
		work = append(work, item)
	}

	// Snapshot permissions are intentionally held for this ObjectID work unit.
	// Changes published by Management apply when the next ObjectID is selected.
	sort.Slice(work, func(i, j int) bool {
		a, b := work[i], work[j]
		ka, kb := a.location.key, b.location.key
		phase := func(item objectDeleteLocation) int {
			if item.location.key.StripeID == 0 {
				return 2
			}
			if !item.current {
				return 0
			}
			return 1
		}
		phaseA, phaseB := phase(a), phase(b)
		if phaseA != phaseB {
			return phaseA < phaseB
		}
		if phaseA == 0 {
			if ka.StripeID != kb.StripeID {
				return ka.StripeID > kb.StripeID
			}
			if ka.StripeVersion != kb.StripeVersion {
				return ka.StripeVersion < kb.StripeVersion
			}
		} else if phaseA == 1 && ka.StripeID != kb.StripeID {
			return ka.StripeID > kb.StripeID
		} else if phaseA == 2 && a.current != b.current {
			return !a.current
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
		d.removeObjectFileLocked(request.ObjectID, location)
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

func (d *Distribution) removeObjectFileLocked(objectID shared.ObjectID, removed chunkLocation) {
	for key, versions := range d.versions {
		if key.objectID != objectID {
			continue
		}
		for versionNo, version := range versions {
			for chunk, location := range version.chunks {
				if location.ioIndex == removed.ioIndex && location.key == removed.key {
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
			if location.ioIndex == removed.ioIndex && location.key == removed.key {
				delete(stripe.chunks, chunk)
			}
		}
		if len(stripe.chunks) == 0 {
			delete(d.objects[objectID], stripeID)
		}
	}
	remaining := d.orphans[objectID][:0]
	for _, location := range d.orphans[objectID] {
		if location.ioIndex != removed.ioIndex || location.key != removed.key {
			remaining = append(remaining, location)
		}
	}
	if len(remaining) == 0 {
		delete(d.orphans, objectID)
	} else {
		d.orphans[objectID] = remaining
	}
}
