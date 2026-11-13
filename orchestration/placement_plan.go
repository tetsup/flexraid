package orchestration

import (
	"fmt"
	"sort"

	"github.com/tetsup/flexraid/pkg/shared"
	cfg "github.com/tetsup/flexraid/pkg/shared/config"
)

type stripePlacement struct {
	StripeID  uint64
	ChunkSize uint32
	Layout    layout
	DataSize  uint64
}

// planPlacement returns the logical capacity and physical layout for each stripe.
func planPlacement(size uint64, overrides ...cfg.Placement) ([]stripePlacement, error) {
	p := cfg.DefaultPlacement()
	if len(overrides) > 1 {
		return nil, fmt.Errorf("at most one placement override")
	}
	if len(overrides) == 1 {
		p = overrides[0]
	}
	if err := validatePersistencePlacement(p); err != nil {
		return nil, err
	}
	first := stripePlacement{StripeID: 0, ChunkSize: cfg.MinimumChunkSize}
	if size <= cfg.HeaderDataCapacity {
		first.Layout = layout{Mode: layoutModeMirror, ChunkSize: cfg.MinimumChunkSize, DataChunks: 2}
		first.DataSize = size
		return []stripePlacement{first}, nil
	}
	firstCap := cfg.HeaderDataCapacity + uint64(p.DataChunks-1)*uint64(cfg.MinimumChunkSize)
	first.Layout = layout{Mode: layoutModeStripe, ChunkSize: cfg.MinimumChunkSize, DataChunks: uint16(p.DataChunks), ParityChunks: uint16(p.ParityChunks)}
	first.DataSize = size
	if first.DataSize > firstCap {
		first.DataSize = firstCap
	}
	plan := []stripePlacement{first}
	if size <= firstCap {
		return plan, nil
	}
	remaining := size - firstCap
	block := uint64(cfg.MinimumChunkSize)
	blocks := (remaining + uint64(p.DataChunks)*block - 1) / (uint64(p.DataChunks) * block)
	chunk := blocks * block
	if chunk > uint64(p.MaxChunkSize) {
		chunk = uint64(p.MaxChunkSize)
	}
	capacity := chunk * uint64(p.DataChunks)
	count := (remaining + capacity - 1) / capacity
	for id := uint64(1); id <= count; id++ {
		n := remaining
		if n > capacity {
			n = capacity
		}
		plan = append(plan, stripePlacement{StripeID: id, ChunkSize: uint32(chunk), Layout: layout{Mode: layoutModeStripe, ChunkSize: uint32(chunk), DataChunks: uint16(p.DataChunks), ParityChunks: uint16(p.ParityChunks)}, DataSize: n})
		remaining -= n
	}
	return plan, nil
}

// planPlacementForResize preserves the existing multi-stripe layout. A new
// placement policy is selected only when the resulting file fits in Stripe 0.
func planPlacementForResize(old header, size uint64) ([]stripePlacement, error) {
	first, err := geometryFor(old, 0)
	if err != nil {
		return nil, err
	}
	if size <= first.Capacity {
		return planPlacement(size)
	}
	if first.Layout.Type != distribution.LayoutParity || old.Layout.ChunkSize == 0 {
		return nil, fmt.Errorf("%w: cannot extend a single-stripe mirror without replanning", errInvalidHeader)
	}
	regularCapacity := uint64(old.Layout.ChunkSize) * uint64(first.Layout.NumChunks)
	if regularCapacity == 0 {
		return nil, errInvalidHeader
	}
	remaining := size - first.Capacity
	count := 1 + (remaining-1)/regularCapacity
	if count > uint64(int(^uint(0)>>1))-1 {
		return nil, fmt.Errorf("stripe count exceeds addressable range: %w", os.ErrInvalid)
	}
	plan := make([]stripePlacement, 0, int(count)+1)
	firstPlacement := stripePlacement{
		StripeID: 0, ChunkSize: cfg.MinimumChunkSize, Layout: old.Layout, DataSize: first.Capacity,
	}
	plan = append(plan, firstPlacement)
	for id := uint64(1); id <= count; id++ {
		n := remaining
		if n > regularCapacity {
			n = regularCapacity
		}
		plan = append(plan, stripePlacement{
			StripeID: id, ChunkSize: old.Layout.ChunkSize, Layout: old.Layout, DataSize: n,
		})
		remaining -= n
	}
	return plan, nil
}

func selectIOIndexes(capacities []shared.Capacity, count int) ([]int, error) {
	if count < 1 || count > len(capacities) {
		return nil, fmt.Errorf("cannot select %d I/Os from %d", count, len(capacities))
	}
	indexes := make([]int, len(capacities))
	for index := range indexes {
		indexes[index] = index
	}
	sort.SliceStable(indexes, func(i, j int) bool {
		return capacities[indexes[i]].AvailableBytes > capacities[indexes[j]].AvailableBytes
	})
	return indexes[:count], nil
}

func validatePersistencePlacement(p cfg.Placement) error {
	if p.DiskCount < 1 || p.DiskCount > 9 || p.DataChunks < 1 || p.DataChunks > 8 ||
		p.ParityChunks != 1 || p.DiskCount < p.DataChunks+p.ParityChunks {
		return fmt.Errorf("invalid placement layout")
	}
	if p.MaxChunkSize < cfg.MinimumChunkSize || p.MaxChunkSize%cfg.MinimumChunkSize != 0 {
		return fmt.Errorf("invalid maximum chunk size")
	}
	return nil
}
