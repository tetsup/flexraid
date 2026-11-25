package distribution

import (
	"context"
	"errors"
	"fmt"
	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
	"os"
	"sort"
	"sync"
	"time"
)

type WriteStripeData struct {
	StripeID  storage.StripeID
	ChunkSize uint32
	Layout    Layout
	IOIndexes []int
	Data      []byte
	ZeroSize  uint64
}
type WriteStripesRequest struct {
	Context  context.Context
	ObjectID shared.ObjectID
	Stripes  []WriteStripeData
	Sync     bool
}
type WriteStripesResponse struct {
	Version          storage.StripeVersion
	PersistedStripes uint32
}

// WriteStripes commits all supplied Stripes under one Version. Stripe 0 is mandatory and written last.
func (d *Distribution) WriteStripes(request WriteStripesRequest) (response WriteStripesResponse, err error) {
	started := time.Now()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		logOperation(d, "WriteStripes", started, err, "object_id", request.ObjectID.String(), "stripes", len(request.Stripes), "version", response.Version, "persisted_stripes", response.PersistedStripes)
	}()
	if err = ctx.Err(); err != nil {
		return response, err
	}
	if len(request.Stripes) == 0 {
		return response, fmt.Errorf("at least one Stripe is required")
	}
	items := make(map[storage.StripeID]WriteStripeData, len(request.Stripes))
	ids := make([]int, 0, len(request.Stripes))
	hasHeader := false
	for _, item := range request.Stripes {
		if item.StripeID == 0 {
			hasHeader = true
		}
		if _, ok := items[item.StripeID]; ok {
			return response, fmt.Errorf("duplicate Stripe %d", item.StripeID)
		}
		if item.ChunkSize == 0 {
			return response, fmt.Errorf("Stripe %d chunk size must be positive", item.StripeID)
		}
		method, e := item.Layout.method()
		if e != nil {
			return response, e
		}
		required := int(method.numChunks)
		if method.distributionType == distributionParity {
			required++
		}
		if e = validateIOIndexes(item.IOIndexes, len(d.ios), required); e != nil {
			return response, fmt.Errorf("Stripe %d: %w", item.StripeID, e)
		}
		if item.ZeroSize > 0 && len(item.Data) > 0 {
			return response, fmt.Errorf("Stripe %d cannot specify both Data and ZeroSize", item.StripeID)
		}
		items[item.StripeID] = item
		ids = append(ids, int(item.StripeID))
	}
	if !hasHeader {
		return response, fmt.Errorf("Stripe 0 is required to commit an Object Version")
	}
	objectUnlock := d.lockObject(request.ObjectID)
	defer objectUnlock()
	sort.Ints(ids)
	unlocks := make([]func(), 0, len(ids))
	for _, raw := range ids {
		u, um := d.lockStripe(stripeIDKey{objectID: request.ObjectID, stripeID: storage.StripeID(raw)})
		um()
		unlocks = append(unlocks, u)
	}
	defer func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}()
	if err = d.removeUncommittedObjectVersions(ctx, request.ObjectID); err != nil {
		return response, fmt.Errorf("remove uncommitted Object versions: %w", err)
	}
	d.mapMu.Lock()
	version := d.nextObjectVersionLocked(request.ObjectID)
	d.mapMu.Unlock()
	if version == 0 {
		return response, fmt.Errorf("Object Version overflow")
	}
	response.Version = version
	order := make([]int, 0, len(ids))
	for _, id := range ids {
		if id != 0 {
			order = append(order, id)
		}
	}
	order = append(order, 0)
	written := make(map[storage.StripeID]map[chunkNo]chunkLocation, len(items))
	methods := make(map[storage.StripeID]distributionMethod, len(items))
	chunkSizes := make(map[storage.StripeID]uint32, len(items))
	publishPartial := func() {
		d.mapMu.Lock()
		defer d.mapMu.Unlock()
		for id, chunks := range written {
			key := stripeIDKey{objectID: request.ObjectID, stripeID: id}
			vs := d.versions[key]
			if vs == nil {
				vs = make(map[storage.StripeVersion]*versionInfo)
				d.versions[key] = vs
			}
			vs[version] = &versionInfo{method: methods[id], chunkSize: chunkSizes[id], chunks: cloneChunks(chunks), recoverable: false}
			d.refreshStripeCleanupQueueLocked(key)
		}
	}
	for _, raw := range order {
		if err = ctx.Err(); err != nil {
			publishPartial()
			return response, err
		}
		item := items[storage.StripeID(raw)]
		method, _ := item.Layout.method()
		data := item.Data
		if item.ZeroSize > 0 {
			if item.ZeroSize > uint64(int(^uint(0)>>1)) {
				publishPartial()
				return response, fmt.Errorf("Stripe %d zero payload exceeds addressable memory", raw)
			}
			data = make([]byte, int(item.ZeroSize))
		}
		capacity := int(item.ChunkSize) * int(method.numChunks)
		if method.distributionType == distributionMirror {
			capacity = int(item.ChunkSize)
		}
		if len(data) > capacity {
			publishPartial()
			return response, fmt.Errorf("Stripe %d data exceeds chunk capacity", raw)
		}
		chunks := make(map[chunkNo][]byte, int(method.numChunks)+int(method.numParities))
		if method.distributionType == distributionMirror {
			for i := uint8(0); i < method.numChunks; i++ {
				chunks[chunkNo(i)] = append([]byte(nil), data...)
			}
		} else {
			chunks = splitStripeWithSize(data, method.numChunks, int(item.ChunkSize))
			chunks[parityFlag] = generateParity(orderedDataChunks(chunks, method.numChunks))
		}
		methods[item.StripeID], chunkSizes[item.StripeID] = method, item.ChunkSize
		written[item.StripeID] = make(map[chunkNo]chunkLocation, len(chunks))
		for _, no := range orderedChunkNos(chunks) {
			if err = ctx.Err(); err != nil {
				publishPartial()
				return response, err
			}
			ioIndex := item.IOIndexes[int(no.index())]
			if no.isParity() {
				ioIndex = item.IOIndexes[int(method.numChunks)]
			}
			key := storage.FileKey{ObjectID: request.ObjectID, StripeVersion: version, LayoutID: layoutFor(method, no), StripeID: item.StripeID}
			encoded := encodeChunk(uint64(len(data)), chunks[no])
			_, we := d.ios[ioIndex].Write(physicalio.WriteRequest{Context: ctx, Key: key, Data: encoded, Sync: request.Sync})
			d.recordChunkOperation("Write", we)
			if we != nil {
				publishPartial()
				err = fmt.Errorf("write Object %s Stripe %d chunk %d to I/O %d: %w", request.ObjectID.String(), item.StripeID, no, ioIndex, we)
				if d.logger != nil {
					d.logger.Error("Object stripe commit failed", "object_id", request.ObjectID.String(), "stripe_id", item.StripeID, "version", version, "io_index", ioIndex, "chunk", no, "error", err)
				}
				return response, err
			}
			written[item.StripeID][no] = chunkLocation{ioIndex: ioIndex, key: key, data: append([]byte(nil), chunks[no]...), totalLength: uint64(len(data)), sizeBytes: uint64(len(encoded))}
		}
	}
	d.mapMu.Lock()
	defer d.mapMu.Unlock()
	for id, chunks := range written {
		key := stripeIDKey{objectID: request.ObjectID, stripeID: id}
		vs := d.versions[key]
		if vs == nil {
			vs = make(map[storage.StripeVersion]*versionInfo)
			d.versions[key] = vs
		}
		vs[version] = &versionInfo{method: methods[id], chunkSize: chunkSizes[id], chunks: cloneChunks(chunks), recoverable: true}
		ss := d.objects[request.ObjectID]
		if ss == nil {
			ss = make(map[storage.StripeID]*stripeInfo)
			d.objects[request.ObjectID] = ss
		}
		ss[id] = &stripeInfo{version: version, method: methods[id], chunkSize: chunkSizes[id], chunks: cloneChunks(chunks)}
		d.refreshStripeCleanupQueueLocked(key)
	}
	d.objectWatermarks[request.ObjectID] = version
	response.PersistedStripes = uint32(len(written))
	return response, nil
}
func (d *Distribution) lockObject(id shared.ObjectID) func() {
	d.objectLockMu.Lock()
	lock := d.objectLocks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		d.objectLocks[id] = lock
	}
	d.objectLockMu.Unlock()
	lock.Lock()
	return lock.Unlock
}
func (d *Distribution) nextObjectVersionLocked(id shared.ObjectID) storage.StripeVersion {
	var max storage.StripeVersion
	for key, vs := range d.versions {
		if key.objectID != id {
			continue
		}
		for v := range vs {
			if v > max {
				max = v
			}
		}
	}
	for _, loc := range d.orphans[id] {
		if loc.key.StripeVersion > max {
			max = loc.key.StripeVersion
		}
	}
	if max == ^storage.StripeVersion(0) {
		return 0
	}
	return max + 1
}
func (d *Distribution) removeUncommittedObjectVersions(ctx context.Context, id shared.ObjectID) error {
	d.mapMu.Lock()
	watermark := d.objectWatermarks[id]
	var locations []chunkLocation
	for key, vs := range d.versions {
		if key.objectID != id {
			continue
		}
		for v, info := range vs {
			if v > watermark {
				for _, loc := range info.chunks {
					locations = append(locations, loc)
				}
			}
		}
	}
	for _, loc := range d.orphans[id] {
		if loc.key.StripeVersion > watermark {
			locations = append(locations, loc)
		}
	}
	d.mapMu.Unlock()
	type identity struct {
		io  int
		key storage.FileKey
	}
	seen := make(map[identity]struct{}, len(locations))
	for _, loc := range locations {
		if err := ctx.Err(); err != nil {
			return err
		}
		k := identity{io: loc.ioIndex, key: loc.key}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		_, e := d.ios[loc.ioIndex].Delete(physicalio.DeleteRequest{Context: ctx, Key: loc.key})
		d.recordChunkOperation("Delete", e)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return fmt.Errorf("delete uncommitted version %d on I/O %d: %w", loc.key.StripeVersion, loc.ioIndex, e)
		}
	}
	d.mapMu.Lock()
	defer d.mapMu.Unlock()
	for key, vs := range d.versions {
		if key.objectID != id {
			continue
		}
		for v := range vs {
			if v > watermark {
				delete(vs, v)
			}
		}
		if len(vs) == 0 {
			delete(d.versions, key)
		}
	}
	remaining := d.orphans[id][:0]
	for _, loc := range d.orphans[id] {
		if loc.key.StripeVersion <= watermark {
			remaining = append(remaining, loc)
		}
	}
	if len(remaining) == 0 {
		delete(d.orphans, id)
	} else {
		d.orphans[id] = remaining
	}
	return nil
}
