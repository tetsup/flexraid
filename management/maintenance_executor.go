package management

import (
	"context"
	"fmt"
	"sync"
)

// MaintenanceProviderID identifies a provider registered with a MaintenanceExecutor.
type MaintenanceProviderID string

// MaintenanceWorkID identifies one provider-owned unit of maintenance work.
type MaintenanceWorkID string

// MaintenanceDiskID identifies a disk referenced by a policy-produced plan.
type MaintenanceDiskID string

// MaintenancePlan is an ordered execution plan produced by MaintenancePolicy.
type MaintenancePlan struct {
	Batches []MaintenanceBatch
}

// MaintenanceBatch groups work items selected by the policy.
// Grouping does not define a lock scope; each provider unit is executed separately.
type MaintenanceBatch struct {
	Items []MaintenanceWorkItem
}

// MaintenanceWorkItem identifies provider-owned work and records the disks
// considered by the policy. DiskIDs are descriptive plan data; the executor
// does not inspect or revalidate disk state.
type MaintenanceWorkItem struct {
	ProviderID MaintenanceProviderID
	WorkID     MaintenanceWorkID
	DiskIDs    []MaintenanceDiskID
}

// MaintenanceWorkResult reports whether the provider-owned work item has
// completed. A false value means another safe unit remains for the same item.
type MaintenanceWorkResult struct {
	Completed bool
}

// MaintenanceWorkProvider executes exactly one recoverable unit of a work item.
type MaintenanceWorkProvider interface {
	ExecuteUnit(context.Context, MaintenanceWorkID) (MaintenanceWorkResult, error)
}

// MaintenanceExecutorRequest supplies an immutable plan and its work providers.
type MaintenanceExecutorRequest struct {
	Plan      MaintenancePlan
	Providers map[MaintenanceProviderID]MaintenanceWorkProvider
}

// MaintenanceExecutorResponse describes one executed unit.
// WorkItem is a copy of the item selected from the plan.
type MaintenanceExecutorResponse struct {
	BatchIndex    int
	WorkItem      MaintenanceWorkItem
	WorkItemDone  bool
	PlanCompleted bool
	ExecutedUnits uint64
}

// MaintenanceExecutor executes a policy-produced plan in its exact batch and
// item order. It makes no scheduling, disk-state, or power-state decisions.
type MaintenanceExecutor struct {
	mu        sync.Mutex
	batches   []MaintenanceBatch
	providers map[MaintenanceProviderID]MaintenanceWorkProvider
	batch     int
	item      int
	units     uint64
}

// NewMaintenanceExecutor validates and copies the plan and provider registry.
func NewMaintenanceExecutor(request MaintenanceExecutorRequest) (*MaintenanceExecutor, error) {
	providers := make(map[MaintenanceProviderID]MaintenanceWorkProvider, len(request.Providers))
	for id, provider := range request.Providers {
		if id == "" {
			return nil, fmt.Errorf("maintenance provider ID is required")
		}
		if provider == nil {
			return nil, fmt.Errorf("maintenance provider %q is nil", id)
		}
		providers[id] = provider
	}

	batches := make([]MaintenanceBatch, len(request.Plan.Batches))
	for batchIndex, batch := range request.Plan.Batches {
		batches[batchIndex].Items = make([]MaintenanceWorkItem, len(batch.Items))
		for itemIndex, item := range batch.Items {
			if item.ProviderID == "" {
				return nil, fmt.Errorf("maintenance batch %d item %d: provider ID is required", batchIndex, itemIndex)
			}
			if item.WorkID == "" {
				return nil, fmt.Errorf("maintenance batch %d item %d: work ID is required", batchIndex, itemIndex)
			}
			if _, exists := providers[item.ProviderID]; !exists {
				return nil, fmt.Errorf("maintenance batch %d item %d: provider %q is not registered", batchIndex, itemIndex, item.ProviderID)
			}
			batches[batchIndex].Items[itemIndex] = cloneMaintenanceWorkItem(item)
		}
	}

	return &MaintenanceExecutor{
		batches:   batches,
		providers: providers,
	}, nil
}

// ExecuteNext executes at most one provider unit from the next unfinished item.
// Empty batches and already completed items are skipped without performing I/O.
func (executor *MaintenanceExecutor) ExecuteNext(ctx context.Context) (MaintenanceExecutorResponse, error) {
	if ctx == nil {
		return MaintenanceExecutorResponse{}, fmt.Errorf("maintenance execution context is required")
	}
	if err := ctx.Err(); err != nil {
		return MaintenanceExecutorResponse{}, err
	}

	executor.mu.Lock()
	defer executor.mu.Unlock()

	executor.advanceCompletedItems()
	if executor.batch >= len(executor.batches) {
		return MaintenanceExecutorResponse{
			BatchIndex:    -1,
			PlanCompleted: true,
			ExecutedUnits: executor.units,
		}, nil
	}

	batchIndex := executor.batch
	item := cloneMaintenanceWorkItem(executor.batches[executor.batch].Items[executor.item])
	provider := executor.providers[item.ProviderID]

	result, err := provider.ExecuteUnit(ctx, item.WorkID)
	if err != nil {
		return MaintenanceExecutorResponse{}, fmt.Errorf(
			"execute maintenance work %q from provider %q: %w",
			item.WorkID, item.ProviderID, err,
		)
	}

	executor.units++
	if result.Completed {
		executor.item++
		executor.advanceCompletedItems()
	}

	return MaintenanceExecutorResponse{
		BatchIndex:    batchIndex,
		WorkItem:      item,
		WorkItemDone:  result.Completed,
		PlanCompleted: executor.batch >= len(executor.batches),
		ExecutedUnits: executor.units,
	}, nil
}

// Next adapts MaintenanceExecutor to the Scheduler's BackgroundTask contract.
// The Scheduler already owns the shared execution lock around this call.
func (executor *MaintenanceExecutor) Next(ctx context.Context) (bool, error) {
	result, err := executor.ExecuteNext(ctx)
	if err != nil {
		return false, err
	}
	return result.PlanCompleted, nil
}

func (executor *MaintenanceExecutor) advanceCompletedItems() {
	for executor.batch < len(executor.batches) {
		if executor.item < len(executor.batches[executor.batch].Items) {
			return
		}
		executor.batch++
		executor.item = 0
	}
}

func cloneMaintenanceWorkItem(item MaintenanceWorkItem) MaintenanceWorkItem {
	item.DiskIDs = append([]string(nil), item.DiskIDs...)
	return item
}
