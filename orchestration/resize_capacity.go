package orchestration

import (
	"context"
	"fmt"
	"syscall"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
	cfg "github.com/tetsup/flexraid/pkg/shared/config"
	"github.com/tetsup/flexraid/storage"
)

// validateResizeCapacity rejects impossible growth before building a placement
// plan or allocating per-stripe metadata. It counts distributable data stripes,
// not the aggregate raw byte capacity.
func (layer *Orchestration) validateResizeCapacity(ctx context.Context, old header, newSize uint64) error {
	if newSize <= old.Size {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	capacity, err := layer.distribution.GetCapacity(distribution.CapacityRequest{})
	if err != nil {
		return fmt.Errorf("get capacity for resize: %w", err)
	}

	first, err := geometryFor(old, 0)
	if err != nil {
		return err
	}
	var firstLayout distribution.Layout
	var firstChunk uint32
	var firstCapacity uint64
	var regularLayout distribution.Layout
	var regularChunk uint32
	if newSize <= first.Capacity {
		plan, planErr := planPlacement(newSize)
		if planErr != nil {
			return planErr
		}
		firstLayout, err = distributionLayout(plan[0].Layout)
		if err != nil {
			return err
		}
		firstChunk = cfg.MinimumChunkSize
		firstCapacity = first.Capacity
		regularLayout = firstLayout
	} else if first.Layout.Type == distribution.LayoutParity {
		firstLayout = first.Layout
		firstChunk = cfg.MinimumChunkSize
		firstCapacity = first.Capacity
		regularLayout = first.Layout
		regularChunk = old.Layout.ChunkSize
	} else {
		p := cfg.DefaultPlacement()
		firstCapacity = cfg.HeaderDataCapacity + uint64(p.DataChunks-1)*uint64(cfg.MinimumChunkSize)
		firstLayout = distribution.Layout{Type: distribution.LayoutParity, NumChunks: uint8(p.DataChunks)}
		firstChunk = cfg.MinimumChunkSize
		regularLayout = firstLayout
		if newSize > firstCapacity {
			remaining := newSize - firstCapacity
			blocks := (remaining + uint64(p.DataChunks)*uint64(cfg.MinimumChunkSize) - 1) /
				(uint64(p.DataChunks) * uint64(cfg.MinimumChunkSize))
			regularChunk = uint32(blocks * uint64(cfg.MinimumChunkSize))
			if regularChunk > p.MaxChunkSize {
				regularChunk = p.MaxChunkSize
			}
		}
	}

	firstRequired := int(firstLayout.NumChunks)
	if firstLayout.Type == distribution.LayoutParity {
		firstRequired++
	}
	firstIndexes, placementErr := layer.distribution.StripeIOIndexes(distribution.StripeIOIndexesRequest{
		ObjectID: old.ObjectID, StripeID: storage.StripeID(0),
	})
	if placementErr != nil || len(firstIndexes.IOIndexes) != firstRequired {
		firstIndexes.IOIndexes, err = selectIOIndexes(capacity.Capacities, firstRequired)
		if err != nil {
			return fmt.Errorf("select devices for first stripe: %w", err)
		}
	}
	available := make([]uint64, len(capacity.Capacities))
	for i, item := range capacity.Capacities {
		available[i] = item.AvailableBytes
	}
	for _, index := range firstIndexes.IOIndexes {
		if available[index] < uint64(firstChunk) {
			return syscall.ENOSPC
		}
		available[index] -= uint64(firstChunk)
	}

	if regularChunk == 0 || newSize <= firstCapacity {
		return nil
	}
	regularCapacity := uint64(regularChunk) * uint64(regularLayout.NumChunks)
	if regularCapacity == 0 {
		return syscall.ENOSPC
	}
	newCount := 1 + (newSize-firstCapacity-1)/regularCapacity

	// EFBIG means the requested logical size exceeds the filesystem's
	// theoretical distributable capacity. ENOSPC is reserved for requests
	// that fit within total capacity but cannot fit in the currently free space.
	totalCapacity := make([]uint64, len(capacity.Capacities))
	totalDevices := make([]shared.Capacity, len(capacity.Capacities))
	for i, item := range capacity.Capacities {
		totalCapacity[i] = item.TotalBytes / uint64(regularChunk)
		totalDevices[i] = item
		totalDevices[i].AvailableBytes = item.TotalBytes
	}
	totalFirstIndexes, err := selectIOIndexes(totalDevices, firstRequired)
	if err != nil {
		return fmt.Errorf("select devices for total-capacity check: %w", err)
	}
	for _, index := range totalFirstIndexes {
		if totalCapacity[index] > 0 {
			totalCapacity[index]--
		}
	}
	devicesPerStripe := int(regularLayout.NumChunks)
	if regularLayout.Type == distribution.LayoutParity {
		devicesPerStripe++
	}
	if maxDistributableStripeCount(totalCapacity, devicesPerStripe) < newCount {
		return syscall.EFBIG
	}

	var oldCount uint64
	if first.Layout.Type == distribution.LayoutParity && old.Size > firstCapacity {
		oldRegularCapacity := uint64(old.Layout.ChunkSize) * uint64(first.Layout.NumChunks)
		if oldRegularCapacity > 0 {
			oldCount = 1 + (old.Size-firstCapacity-1)/oldRegularCapacity
		}
	}
	required := uint64(0)
	if newCount > oldCount {
		required = newCount - oldCount
	}
	if first.Layout.Type == distribution.LayoutParity && old.Size > firstCapacity &&
		old.Size%(uint64(old.Layout.ChunkSize)*uint64(first.Layout.NumChunks)) != 0 {
		// The old partial tail must be rewritten to add its zero-filled suffix.
		required++
	}
	if required == 0 {
		return nil
	}

	chunksPerDevice := make([]uint64, len(available))
	for i, bytes := range available {
		chunksPerDevice[i] = bytes / uint64(regularChunk)
	}
	if maxDistributableStripeCount(chunksPerDevice, devicesPerStripe) < required {
		return syscall.ENOSPC
	}
	return nil
}

func maxDistributableStripeCount(available []uint64, devicesPerStripe int) uint64 {
	if devicesPerStripe <= 0 || len(available) < devicesPerStripe {
		return 0
	}
	var total uint64
	for _, value := range available {
		if ^uint64(0)-total < value {
			total = ^uint64(0)
			break
		}
		total += value
	}
	low, high := uint64(0), total/uint64(devicesPerStripe)
	for low < high {
		mid := low + (high-low+1)/2
		var usable uint64
		for _, value := range available {
			add := value
			if add > mid {
				add = mid
			}
			if ^uint64(0)-usable < add {
				usable = ^uint64(0)
				break
			}
			usable += add
		}
		if mid > ^uint64(0)/uint64(devicesPerStripe) ||
			usable < mid*uint64(devicesPerStripe) {
			high = mid - 1
		} else {
			low = mid
		}
	}
	return low
}
