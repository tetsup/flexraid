package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	hdioDriveCmd   = 0x031f
	ataStandbyNow  = 0xe0
	ataCommandSize = 4
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

// SpinDownRequest identifies the drive to place in standby.
type SpinDownRequest struct {
	Context context.Context
	WWN     string
}

// SpinDownResponse reports the drive that received the standby request.
type SpinDownResponse struct {
	WWN  string
	Path string
}

// SpinDown sends the ATA STANDBY IMMEDIATE command to the drive.
func SpinDown(request SpinDownRequest) (SpinDownResponse, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SpinDownResponse{}, err
	}
	drive, err := ResolveDrive(DriveRequest{Context: ctx, WWN: request.WWN})
	if err != nil {
		return SpinDownResponse{}, err
	}
	file, err := os.OpenFile(drive.Path, os.O_RDWR|unix.O_NONBLOCK, 0)
	if err != nil {
		return SpinDownResponse{}, fmt.Errorf("open drive %q for standby: %w", drive.WWN, err)
	}
	defer file.Close()

	command := [ataCommandSize]byte{ataStandbyNow}
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, file.Fd(), uintptr(hdioDriveCmd), uintptr(unsafe.Pointer(&command[0])))
	if errno != 0 {
		return SpinDownResponse{}, fmt.Errorf("send standby command to WWN %q: %w", drive.WWN, errno)
	}
	return SpinDownResponse{WWN: drive.WWN, Path: drive.Path}, nil
}

// SpinUpRequest identifies the drive to wake.
type SpinUpRequest struct {
	Context context.Context
	WWN     string
}

// SpinUpResponse reports the drive that was accessed to wake it.
type SpinUpResponse struct {
	WWN  string
	Path string
}

// SpinUp wakes the drive by issuing a read-only access to sector zero.
func SpinUp(request SpinUpRequest) (SpinUpResponse, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SpinUpResponse{}, err
	}
	drive, err := ResolveDrive(DriveRequest{Context: ctx, WWN: request.WWN})
	if err != nil {
		return SpinUpResponse{}, err
	}
	file, err := os.OpenFile(drive.Path, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return SpinUpResponse{}, fmt.Errorf("open drive %q to wake: %w", drive.WWN, err)
	}
	defer file.Close()

	buffer := make([]byte, 4096)
	if _, err := file.ReadAt(buffer, 0); err != nil {
		return SpinUpResponse{}, fmt.Errorf("read drive %q to wake: %w", drive.WWN, err)
	}
	if err := ctx.Err(); err != nil {
		return SpinUpResponse{}, err
	}
	return SpinUpResponse{WWN: drive.WWN, Path: drive.Path}, nil
}
