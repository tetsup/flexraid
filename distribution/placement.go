package distribution

import (
	"fmt"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type StripeIOIndexesRequest struct {
	ObjectID shared.ObjectID
	StripeID storage.StripeID
}
type StripeIOIndexesResponse struct{ IOIndexes []int }
type AvailableBytesRequest struct{}
type AvailableBytesResponse struct{ Bytes []uint64 }

// StripeIOIndexes returns persisted physical I/O indexes in logical chunk order,
// with parity last. The returned slice contains only the devices used by the stripe.
func (d *Distribution) StripeIOIndexes(request StripeIOIndexesRequest) (StripeIOIndexesResponse, error) {
	stripes := d.objects[request.ObjectID]
	if stripes == nil || stripes[request.StripeID] == nil {
		return StripeIOIndexesResponse{}, ErrStripeNotFound
	}
	info := stripes[request.StripeID]
	indexes := make([]int, 0, len(info.chunks))
	for _, no := range expectedChunkNos(info.method) {
		location, exists := info.chunks[no]
		if !exists {
			return StripeIOIndexesResponse{}, fmt.Errorf("stripe %d has no placement for chunk %d", request.StripeID, no)
		}
		indexes = append(indexes, location.ioIndex)
	}
	return StripeIOIndexesResponse{IOIndexes: indexes}, nil
}

// AvailableBytes reports available bytes for each I/O layer in index order.
func (d *Distribution) AvailableBytes(AvailableBytesRequest) (AvailableBytesResponse, error) {
	result := make([]uint64, len(d.ios))
	for index, layer := range d.ios {
		available, err := layer.AvailableBytes(physicalio.AvailableBytesRequest{})
		if err != nil {
			return AvailableBytesResponse{}, fmt.Errorf("get available capacity for I/O %d: %w", index, err)
		}
		result[index] = available.Bytes
	}
	return AvailableBytesResponse{Bytes: result}, nil
}
