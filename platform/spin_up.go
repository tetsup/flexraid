package platform

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

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
