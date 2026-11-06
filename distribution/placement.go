package distribution

import (
	"fmt"
	"sort"

	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

package distribution

import (
	"fmt"
	"sort"
)

// StripeIOIndexes returns the persisted physical I/O order for a stripe.
// Data chunks are returned in logical chunk order; parity is last.
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

// AvailableIOBytes reports currently available bytes for each I/O layer in index order.
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

// orderIOIndexes returns exactly count indexes, preferring I/Os with the most free space.
// Ties retain their original index order for deterministic placement.
func orderIOIndexes(available []uint64, count int) ([]int, error) {
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

 // SelectIOIndexes chooses exactly count devices, preferring the largest available capacity.
func (d *Distribution) SelectIOIndexes(count int) ([]int, error) {
	available, err := d.AvailableIOBytes()
	if err != nil {
		return nil, err
	}
	return orderIOIndexes(available, count)
}
