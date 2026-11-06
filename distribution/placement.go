package distribution

import (
	"fmt"
	"sort"

	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

// StripeIOIndexes returns persisted physical I/O indexes in logical chunk order,
// with parity last. The returned slice contains only the devices used by the stripe.
func (d *Distribution) StripeIOIndexes(objectID shared.ObjectID, stripeID storage.StripeID) ([]int, error) {
	stripes := d.objects[objectID]
	if stripes == nil || stripes[stripeID] == nil {
		return nil, ErrStripeNotFound
	}
	info := stripes[stripeID]
	indexes := make([]int, 0, len(info.chunks))
	for _, no := range expectedChunkNos(info.method) {
		location, exists := info.chunks[no]
		if !exists {
			return nil, fmt.Errorf("stripe %d has no placement for chunk %d", stripeID, no)
		}
		indexes = append(indexes, location.ioIndex)
	}
	return indexes, nil
}

// AvailableIOBytes reports available bytes for each I/O layer in index order.
func (d *Distribution) AvailableIOBytes() ([]uint64, error) {
	result := make([]uint64, len(d.ios))
	for index, layer := range d.ios {
		available, err := layer.AvailableBytes()
		if err != nil {
			return nil, fmt.Errorf("get available capacity for I/O %d: %w", index, err)
		}
		result[index] = available
	}
	return result, nil
}
