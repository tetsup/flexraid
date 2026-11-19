package management

import (
	"fmt"
	"strings"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/orchestration"
	"github.com/tetsup/flexraid/platform"
	"github.com/tetsup/flexraid/pkg/shared"
)

// DriveConfig binds one storage root to its physical mountpoint and WWN.
type DriveConfig struct {
	Root                   string
	Mountpoint             string
	WWN                    string
	CleanupDeleteAllowed   bool
	CleanupPreserveAllowed bool
}

// NewRequest initializes Management with Orchestration and its startup configuration.
type NewRequest struct {
	Orchestration  *orchestration.Orchestration
	Distribution   *distribution.Distribution
	Initialization InitializationTask
	Config         Config
	CleanupPermissions *shared.CleanupPermissionStore
}

type cleanupPermissionStoreReceiver interface {
	SetCleanupPermissionStore(*shared.CleanupPermissionStore)
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
		if strings.TrimSpace(drive.WWN) != drive.WWN {
			return nil, fmt.Errorf("drive mapping %d has whitespace around WWN", index+1)
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

func resolveDriveMappings(drives []DriveConfig) (map[string]DriveConfig, error) {
	byWWN, err := validateDriveConfig(drives)
	if err != nil {
		return nil, err
	}
	for wwn := range byWWN {
		if _, err := platform.ResolveDrive(platform.DriveRequest{WWN: wwn}); err != nil {
			return nil, fmt.Errorf("validate configured WWN %q: %w", wwn, err)
		}
	}
	return byWWN, nil
}

// ValidateDriveMappings validates configuration and confirms each WWN resolves.
func ValidateDriveMappings(drives []DriveConfig) error {
	_, err := resolveDriveMappings(drives)
	return err
}

func configuredDriveOrder(drives []DriveConfig) []string {
	result := make([]string, len(drives))
	for index, drive := range drives {
		result[index] = drive.WWN
	}
	return result
}
