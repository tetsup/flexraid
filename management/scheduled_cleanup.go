package management

import (
	"context"
	"fmt"

	"github.com/tetsup/flexraid/distribution"
)

// SetDistribution publishes the initialized Distribution layer and starts its
// autonomous cleanup worker. It is used by the initialization path.
func (layer *Management) SetDistribution(distributed *distribution.Distribution) error {
	if distributed == nil {
		return fmt.Errorf("Distribution layer is required")
	}
	if layer.scheduler == nil || layer.scheduler.Phase() != LifecycleInitializing {
		return fmt.Errorf("Distribution can only be set during initialization")
	}
	layer.mu.Lock()
	defer layer.mu.Unlock()
	if layer.distribution != nil {
		return fmt.Errorf("Distribution layer is already set")
	}
	if err := distributed.SetCleanupPermissionStore(layer.cleanupPermissionStore); err != nil {
		return err
	}
	layer.distribution = distributed
	if err := layer.publishCleanupPermissionsLocked(); err != nil {
		layer.distribution = nil
		return err
	}
	if err := distributed.StartCleanupWorker(context.Background()); err != nil {
		layer.distribution = nil
		return err
	}
	return nil
}

// publishCleanupPermissionsLocked passes a fresh drive-ordered permission
// snapshot to Distribution. The caller must hold layer.mu.
func (layer *Management) publishCleanupPermissionsLocked() error {
	if layer.orchestration != nil {
		if err := layer.orchestration.SetCleanupPermissionStore(layer.cleanupPermissionStore); err != nil { return err }
	}
	if layer.distribution != nil {
		layer.distribution.NotifyCleanupPermissionsChanged()
		return nil
	}
	return nil
}
