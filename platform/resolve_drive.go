package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// DriveRequest identifies a physical drive by its World Wide Name.
type DriveRequest struct {
	Context context.Context
	WWN     string
}

// DriveResponse describes the uniquely resolved physical block device.
type DriveResponse struct {
	WWN  string
	Path string
}

// ResolveDrive resolves WWN through Linux's stable /dev/disk/by-id names.
// It never falls back to a device name, serial number, filesystem UUID, or PARTUUID.
func ResolveDrive(request DriveRequest) (DriveResponse, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return DriveResponse{}, err
	}
	wwn := strings.TrimSpace(request.WWN)
	if wwn == "" || wwn != request.WWN || strings.ContainsAny(wwn, "/\\*?[]") {
		return DriveResponse{}, fmt.Errorf("invalid WWN %q", request.WWN)
	}

	path := filepath.Join("/dev/disk/by-id", "wwn-"+wwn)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return DriveResponse{}, fmt.Errorf("resolve WWN %q: %w", wwn, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return DriveResponse{}, fmt.Errorf("stat device for WWN %q: %w", wwn, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Mode&syscall.S_IFMT != syscall.S_IFBLK {
		return DriveResponse{}, fmt.Errorf("WWN %q does not resolve to a block device", wwn)
	}

	base := filepath.Base(resolved)
	if _, err := os.Stat(filepath.Join("/sys/class/block", base, "partition")); err == nil {
		return DriveResponse{}, fmt.Errorf("WWN %q resolves to a partition, not a whole drive", wwn)
	} else if !os.IsNotExist(err) {
		return DriveResponse{}, fmt.Errorf("inspect block device for WWN %q: %w", wwn, err)
	}
	return DriveResponse{WWN: wwn, Path: resolved}, nil
}
