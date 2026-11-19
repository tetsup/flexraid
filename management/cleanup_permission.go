package management

import (
	"context"
	"fmt"

	"github.com/tetsup/flexraid/pkg/shared"
)

// SetDriveCleanupDeletePermissionRequest changes physical deletion permission.
type SetDriveCleanupDeletePermissionRequest struct {
	Context context.Context
	WWN     string
	Allowed bool
}

// SetDriveCleanupDeletePermissionResponse reports the updated permission.
type SetDriveCleanupDeletePermissionResponse struct {
	WWN     string
	Allowed bool
}

// SetDriveCleanupDeletePermission updates the shared permission state. It does
// not acquire Management's filesystem-operation lock, so an active Object
// cleanup can finish and observe the new setting at the next Object boundary.
func (layer *Management) SetDriveCleanupDeletePermission(
	request SetDriveCleanupDeletePermissionRequest,
) (SetDriveCleanupDeletePermissionResponse, error) {
	if request.Context == nil {
		return SetDriveCleanupDeletePermissionResponse{}, fmt.Errorf("context is required")
	}
	if err := request.Context.Err(); err != nil {
		return SetDriveCleanupDeletePermissionResponse{}, err
	}
	layer.cleanupPermissionUpdateMu.Lock()
	defer layer.cleanupPermissionUpdateMu.Unlock()
	if _, exists := layer.drives[request.WWN]; !exists {
		return SetDriveCleanupDeletePermissionResponse{}, fmt.Errorf("WWN %q is not configured", request.WWN)
	}
	index := layer.cleanupDriveIndex(request.WWN)
	if index < 0 {
		return SetDriveCleanupDeletePermissionResponse{}, fmt.Errorf("cleanup permissions for WWN %q are not initialized", request.WWN)
	}
	snapshot := layer.cleanupPermissionStore.Snapshot()
	if index >= len(snapshot.Drives) {
		return SetDriveCleanupDeletePermissionResponse{}, fmt.Errorf("cleanup permission index %d is out of range", index)
	}
	snapshot.Drives[index].DeleteAllowed = request.Allowed
	if err := layer.cleanupPermissionStore.Replace(snapshot.Drives); err != nil {
		return SetDriveCleanupDeletePermissionResponse{}, err
	}
	if layer.distribution != nil {
		layer.distribution.NotifyCleanupPermissionsChanged()
	}
	return SetDriveCleanupDeletePermissionResponse{WWN: request.WWN, Allowed: request.Allowed}, nil
}

// SetDriveCleanupPreservePermissionRequest changes abnormal-data preservation permission.
type SetDriveCleanupPreservePermissionRequest struct {
	Context context.Context
	WWN     string
	Allowed bool
}

// SetDriveCleanupPreservePermissionResponse reports the updated permission.
type SetDriveCleanupPreservePermissionResponse struct {
	WWN     string
	Allowed bool
}

// SetDriveCleanupPreservePermission updates the shared preservation permission
// without blocking an active Object-level cleanup work unit.
func (layer *Management) SetDriveCleanupPreservePermission(
	request SetDriveCleanupPreservePermissionRequest,
) (SetDriveCleanupPreservePermissionResponse, error) {
	if request.Context == nil {
		return SetDriveCleanupPreservePermissionResponse{}, fmt.Errorf("context is required")
	}
	if err := request.Context.Err(); err != nil {
		return SetDriveCleanupPreservePermissionResponse{}, err
	}
	layer.cleanupPermissionUpdateMu.Lock()
	defer layer.cleanupPermissionUpdateMu.Unlock()
	if _, exists := layer.drives[request.WWN]; !exists {
		return SetDriveCleanupPreservePermissionResponse{}, fmt.Errorf("WWN %q is not configured", request.WWN)
	}
	index := layer.cleanupDriveIndex(request.WWN)
	if index < 0 {
		return SetDriveCleanupPreservePermissionResponse{}, fmt.Errorf("cleanup permissions for WWN %q are not initialized", request.WWN)
	}
	snapshot := layer.cleanupPermissionStore.Snapshot()
	if index >= len(snapshot.Drives) {
		return SetDriveCleanupPreservePermissionResponse{}, fmt.Errorf("cleanup permission index %d is out of range", index)
	}
	snapshot.Drives[index].PreserveAllowed = request.Allowed
	if err := layer.cleanupPermissionStore.Replace(snapshot.Drives); err != nil {
		return SetDriveCleanupPreservePermissionResponse{}, err
	}
	if layer.distribution != nil {
		layer.distribution.NotifyCleanupPermissionsChanged()
	}
	return SetDriveCleanupPreservePermissionResponse{WWN: request.WWN, Allowed: request.Allowed}, nil
}

// GetDriveCleanupPermissionsRequest reads the current permissions for one drive.
type GetDriveCleanupPermissionsRequest struct {
	Context context.Context
	WWN     string
}

// GetDriveCleanupPermissionsResponse contains the current shared drive permission.
type GetDriveCleanupPermissionsResponse struct {
	WWN         string
	Permissions shared.DriveCleanupPermission
}

// GetDriveCleanupPermissions returns a value from the shared permission snapshot.
func (layer *Management) GetDriveCleanupPermissions(
	request GetDriveCleanupPermissionsRequest,
) (GetDriveCleanupPermissionsResponse, error) {
	if request.Context == nil {
		return GetDriveCleanupPermissionsResponse{}, fmt.Errorf("context is required")
	}
	if err := request.Context.Err(); err != nil {
		return GetDriveCleanupPermissionsResponse{}, err
	}
	if _, exists := layer.drives[request.WWN]; !exists {
		return GetDriveCleanupPermissionsResponse{}, fmt.Errorf("WWN %q is not configured", request.WWN)
	}
	index := layer.cleanupDriveIndex(request.WWN)
	snapshot := layer.cleanupPermissionStore.Snapshot()
	if index < 0 || index >= len(snapshot.Drives) {
		return GetDriveCleanupPermissionsResponse{}, fmt.Errorf("cleanup permissions for WWN %q are not initialized", request.WWN)
	}
	return GetDriveCleanupPermissionsResponse{WWN: request.WWN, Permissions: snapshot.Drives[index]}, nil
}

func (layer *Management) cleanupDriveIndex(wwn string) int {
	for index, configured := range layer.driveOrder {
		if configured == wwn {
			return index
		}
	}
	return -1
}

// StopDistributionCleanupWorker stops the Distribution-owned cleanup loop.
func (layer *Management) StopDistributionCleanupWorker(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	layer.mu.Lock()
	defer layer.mu.Unlock()
	if layer.distribution == nil {
		return fmt.Errorf("Distribution layer is not initialized")
	}
	if err := layer.distribution.StopCleanupWorker(ctx); err != nil {
		return err
	}
	layer.backgroundProcessesStarted = false
	return nil
}

// StartDistributionCleanupWorker starts all currently configured background
// processes. It is retained as a focused entry point for the current
// Distribution-owned cleanup process.
func (layer *Management) StartDistributionCleanupWorker(ctx context.Context) error {
	return layer.StartBackgroundProcesses(ctx)
}

