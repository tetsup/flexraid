package platform

import (
	"context"
	"fmt"

	"github.com/tetsup/flexraid/pkg/shared"
	"golang.org/x/sys/unix"
)

// CapacityRequest identifies the filesystem whose physical capacity is queried.
type CapacityRequest struct {
	Context context.Context
	Root    string
}

// CapacityResponse reports physical filesystem capacity in bytes.
type CapacityResponse struct {
	Capacity shared.Capacity
}

// GetCapacity returns the physical capacity of the filesystem containing Root.
func GetCapacity(request CapacityRequest) (CapacityResponse, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return CapacityResponse{}, err
	}
	if request.Root == "" {
		return CapacityResponse{}, fmt.Errorf("filesystem root is required")
	}

	var stat unix.Statfs_t
	if err := unix.Statfs(request.Root, &stat); err != nil {
		return CapacityResponse{}, fmt.Errorf("stat filesystem capacity for %q: %w", request.Root, err)
	}
	if stat.Bsize <= 0 {
		return CapacityResponse{}, fmt.Errorf("invalid filesystem capacity for %q", request.Root)
	}
	blockSize := uint64(stat.Bsize)
	return CapacityResponse{Capacity: shared.Capacity{
		TotalBytes:     uint64(stat.Blocks) * blockSize,
		FreeBytes:      uint64(stat.Bfree) * blockSize,
		AvailableBytes: uint64(stat.Bavail) * blockSize,
	}}, nil
}
