package distribution

import (
	"fmt"

	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type StripeIOIndexesRequest struct {
	ObjectID shared.ObjectID
	StripeID storage.StripeID
}
type StripeIOIndexesResponse struct{ IOIndexes []int }

// StripeIOIndexes returns persisted physical I/O indexes in logical chunk order,
// with parity last. The returned slice contains only the devices used by the stripe.
func (d *Distribution) StripeIOIndexes(request StripeIOIndexesRequest) (StripeIOIndexesResponse, error) {
	info, unlock, err := d.lockSelectedStripeRead(stripeIDKey{objectID: request.ObjectID, stripeID: request.StripeID})
	if err != nil {
		return StripeIOIndexesResponse{}, err
	}
	defer unlock()
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
