package io

import (
	"fmt"

	"github.com/tetsup/flexraid/pkg/shared"
	"golang.org/x/sys/unix"
)

type CapacityRequest struct{}
type CapacityResponse struct{ Capacity shared.Capacity }

// Capacity returns the physical filesystem capacity for this I/O root.
func (layer *IO) GetCapacity(CapacityRequest) (CapacityResponse, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(layer.root, &stat); err != nil {
		return CapacityResponse{}, fmt.Errorf("stat filesystem capacity: %w", err)
	}
	if stat.Bsize <= 0 {
		return CapacityResponse{}, fmt.Errorf("invalid filesystem capacity")
	}
	blockSize := uint64(stat.Bsize)
	return CapacityResponse{Capacity: shared.Capacity{
		TotalBytes:     uint64(stat.Blocks) * blockSize,
		FreeBytes:      uint64(stat.Bfree) * blockSize,
		AvailableBytes: uint64(stat.Bavail) * blockSize,
	}}, nil
}
