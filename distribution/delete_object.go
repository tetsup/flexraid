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

type DeleteObjectRequest struct {
	Context context.Context
	ObjectID shared.ObjectID
}
type DeleteObjectResponse struct{ DeletedFiles int }

// DeleteObject removes every physical stripe for a logically deleted object.
// Each stripe is an independent atomic unit; Stripe 0 is processed last.
func (d *Distribution) DeleteObject(request DeleteObjectRequest) (response DeleteObjectResponse, err error) {
	started := time.Now()
	ctx := request.Context
	if ctx == nil { ctx = context.Background() }
	defer func() {
		logOperation(d.logger, "DeleteObject", started, err, "object_id", request.ObjectID.String(), "deleted_files", response.DeletedFiles)
	}()
	if err = ctx.Err(); err != nil { return response, err }

	d.mapMu.Lock()
	seen := make(map[storage.StripeID]struct{})
	for id := range d.objects[request.ObjectID] { seen[id] = struct{}{} }
	for key := range d.versions { if key.objectID == request.ObjectID { seen[key.stripeID] = struct{}{} } }
	for _, location := range d.orphans[request.ObjectID] { seen[location.key.StripeID] = struct{}{} }
	d.mapMu.Unlock()

	stripeIDs := make([]int, 0, len(seen))
	for id := range seen { if id != 0 { stripeIDs = append(stripeIDs, int(id)) } }
	sort.Ints(stripeIDs)
	if _, hasHeader := seen[0]; hasHeader { stripeIDs = append(stripeIDs, 0) }
	for _, rawID := range stripeIDs {
		if err = ctx.Err(); err != nil { return response, err }
		key := stripeIDKey{objectID:request.ObjectID, stripeID:storage.StripeID(rawID)}
		unlock := d.lockStripe(key)
		if err = d.cacheStripeLocked(ctx, key); err != nil {
			unlock()
			return response, fmt.Errorf("cache stripe %d before deletion: %w", rawID, err)
		}
		d.mapMu.Lock()
		locations := make([]chunkLocation, 0)
		seenKeys := make(map[storage.FileKey]struct{})
		for _, version := range d.versions[key] {
			for _, location := range version.chunks {
				if _, ok := seenKeys[location.key]; !ok { seenKeys[location.key]=struct{}{}; locations=append(locations,location) }
			}
		}
		for _, location := range d.orphans[request.ObjectID] {
			if location.key.StripeID == key.stripeID {
				if _, ok := seenKeys[location.key]; !ok { seenKeys[location.key]=struct{}{}; locations=append(locations,location) }
			}
		}
		d.mapMu.Unlock()

		for _, location := range locations {
			if location.ioIndex < 0 || location.ioIndex >= len(d.ios) {
				unlock()
				return response, fmt.Errorf("invalid I/O index %d for stripe %d", location.ioIndex, rawID)
			}
			_, deleteErr := d.ios[location.ioIndex].Delete(physicalio.DeleteRequest{Context:ctx, Key:location.key})
			if deleteErr != nil && !errors.Is(deleteErr, os.ErrNotExist) {
				unlock()
				return response, deleteErr
			}
			if deleteErr == nil { response.DeletedFiles++ }
		}
		d.mapMu.Lock()
		delete(d.versions,key)
		if stripes := d.objects[request.ObjectID]; stripes != nil {
			delete(stripes,key.stripeID)
			if len(stripes)==0 { delete(d.objects,request.ObjectID) }
		}
		remaining := d.orphans[request.ObjectID][:0]
		for _, location := range d.orphans[request.ObjectID] {
			if location.key.StripeID != key.stripeID { remaining=append(remaining,location) }
		}
		if len(remaining)==0 { delete(d.orphans,request.ObjectID) } else { d.orphans[request.ObjectID]=remaining }
		d.mapMu.Unlock()
		unlock()
	}
	return response,nil
}
