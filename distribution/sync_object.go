package distribution

import (
	"context"
	"fmt"
	"sort"
	"time"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

// SyncObjectRequest identifies an object whose latest physical stripes must be
// synchronized to stable storage.
type SyncObjectRequest struct {
	Context  context.Context
	ObjectID shared.ObjectID
}

// SyncObjectResponse reports how many physical chunks were synchronized.
type SyncObjectResponse struct{ SyncedChunks uint32 }

// SyncObject synchronizes every chunk in the latest known version of each stripe.
func (d *Distribution) SyncObject(request SyncObjectRequest) (response SyncObjectResponse, err error) {
	started := time.Now()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		logOperation(d.logger, "SyncObject", started, err,
			"object_id", request.ObjectID.String(),
			"synced_chunks", response.SyncedChunks)
	}()

	if err = ctx.Err(); err != nil {
		return response, err
	}
	stripes := d.objects[request.ObjectID]
	stripeIDs := make([]int, 0, len(stripes))
	for id := range stripes {
		stripeIDs = append(stripeIDs, int(id))
	}
	sort.Ints(stripeIDs)
	for _, rawID := range stripeIDs {
		if err = ctx.Err(); err != nil {
			return response, err
		}
		stripe := stripes[storage.StripeID(rawID)]
		chunkNos := make([]int, 0, len(stripe.chunks))
		for no := range stripe.chunks {
			chunkNos = append(chunkNos, int(no))
		}
		sort.Ints(chunkNos)
		for _, rawNo := range chunkNos {
			if err = ctx.Err(); err != nil {
				return response, err
			}
			location := stripe.chunks[chunkNo(rawNo)]
			if location.ioIndex < 0 || location.ioIndex >= len(d.ios) {
				return response, fmt.Errorf("stripe %d chunk %d has invalid I/O index %d", rawID, rawNo, location.ioIndex)
			}
			if _, err = d.ios[location.ioIndex].Sync(physicalio.SyncRequest{Context: ctx, Key: location.key}); err != nil {
				d.recordIOError(location.ioIndex, err)
				return response, fmt.Errorf("sync stripe %d chunk %d on I/O %d: %w", rawID, rawNo, location.ioIndex, err)
			}
			response.SyncedChunks++
		}
	}
	return response, nil
}
