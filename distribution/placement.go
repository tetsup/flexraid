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

// SelectIOIndexes chooses exactly count devices, preferring the largest available capacity.
// Ties retain the original I/O index order for deterministic results.
func (d *Distribution) SelectIOIndexes(count int) ([]int, error) {
	available, err := d.AvailableIOBytes()
	if err != nil {
		return nil, err
	}
	if count < 1 || count > len(available) {
		return nil, fmt.Errorf("cannot select %d I/Os from %d", count, len(available))
	}
	indexes := make([]int, len(available))
	for index := range indexes {
		indexes[index] = index
	}
	sort.SliceStable(indexes, func(i, j int) bool {
		return available[indexes[i]] > available[indexes[j]]
	})
	return indexes[:count], nil
}
