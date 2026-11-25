package distribution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type WriteStripesRequest struct {
	Context context.Context
	ObjectID shared.ObjectID
	Stripes []WriteStripeRequest
}

type WriteStripesResponse struct { Version storage.StripeVersion }

// WriteStripes commits the supplied stripes as one Object-level Version.
// Data stripes are persisted first and Stripe 0 is persisted last.
func (d *Distribution) WriteStripes(request WriteStripesRequest) (response WriteStripesResponse, err error) {
	started := time.Now()
	defer func() {
		logOperation(d, "WriteStripes", started, err, "object_id", request.ObjectID.String(), "stripes", len(request.Stripes))
	}()
	ctx := request.Context
	if ctx == nil { ctx = context.Background() }
	if err = ctx.Err(); err != nil { return response, err }
	if len(request.Stripes) == 0 { return response, fmt.Errorf("at least one stripe is required") }

	value, _ := d.objectCommitLocks.LoadOrStore(request.ObjectID, &sync.Mutex{})
	commitMu := value.(*sync.Mutex)
	commitMu.Lock()
	defer commitMu.Unlock()

	requests := append([]WriteStripeRequest(nil), request.Stripes...)
	seen := make(map[storage.StripeID]struct{}, len(requests))
	headerIndex := -1
	for i := range requests {
		if requests[i].ObjectID != request.ObjectID { return response, fmt.Errorf("stripe ObjectID does not match request ObjectID") }
		if requests[i].Context == nil { requests[i].Context = ctx }
		if _, exists := seen[requests[i].StripeID]; exists { return response, fmt.Errorf("duplicate stripe ID %d", requests[i].StripeID) }
		seen[requests[i].StripeID] = struct{}{}
		if requests[i].StripeID == 0 { headerIndex = i }
	}
	if headerIndex < 0 { return response, fmt.Errorf("Stripe 0 must be included in an Object commit") }
	header := requests[headerIndex]
	requests = append(requests[:headerIndex], requests[headerIndex+1:]...)
	sort.Slice(requests, func(i, j int) bool { return requests[i].StripeID < requests[j].StripeID })

	d.mapMu.RLock()
	var watermark storage.StripeVersion
	if stripes := d.objects[request.ObjectID]; stripes != nil && stripes[0] != nil { watermark = stripes[0].version }
	d.mapMu.RUnlock()
	if watermark == ^storage.StripeVersion(0) { return response, fmt.Errorf("Object version exhausted") }
	version := watermark + 1
	if err = d.removeUncommittedVersions(ctx, request.ObjectID, watermark); err != nil {
		return response, fmt.Errorf("remove uncommitted versions: %w", err)
	}
	for _, stripe := range requests {
		if err = ctx.Err(); err != nil { return response, err }
		if _, err = d.writeStripeAtVersion(stripe, version, false); err != nil {
			return response, fmt.Errorf("write data stripe %d: %w", stripe.StripeID, err)
		}
	}
	if err = ctx.Err(); err != nil { return response, err }
	if _, err = d.writeStripeAtVersion(header, version, false); err != nil {
		return response, fmt.Errorf("commit Stripe 0: %w", err)
	}

	d.mapMu.Lock()
	published := append(append([]WriteStripeRequest(nil), requests...), header)
	for _, stripe := range published {
		key := stripeIDKey{objectID: request.ObjectID, stripeID: stripe.StripeID}
		info := d.versions[key][version]
		if info == nil || !info.recoverable {
			d.mapMu.Unlock()
			return response, fmt.Errorf("committed stripe %d is missing from version index", stripe.StripeID)
		}
	}
	stripes := d.objects[request.ObjectID]
	if stripes == nil { stripes = make(map[storage.StripeID]*stripeInfo); d.objects[request.ObjectID] = stripes }
	for _, stripe := range published {
		key := stripeIDKey{objectID: request.ObjectID, stripeID: stripe.StripeID}
		info := d.versions[key][version]
		stripes[stripe.StripeID] = &stripeInfo{version: version, method: info.method, chunkSize: info.chunkSize, chunks: cloneChunks(info.chunks)}
	}
	for _, stripe := range published {
		d.refreshStripeCleanupQueueLocked(stripeIDKey{objectID: request.ObjectID, stripeID: stripe.StripeID})
	}
	d.mapMu.Unlock()
	response.Version = version
	return response, nil
}

func (d *Distribution) removeUncommittedVersions(ctx context.Context, objectID shared.ObjectID, watermark storage.StripeVersion) error {
	d.mapMu.RLock()
	locations := make([]chunkLocation, 0)
	for key, versions := range d.versions {
		if key.objectID != objectID { continue }
		for version, info := range versions {
			if version > watermark { for _, location := range info.chunks { locations = append(locations, location) } }
		}
	}
	for _, location := range d.orphans[objectID] {
		if location.key.StripeVersion > watermark { locations = append(locations, location) }
	}
	d.mapMu.RUnlock()
	seen := make(map[struct{ ioIndex int; key storage.FileKey }]struct{}, len(locations))
	for _, location := range locations {
		if err := ctx.Err(); err != nil { return err }
		identity := struct{ ioIndex int; key storage.FileKey }{location.ioIndex, location.key}
		if _, exists := seen[identity]; exists { continue }
		seen[identity] = struct{}{}
		_, err := d.ios[location.ioIndex].Delete(physicalio.DeleteRequest{Context: ctx, Key: location.key})
		if err != nil && !errors.Is(err, os.ErrNotExist) { return err }
	}
	d.mapMu.Lock()
	for key, versions := range d.versions {
		if key.objectID != objectID { continue }
		for version := range versions { if version > watermark { delete(versions, version) } }
		if len(versions) == 0 { delete(d.versions, key) }
		d.refreshStripeCleanupQueueLocked(key)
	}
	remaining := d.orphans[objectID][:0]
	for _, location := range d.orphans[objectID] { if location.key.StripeVersion <= watermark { remaining = append(remaining, location) } }
	if len(remaining) == 0 { delete(d.orphans, objectID) } else { d.orphans[objectID] = remaining }
	d.mapMu.Unlock()
	return nil
}
