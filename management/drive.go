package management

import (
	"context"
	"fmt"

	"github.com/tetsup/flexraid/orchestration"
	"github.com/tetsup/flexraid/platform"
)

// DriveConfig binds one storage root to its physical mountpoint and WWN.
type DriveConfig struct {
	Root       string
	Mountpoint string
	WWN        string
}

// NewRequest initializes Management with Orchestration and the configured drives.
type NewRequest struct {
	Orchestration *orchestration.Orchestration
	Drives        []DriveConfig
}

// SpinUpRequest identifies a configured drive by WWN.
type SpinUpRequest struct {
	Context context.Context
	WWN     string
}

// SpinUpResponse reports the physical drive that was woken.
type SpinUpResponse struct {
	WWN string
}

// SpinDownRequest identifies a configured drive by WWN.
type SpinDownRequest struct {
	Context context.Context
	WWN     string
}

// SpinDownResponse reports the physical drive that received the standby request.
type SpinDownResponse struct {
	WWN string
}

func validateDriveConfig(drives []DriveConfig) (map[string]DriveConfig, error) {
	if len(drives) == 0 {
		return nil, fmt.Errorf("at least one drive mapping is required")
	}
	byWWN := make(map[string]DriveConfig, len(drives))
	byMountpoint := make(map[string]struct{}, len(drives))
	byRoot := make(map[string]struct{}, len(drives))
	for index, drive := range drives {
		if drive.Root == "" || drive.Mountpoint == "" || drive.WWN == "" {
			return nil, fmt.Errorf("drive mapping %d requires root, mountpoint, and WWN", index+1)
		}
		if _, exists := byRoot[drive.Root]; exists {
			return nil, fmt.Errorf("duplicate storage root %q", drive.Root)
		}
		if _, exists := byMountpoint[drive.Mountpoint]; exists {
			return nil, fmt.Errorf("duplicate physical mountpoint %q", drive.Mountpoint)
		}
		if _, exists := byWWN[drive.WWN]; exists {
			return nil, fmt.Errorf("duplicate WWN %q", drive.WWN)
		}
		byRoot[drive.Root] = struct{}{}
		byMountpoint[drive.Mountpoint] = struct{}{}
		byWWN[drive.WWN] = drive
	}
	return byWWN, nil
}

// ValidateDriveMappings validates configuration and confirms each WWN resolves.
func ValidateDriveMappings(drives []DriveConfig) error {
	byWWN, err := validateDriveConfig(drives)
	if err != nil {
		return err
	}
	for wwn := range byWWN {
		if _, err := platform.ResolveDrive(platform.DriveRequest{WWN: wwn}); err != nil {
			return fmt.Errorf("validate configured WWN %q: %w", wwn, err)
		}
	}
	return nil
}

// SpinUp requests spin-up only for a drive registered during initialization.
func (layer *Management) SpinUp(request SpinUpRequest) (SpinUpResponse, error) {
	if _, exists := layer.drives[request.WWN]; !exists {
		return SpinUpResponse{}, fmt.Errorf("WWN %q is not configured", request.WWN)
	}
	result, err := platform.SpinUp(platform.SpinUpRequest{Context: request.Context, WWN: request.WWN})
	if err != nil {
		return SpinUpResponse{}, err
	}
	return SpinUpResponse{WWN: result.WWN}, nil
}

// SpinDown requests standby only for a drive registered during initialization.
func (layer *Management) SpinDown(request SpinDownRequest) (SpinDownResponse, error) {
	if _, exists := layer.drives[request.WWN]; !exists {
		return SpinDownResponse{}, fmt.Errorf("WWN %q is not configured", request.WWN)
	}
	result, err := platform.SpinDown(platform.SpinDownRequest{Context: request.Context, WWN: request.WWN})
	if err != nil {
		return SpinDownResponse{}, err
	}
	return SpinDownResponse{WWN: result.WWN}, nil
}
