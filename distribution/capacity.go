package distribution

import (
	"fmt"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
)

type CapacityRequest struct{}
type CapacityResponse struct{ Capacities []shared.Capacity }

// Capacity returns physical capacities for all I/O layers in index order.
func (d *Distribution) Capacity(CapacityRequest) (CapacityResponse, error) {
	result := make([]shared.Capacity, len(d.ios))
	for index, layer := range d.ios {
		capacity, err := layer.Capacity(physicalio.CapacityRequest{})
		if err != nil {
			return CapacityResponse{}, fmt.Errorf("get capacity for I/O %d: %w", index, err)
		}
		result[index] = capacity.Capacity
	}
	return CapacityResponse{Capacities: result}, nil
}
