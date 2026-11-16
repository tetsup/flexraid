package management

import (
	"context"
	"fmt"
	"syscall"

	"github.com/tetsup/flexraid/orchestration"
)

// Start begins the Management lifecycle. Startup initialization runs before
// the instance can be used by FUSE.
func (layer *Management) Start() error {
	if layer.scheduler == nil {
		return fmt.Errorf("Management has no startup initialization task")
	}
	return layer.scheduler.Start()
}

// WaitReady waits until all startup initialization units have completed.
func (layer *Management) WaitReady(ctx context.Context) error {
	if layer.scheduler == nil {
		return nil
	}
	return layer.scheduler.WaitReady(ctx)
}

// Phase returns the current Management lifecycle phase.
func (layer *Management) Phase() LifecyclePhase {
	if layer.scheduler == nil {
		return LifecycleReady
	}
	return layer.scheduler.Phase()
}

// Stop stops the lifecycle scheduler at a safe execution-unit boundary.
func (layer *Management) Stop(ctx context.Context) error {
	if layer.scheduler == nil {
		return nil
	}
	return layer.scheduler.Stop(ctx)
}

// SetOrchestration publishes the fully initialized Orchestration layer.
// It is only valid from an InitializationTask before the scheduler enters Ready.
func (layer *Management) SetOrchestration(orchestrationLayer *orchestration.Orchestration) error {
	if orchestrationLayer == nil {
		return fmt.Errorf("Orchestration layer is required")
	}
	if layer.scheduler == nil || layer.scheduler.Phase() != LifecycleInitializing {
		return fmt.Errorf("Orchestration can only be set during initialization")
	}
	layer.mu.Lock()
	defer layer.mu.Unlock()
	if layer.orchestration != nil {
		return fmt.Errorf("Orchestration layer is already set")
	}
	layer.orchestration = orchestrationLayer
	return nil
}

// LifecycleErrno returns the errno used to reject ordinary filesystem
// requests in the current lifecycle phase. Kernel bookkeeping such as
// Release is intentionally handled by the FUSE adapter without this check.
func (layer *Management) LifecycleErrno() syscall.Errno {
	switch layer.Phase() {
	case LifecycleReady:
		return 0
	case LifecycleFailed:
		return syscall.EIO
	default:
		return syscall.EBUSY
	}
}
