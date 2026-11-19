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

// SetDriveCleanupDeletePermission updates the in-memory deletion permission
// for a configured drive. It does not start cleanup by itself.
func (layer *Management) SetDriveCleanupDeletePermission(
	request SetDriveCleanupDeletePermissionRequest,
) (SetDriveCleanupDeletePermissionResponse, error) {
	if request.Context == nil {
		return SetDriveCleanupDeletePermissionResponse{}, fmt.Errorf("context is required")
	}
	if err := request.Context.Err(); err != nil {
		return SetDriveCleanupDeletePermissionResponse{}, err
	}

	layer.mu.Lock()
	defer layer.mu.Unlock()
	if _, exists := layer.drives[request.WWN]; !exists {
		return SetDriveCleanupDeletePermissionResponse{}, fmt.Errorf("WWN %q is not configured", request.WWN)
	}
	permissions, exists := layer.cleanupPermissions[request.WWN]
	if !exists {
		return SetDriveCleanupDeletePermissionResponse{}, fmt.Errorf("cleanup permissions for WWN %q are not initialized", request.WWN)
	}
	permissions.DeleteAllowed = request.Allowed
	layer.cleanupPermissions[request.WWN] = permissions
	if err := layer.publishCleanupPermissionsLocked(); err != nil {
		return SetDriveCleanupDeletePermissionResponse{}, err
	}
	return SetDriveCleanupDeletePermissionResponse{WWN: request.WWN, Allowed: permissions.DeleteAllowed}, nil
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

// SetDriveCleanupPreservePermission updates the in-memory preservation
// permission for a configured drive. It does not start cleanup by itself.
func (layer *Management) SetDriveCleanupPreservePermission(
	request SetDriveCleanupPreservePermissionRequest,
) (SetDriveCleanupPreservePermissionResponse, error) {
	if request.Context == nil {
		return SetDriveCleanupPreservePermissionResponse{}, fmt.Errorf("context is required")
	}
	if err := request.Context.Err(); err != nil {
		return SetDriveCleanupPreservePermissionResponse{}, err
	}

	layer.mu.Lock()
	defer layer.mu.Unlock()
	if _, exists := layer.drives[request.WWN]; !exists {
		return SetDriveCleanupPreservePermissionResponse{}, fmt.Errorf("WWN %q is not configured", request.WWN)
	}
	permissions, exists := layer.cleanupPermissions[request.WWN]
	if !exists {
		return SetDriveCleanupPreservePermissionResponse{}, fmt.Errorf("cleanup permissions for WWN %q are not initialized", request.WWN)
	}
	permissions.PreserveAllowed = request.Allowed
	layer.cleanupPermissions[request.WWN] = permissions
	if err := layer.publishCleanupPermissionsLocked(); err != nil {
		return SetDriveCleanupPreservePermissionResponse{}, err
	}
	return SetDriveCleanupPreservePermissionResponse{WWN: request.WWN, Allowed: permissions.PreserveAllowed}, nil
}

// GetDriveCleanupPermissionsRequest reads the current permissions for one drive.
type GetDriveCleanupPermissionsRequest struct {
	Context context.Context
	WWN     string
}

// GetDriveCleanupPermissionsResponse is a copy of the current drive permissions.
type GetDriveCleanupPermissionsResponse struct {
	WWN         string
	Permissions shared.DriveCleanupPermission
}

// GetDriveCleanupPermissions returns a snapshot without exposing Management's map.
func (layer *Management) GetDriveCleanupPermissions(
	request GetDriveCleanupPermissionsRequest,
) (GetDriveCleanupPermissionsResponse, error) {
	if request.Context == nil {
		return GetDriveCleanupPermissionsResponse{}, fmt.Errorf("context is required")
	}
	if err := request.Context.Err(); err != nil {
		return GetDriveCleanupPermissionsResponse{}, err
	}

	layer.mu.Lock()
	defer layer.mu.Unlock()
	if _, exists := layer.drives[request.WWN]; !exists {
		return GetDriveCleanupPermissionsResponse{}, fmt.Errorf("WWN %q is not configured", request.WWN)
	}
	permissions, exists := layer.cleanupPermissions[request.WWN]
	if !exists {
		return GetDriveCleanupPermissionsResponse{}, fmt.Errorf("cleanup permissions for WWN %q are not initialized", request.WWN)
	}
	return GetDriveCleanupPermissionsResponse{WWN: request.WWN, Permissions: permissions}, nil
}

// StopDistributionCleanupWorker stops the autonomous cleanup loop without
// stopping foreground filesystem operations.
func (layer *Management) StopDistributionCleanupWorker(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	layer.mu.Lock()
	distributed := layer.distribution
	layer.mu.Unlock()
	if distributed == nil {
		return fmt.Errorf("Distribution layer is not initialized")
	}
	return distributed.StopCleanupWorker(ctx)
}

// StartDistributionCleanupWorker starts the Distribution-owned cleanup loop
// after publishing the latest drive permission snapshot.
func (layer *Management) StartDistributionCleanupWorker(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	layer.mu.Lock()
	defer layer.mu.Unlock()
	if layer.distribution == nil {
		return fmt.Errorf("Distribution layer is not initialized")
	}
	if err := layer.publishCleanupPermissionsLocked(); err != nil {
		return err
	}
	return layer.distribution.StartCleanupWorker(ctx)
}
