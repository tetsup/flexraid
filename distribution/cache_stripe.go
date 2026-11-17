package distribution

import (
	"context"
	"fmt"

	physicalio "github.com/tetsup/flexraid/io"
)

// cacheStripeLocked loads and validates the latest selected version into the
// in-memory chunk cache. The caller must hold the stripe lock.
func (d *Distribution) cacheStripeLocked(ctx context.Context, key stripeIDKey) error {
	d.mapMu.Lock()
	info := d.objects[key.objectID][key.stripeID]
	if info == nil {
		d.mapMu.Unlock()
		return nil
	}
	version, method := info.version, info.method
	locations := make(map[chunkNo]chunkLocation, len(info.chunks))
	for no, location := range info.chunks { locations[no] = location }
	d.mapMu.Unlock()

	chunks := make([]chunkDataWithInfo, 0, len(locations))
	for no, location := range locations {
		if err := ctx.Err(); err != nil { return err }
		if location.data == nil {
			read, err := d.ios[location.ioIndex].Read(physicalio.ReadRequest{Context: ctx, Key: location.key})
			if err != nil { d.recordIOError(location.ioIndex, err); continue }
			total, payload, err := decodeChunk(read.Data)
			if err != nil { d.recordIOError(location.ioIndex, err); continue }
			location.data, location.totalLength = payload, total
			locations[no] = location
		}
		chunks = append(chunks, chunkDataWithInfo{
			stripeKey: stripeKeyInfo{objectID:key.objectID, stripeID:key.stripeID, stripeVersion:version},
			distributionMethod:method, chunkNo:no, data:location.data, totalLength:location.totalLength,
		})
	}
	if _, err := restoreStripe(chunks); err != nil {
		return fmt.Errorf("latest stripe version %d is not recoverable: %w", version, err)
	}
	d.mapMu.Lock()
	defer d.mapMu.Unlock()
	current := d.objects[key.objectID][key.stripeID]
	if current == nil || current.version != version { return ErrDataCorrupted }
	for no, location := range locations {
		current.chunks[no] = location
	}
	if versions := d.versions[key]; versions != nil {
		if selected := versions[version]; selected != nil {
			for no, location := range locations { selected.chunks[no] = location }
		}
	}
	return nil
}
