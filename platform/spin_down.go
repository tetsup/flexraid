package platform

import (
	"context"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	hdioDriveCmd   = 0x031f
	ataStandbyNow  = 0xe0
	ataCommandSize = 4
)

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
