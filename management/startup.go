package management

import (
	"context"
	"fmt"
	"syscall"
	"time"

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
	if ctx == nil {
		return fmt.Errorf("stop context is required")
	}
	if err := layer.StopBackgroundProcesses(ctx); err != nil {
		return err
	}
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
	if err := orchestrationLayer.SetCleanupPermissionStore(layer.cleanupPermissionStore); err != nil {
		return err
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

// StartBackgroundProcesses starts the autonomous processes owned by the
// initialized layers. It must be called after WaitReady and before accepting
// external filesystem requests. Publishing a layer during initialization must
// not start its worker before the remaining layers have been initialized.
func (layer *Management) StartBackgroundProcesses(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("background process context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if layer.Phase() != LifecycleReady {
		return fmt.Errorf("background processes can only start when Management is Ready")
	}
	layer.mu.Lock()
	defer layer.mu.Unlock()
	if layer.backgroundProcessesStarted {
		return nil
	}
	if layer.distribution == nil {
		return fmt.Errorf("Distribution layer is not initialized")
	}
	if layer.orchestration == nil {
		return fmt.Errorf("Orchestration layer is not initialized")
	}
	if err := layer.distribution.StartCleanupWorker(ctx); err != nil {
		return fmt.Errorf("start Distribution cleanup worker: %w", err)
	}
	if err := layer.orchestration.StartCleanupWorker(ctx, func(cleanupCtx context.Context) error {
		_, cleanupErr := layer.Cleanup(orchestration.CleanupRequest{Context: cleanupCtx})
		return cleanupErr
	}); err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = layer.distribution.StopCleanupWorker(stopCtx)
		return fmt.Errorf("start Orchestration cleanup worker: %w", err)
	}
	layer.backgroundProcessesStarted = true
	return nil
}

// StopBackgroundProcesses stops both logical Object cleanup and physical
// obsolete-version cleanup. Do not hold Management.mu while waiting: the
// Orchestration worker enters Cleanup through Management and needs that lock.
func (layer *Management) StopBackgroundProcesses(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("background process stop context is required")
	}
	layer.mu.Lock()
	orchestrationLayer := layer.orchestration
	distributed := layer.distribution
	layer.mu.Unlock()

	if orchestrationLayer != nil {
		if err := orchestrationLayer.StopCleanupWorker(ctx); err != nil {
			return fmt.Errorf("stop Orchestration cleanup worker: %w", err)
		}
	}
	if distributed != nil {
		if err := distributed.StopCleanupWorker(ctx); err != nil {
			return fmt.Errorf("stop Distribution cleanup worker: %w", err)
		}
	}
	layer.mu.Lock()
	layer.backgroundProcessesStarted = false
	layer.mu.Unlock()
	return nil
}
