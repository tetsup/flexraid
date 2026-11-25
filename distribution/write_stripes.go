package distribution

import (
	"context"
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync/atomic"
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
// Data stripes are durably persisted first; Stripe 0 is the final commit record.
func (d *Distribution) WriteStripes(request WriteStripesRequest) (response WriteStripesResponse, err error) {
	started := time.Now()
	defer func() {
		logOperation(d, "WriteStripes", started, err, "object_id", request.ObjectID.String(), "stripes", len(request.Stripes))
	}()
	ctx := request.Context
	if ctx == nil { ctx = context.Background() }
	if err = ctx.Err(); err != nil { return response, err }
	if len(request.Stripes) == 0 { return response, fmt.Errorf("at least one stripe is required") }

	unlock := d.lockObjectWrite(request.ObjectID)
	defer unlock()

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
		stripe.Sync = true
		if _, err = d.writeStripeAtVersion(stripe, version, false); err != nil {
			return response, fmt.Errorf("write data stripe %d: %w", stripe.StripeID, err)
		}
	}
	if err = ctx.Err(); err != nil { return response, err }
	header.Sync = true
	if _, writeErr := d.writeStripeAtVersion(header, version, false); writeErr != nil {
		// A failed Write response can be ambiguous if the file Sync succeeded
		// but the directory Sync response was lost. Re-sync and read every
		// expected chunk before deciding whether Stripe 0 committed.
		locations, verifyErr := d.verifyStripeVersion(ctx, header, version)
		if verifyErr != nil {
			return response, fmt.Errorf("commit Stripe 0: %w (verification failed: %v)", writeErr, verifyErr)
		}
		key := stripeIDKey{objectID: request.ObjectID, stripeID: 0}
		d.mapMu.Lock()
		versions := d.versions[key]
		if versions == nil { versions = make(map[storage.StripeVersion]*versionInfo); d.versions[key] = versions }
		method, _ := header.Layout.method()
		versions[version] = &versionInfo{method: method, chunkSize: header.ChunkSize, chunks: locations, recoverable: true}
		d.mapMu.Unlock()
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
		stripes := d.objects[objectID]
		var selected storage.StripeVersion
		var selectedInfo *versionInfo
		for versionNo, info := range versions {
			if versionNo <= watermark && info.recoverable && (selectedInfo == nil || versionNo > selected) {
				selected, selectedInfo = versionNo, info
			}
		}
		if selectedInfo == nil {
			if stripes != nil { delete(stripes, key.stripeID); if len(stripes) == 0 { delete(d.objects, objectID) } }
		} else {
			if stripes == nil { stripes = make(map[storage.StripeID]*stripeInfo); d.objects[objectID] = stripes }
			stripes[key.stripeID] = &stripeInfo{version:selected, method:selectedInfo.method, chunkSize:selectedInfo.chunkSize, chunks:cloneChunks(selectedInfo.chunks)}
		}
		d.refreshStripeCleanupQueueLocked(key)
	}
	remaining := d.orphans[objectID][:0]
	for _, location := range d.orphans[objectID] { if location.key.StripeVersion <= watermark { remaining = append(remaining, location) } }
	if len(remaining) == 0 { delete(d.orphans, objectID) } else { d.orphans[objectID] = remaining }
	d.mapMu.Unlock()
	return nil
}

func (d *Distribution) verifyStripeVersion(ctx context.Context, request WriteStripeRequest, version storage.StripeVersion) (map[chunkNo]chunkLocation, error) {
	method, err := request.Layout.method()
	if err != nil { return nil, err }
	required := int(method.numChunks)
	if method.distributionType == distributionParity { required++ }
	if err := validateIOIndexes(request.IOIndexes, len(d.ios), required); err != nil { return nil, err }
	locations := make(map[chunkNo]chunkLocation, required)
	chunks := make([]chunkDataWithInfo, 0, required)
	for _, no := range expectedChunkNos(method) {
		if err := ctx.Err(); err != nil { return nil, err }
		ioIndex := request.IOIndexes[int(no.index())]
		if no.isParity() { ioIndex = request.IOIndexes[int(method.numChunks)] }
		key := storage.FileKey{ObjectID:request.ObjectID, StripeID:request.StripeID, StripeVersion:version, LayoutID:layoutFor(method,no)}
		read, readErr := d.ios[ioIndex].Read(physicalio.ReadRequest{Context:ctx,Key:key})
		if readErr != nil {
			if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) { return nil, readErr }
			continue
		}
		// Only count a chunk after its file and parent directory have been
		// synchronized successfully. Other replicas/chunks may still provide
		// a recoverable Stripe if this one cannot be confirmed.
		if _, syncErr := d.ios[ioIndex].Sync(physicalio.SyncRequest{Context:ctx,Key:key}); syncErr != nil {
			if errors.Is(syncErr, context.Canceled) || errors.Is(syncErr, context.DeadlineExceeded) { return nil, syncErr }
			continue
		}
		total, payload, decodeErr := decodeChunk(read.Data)
		if decodeErr != nil { continue }
		locations[no] = chunkLocation{ioIndex:ioIndex,key:key,totalLength:total,sizeBytes:uint64(len(read.Data)),readers:&atomic.Int64{}}
		chunks = append(chunks, chunkDataWithInfo{
			stripeKey:stripeKeyInfo{objectID:request.ObjectID,stripeID:request.StripeID,stripeVersion:version},
			distributionMethod:method,chunkNo:no,data:payload,totalLength:total,
		})
	}
	restored, err := restoreStripe(chunks)
	if err != nil { return nil, err }
	if !bytes.Equal(restored,request.Data) { return nil, fmt.Errorf("verified Stripe 0 payload differs from requested data") }
	if len(locations) < required { d.recordIntegrityError() }
	return locations,nil
}
