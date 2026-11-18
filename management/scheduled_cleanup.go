package management

import (
	"context"
	"fmt"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

// ExecuteScheduledCleanupRequest describes one unit selected by the cleanup
// schedule. WWN is resolved through the configured drive order to Distribution's
// I/O index; the caller selects the Stripe and cleanup method.
type ExecuteScheduledCleanupRequest struct {
	Context  context.Context
	WWN      string
	ObjectID shared.ObjectID
	StripeID storage.StripeID
	Method   distribution.CleanupMethod
}

// ExecuteScheduledCleanupResponse reports whether the unit was skipped by
// policy and, if executed, the single-file Distribution result.
type ExecuteScheduledCleanupResponse struct {
	WWN          string
	IOIndex      int
	Skipped      bool
	Distribution distribution.CleanupStripeDriveResponse
}

// ExecuteScheduledCleanup applies the current per-drive permission and asks
// Distribution to process at most one file. It is intended to be called as
// one Management scheduler execution unit; the schedule itself chooses the
// target Stripe and method.
func (layer *Management) ExecuteScheduledCleanup(
	request ExecuteScheduledCleanupRequest,
) (ExecuteScheduledCleanupResponse, error) {
	if request.Context == nil {
		return ExecuteScheduledCleanupResponse{}, fmt.Errorf("context is required")
	}
	if err := request.Context.Err(); err != nil {
		return ExecuteScheduledCleanupResponse{}, err
	}
	if request.Method != distribution.CleanupMethodDeleteOldVersion &&
		request.Method != distribution.CleanupMethodPreserveAbnormal {
		return ExecuteScheduledCleanupResponse{}, fmt.Errorf("unsupported cleanup method %d", request.Method)
	}

	layer.mu.Lock()
	drive, exists := layer.drives[request.WWN]
	if !exists {
		layer.mu.Unlock()
		return ExecuteScheduledCleanupResponse{}, fmt.Errorf("WWN %q is not configured", request.WWN)
	}
	permissions, exists := layer.cleanupPermissions[request.WWN]
	if !exists {
		layer.mu.Unlock()
		return ExecuteScheduledCleanupResponse{}, fmt.Errorf("cleanup permissions for WWN %q are not initialized", request.WWN)
	}
	ioIndex := -1
	for index, wwn := range layer.driveOrder {
		if wwn == drive.WWN {
			ioIndex = index
			break
		}
	}
	distributed := layer.distribution
	layer.mu.Unlock()

	if ioIndex < 0 {
		return ExecuteScheduledCleanupResponse{}, fmt.Errorf("WWN %q has no configured I/O index", request.WWN)
	}
	if distributed == nil {
		return ExecuteScheduledCleanupResponse{}, fmt.Errorf("Distribution layer is not initialized")
	}

	allowed := permissions.DeleteAllowed
	if request.Method == distribution.CleanupMethodPreserveAbnormal {
		allowed = permissions.PreserveAllowed
	}
	response := ExecuteScheduledCleanupResponse{WWN: request.WWN, IOIndex: ioIndex}
	if !allowed {
		response.Skipped = true
		return response, nil
	}

	result, err := distributed.CleanupStripeDrive(distribution.CleanupStripeDriveRequest{
		Context:  request.Context,
		IOIndex:  ioIndex,
		ObjectID: request.ObjectID,
		StripeID: request.StripeID,
		Method:   request.Method,
	})
	if err != nil {
		return response, err
	}
	response.Distribution = result
	return response, nil
}

// SetDistribution publishes the initialized Distribution layer during startup.
// In non-initializing configurations, pass Distribution through NewRequest.
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
	layer.distribution = distributed
	return nil
}

func configuredDriveOrder(drives []DriveConfig) []string {
	result := make([]string, len(drives))
	for index, drive := range drives {
		result[index] = drive.WWN
	}
	return result
}
