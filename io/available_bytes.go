package io

import (
	"fmt"

	"golang.org/x/sys/unix"
)

type AvailableBytesRequest struct{}
type AvailableBytesResponse struct{ Bytes uint64 }

// AvailableBytes returns the filesystem's currently available bytes for this I/O root.
func (layer *IO) AvailableBytes(AvailableBytesRequest) (AvailableBytesResponse, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(layer.root, &stat); err != nil {
		return AvailableBytesResponse{}, fmt.Errorf("stat filesystem capacity: %w", err)
	}
	if stat.Bsize <= 0 {
		return AvailableBytesResponse{}, fmt.Errorf("invalid filesystem capacity")
	}
	return AvailableBytesResponse{Bytes: uint64(stat.Bavail) * uint64(stat.Bsize)}, nil
}
