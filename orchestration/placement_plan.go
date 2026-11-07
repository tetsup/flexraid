package orchestration

import (
	"fmt"
	"github.com/tetsup/flexraid/pkg/shared"
	cfg "github.com/tetsup/flexraid/pkg/shared/config"
	"github.com/tetsup/flexraid/pkg/shared"
	"sort"
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
func selectIOIndexes(capacities []shared.Capacity, count int) ([]int, error) {
	if count < 1 || count > len(capacities) {
		return nil, fmt.Errorf("cannot select %d I/Os from %d", count, len(capacities))
	}
	indexes := make([]int, len(available))
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
