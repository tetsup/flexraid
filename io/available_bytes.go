package io

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// AvailableBytes returns the filesystem's currently available bytes for this I/O root.
func (layer *IO) AvailableBytes() (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(layer.root, &stat); err != nil {
		return 0, fmt.Errorf("stat filesystem capacity: %w", err)
	}
	if stat.Bavail < 0 || stat.Bsize < 0 {
		return 0, fmt.Errorf("invalid filesystem capacity")
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
