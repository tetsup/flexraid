package distribution

import (
	"context"
	"errors"
	"fmt"

	physicalio "github.com/tetsup/flexraid/io"
)

// cacheStripeLocked loads and validates the selected latest version. The
// caller must hold mapMu and the stripe mutex, in that order.
func (d *Distribution) cacheStripeLocked(ctx context.Context, key stripeIDKey) error {
	info := d.objects[key.objectID][key.stripeID]
	if info == nil {
		return nil
	}
	version, method := info.version, info.method
	chunks := make([]chunkDataWithInfo, 0, len(info.chunks))
	for no, location := range info.chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if location.data == nil {
			read, err := d.ios[location.ioIndex].Read(physicalio.ReadRequest{Context: ctx, Key: location.key})
			d.recordChunkOperation("Read", err)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return err
				}
				continue
			}
			total, payload, err := decodeChunk(read.Data)
			if err != nil {
				continue
			}
			location.data, location.totalLength = payload, total
			info.chunks[no] = location
			if versions := d.versions[key]; versions != nil {
				if selected := versions[version]; selected != nil {
					selected.chunks[no] = location
				}
			}
		}
		chunks = append(chunks, chunkDataWithInfo{
			stripeKey:          stripeKeyInfo{objectID: key.objectID, stripeID: key.stripeID, stripeVersion: version},
			distributionMethod: method, chunkNo: no, data: location.data, totalLength: location.totalLength,
		})
	}
	if _, err := restoreStripe(chunks); err != nil {
		info.corrupted = true
		d.recordIntegrityError()
		return fmt.Errorf("%w: latest stripe version %d is not recoverable: %v", ErrDataCorrupted, version, err)
	}
	return nil
}
