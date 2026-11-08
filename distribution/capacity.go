package distribution

import (
	"fmt"
	"math"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/pkg/shared/config"
)

type CapacityRequest struct {
	BlockSize    uint64
	MirrorCopies int
	DataChunks   int
	ParityChunks int
}

type CapacityResponse struct {
	Capacities            []shared.Capacity
	MirrorAvailableBlocks  uint64
	ParityAvailableBlocks  uint64
}

// GetCapacity returns physical capacities for all I/O layers in index order,
// and estimates logical capacity for Mirror and Parity layouts. The estimate
// counts complete stripes first and subtracts the minimum header overhead last.
func (d *Distribution) GetCapacity(request CapacityRequest) (CapacityResponse, error) {
	blockSize := request.BlockSize
	if blockSize == 0 {
		blockSize = config.BlockSize
	}
	mirrorCopies := request.MirrorCopies
	if mirrorCopies == 0 {
		mirrorCopies = 2
	}
	dataChunks := request.DataChunks
	if dataChunks == 0 {
		dataChunks = config.DefaultDataChunks
	}
	parityChunks := request.ParityChunks
	if parityChunks == 0 {
		parityChunks = config.DefaultParityChunks
	}
	if blockSize == 0 || mirrorCopies < 2 || dataChunks < 1 || parityChunks < 1 {
		return CapacityResponse{}, fmt.Errorf("invalid capacity layout configuration")
	}

	result := CapacityResponse{Capacities: make([]shared.Capacity, len(d.ios))}
	availableBlocks := make([]uint64, 0, len(d.ios))
	for index, layer := range d.ios {
		capacity, err := layer.GetCapacity(physicalio.CapacityRequest{})
		if err != nil {
			return CapacityResponse{}, fmt.Errorf("get capacity for I/O %d: %w", index, err)
		}
		result.Capacities[index] = capacity.Capacity
		availableBlocks = append(availableBlocks, capacity.Capacity.AvailableBytes/blockSize)
	}

	mirrorStripes := maxStripeCount(availableBlocks, mirrorCopies)
	mirrorRawBytes := saturatingMul(mirrorStripes, blockSize)
	mirrorHeaders := saturatingMul(mirrorStripes, config.HeaderReservedSize)
	mirrorBytes := subtractFloor(mirrorRawBytes, mirrorHeaders)
	result.MirrorAvailableBlocks = mirrorBytes / blockSize

	parityStripes := maxStripeCount(availableBlocks, dataChunks+parityChunks)
	parityRawBytes := saturatingMul(saturatingMul(parityStripes, uint64(dataChunks)), blockSize)
	parityHeaders := uint64(0)
	if parityRawBytes > 0 {
		// A large Parity file minimizes the number of headers to one.
		parityHeaders = config.HeaderReservedSize
	}
	parityBytes := subtractFloor(parityRawBytes, parityHeaders)
	result.ParityAvailableBlocks = parityBytes / blockSize

	return result, nil
}

// maxStripeCount computes the maximum number of complete stripes that can be
// assigned across the available devices. Every stripe uses devicesPerStripe
// distinct devices, and each device can contribute at most one chunk per stripe.
// It uses a monotonic feasibility test instead of pairing devices greedily.
func maxStripeCount(available []uint64, devicesPerStripe int) uint64 {
	if devicesPerStripe <= 0 || len(available) < devicesPerStripe {
		return 0
	}
	var total uint64
	for _, blocks := range available {
		total = saturatingAdd(total, blocks)
	}
	low, high := uint64(0), total/uint64(devicesPerStripe)
	for low < high {
		mid := low + (high-low+1)/2
		var usable uint64
		for _, blocks := range available {
			usable = saturatingAdd(usable, min(blocks, mid))
		}
		if usable >= saturatingMul(mid, uint64(devicesPerStripe)) {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return low
}

func saturatingAdd(a, b uint64) uint64 {
	if math.MaxUint64-a < b {
		return math.MaxUint64
	}
	return a + b
}

func saturatingMul(a, b uint64) uint64 {
	if a != 0 && b > math.MaxUint64/a {
		return math.MaxUint64
	}
	return a * b
}

func subtractFloor(value, deduction uint64) uint64 {
	if deduction >= value {
		return 0
	}
	return value - deduction
}
