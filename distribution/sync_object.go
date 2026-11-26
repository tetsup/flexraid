package distribution

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type SyncObjectRequest struct {
	Context  context.Context
	ObjectID shared.ObjectID
}

type SyncObjectResponse struct{ SyncedChunks uint32 }

// SyncObject synchronizes the cached latest version of every stripe.
func (d *Distribution) SyncObject(request SyncObjectRequest) (response SyncObjectResponse, err error) {
	started := time.Now()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		logOperation(d, "SyncObject", started, err, "object_id", request.ObjectID.String(), "synced_chunks", response.SyncedChunks)
	}()
	if err = ctx.Err(); err != nil {
		return response, err
	}

	d.mapMu.RLock()
	ids := make([]int, 0, len(d.objects[request.ObjectID]))
	for id := range d.objects[request.ObjectID] {
		ids = append(ids, int(id))
	}
	d.mapMu.RUnlock()
	sort.Ints(ids)

	for _, rawID := range ids {
		if err = ctx.Err(); err != nil {
			return response, err
		}
		key := stripeIDKey{objectID: request.ObjectID, stripeID: storage.StripeID(rawID)}
		info, unlockVersion, lockErr := d.lockSelectedStripeRead(key)
		if lockErr != nil {
			if errors.Is(lockErr, ErrStripeNotFound) {
				continue
			}
			return response, lockErr
		}
		locations := make([]chunkLocation, 0, len(info.chunks))
		for _, location := range info.chunks {
			locations = append(locations, location)
		}

		for _, location := range locations {
			if err = ctx.Err(); err != nil {
				unlockVersion()
				return response, err
			}
			if location.ioIndex < 0 || location.ioIndex >= len(d.ios) {
				unlockVersion()
				return response, fmt.Errorf("stripe %d has invalid I/O index %d", rawID, location.ioIndex)
			}
			_, syncErr := d.ios[location.ioIndex].Sync(physicalio.SyncRequest{Context: ctx, Key: location.key})
			d.recordChunkOperation("Sync", syncErr)
			if err = syncErr; err != nil {
				if d.logger != nil {
					d.logger.Error("stripe synchronization failed", "object_id", request.ObjectID.String(), "stripe_id", rawID, "io_index", location.ioIndex, "error", err)
				}
				unlockVersion()
				return response, fmt.Errorf("sync stripe %d on I/O %d: %w", rawID, location.ioIndex, err)
			}
			response.SyncedChunks++
		}
		unlockVersion()
	}
	return response, nil
}
