package management

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type maintenanceProviderFunc func(context.Context, MaintenanceWorkID) (MaintenanceWorkResult, error)

func (run maintenanceProviderFunc) ExecuteUnit(ctx context.Context, id MaintenanceWorkID) (MaintenanceWorkResult, error) {
	return run(ctx, id)
}

func TestMaintenanceExecutorRunsPlanInOrderOneUnitAtATime(t *testing.T) {
	var calls []string
	provider := maintenanceProviderFunc(func(_ context.Context, id MaintenanceWorkID) (MaintenanceWorkResult, error) {
		calls = append(calls, string(id))
		return MaintenanceWorkResult{Completed: id != "multi"}, nil
	})
	executor, err := NewMaintenanceExecutor(MaintenanceExecutorRequest{
		Plan: MaintenancePlan{Batches: []MaintenanceBatch{
			{Items: []MaintenanceWorkItem{
				{ProviderID: "p", WorkID: "multi", DiskIDs: []string{"disk-a"}},
				{ProviderID: "p", WorkID: "after"},
			}},
			{Items: []MaintenanceWorkItem{{ProviderID: "p", WorkID: "last"}}},
		}},
		Providers: map[MaintenanceProviderID]MaintenanceWorkProvider{"p": provider},
	})
	if err != nil {
		t.Fatal(err)
	}

	for index, want := range []struct {
		batch int
		work  MaintenanceWorkID
		done  bool
		plan  bool
	}{
		{0, "multi", false, false},
		{0, "multi", false, false},
		{0, "multi", true, false},
		{0, "after", true, false},
		{1, "last", true, true},
	} {
		got, err := executor.ExecuteNext(context.Background())
		if err != nil {
			t.Fatalf("ExecuteNext %d: %v", index, err)
		}
		if got.BatchIndex != want.batch || got.WorkItem.WorkID != want.work ||
			got.WorkItemDone != want.done || got.PlanCompleted != want.plan {
			t.Fatalf("ExecuteNext %d = %+v, want batch=%d work=%q itemDone=%v planDone=%v",
				index, got, want.batch, want.work, want.done, want.plan)
		}
		if got.ExecutedUnits != uint64(index+1) {
			t.Fatalf("executed units = %d, want %d", got.ExecutedUnits, index+1)
		}
	}
	if want := []string{"multi", "multi", "multi", "after", "last"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("provider calls = %v, want %v", calls, want)
	}

	got, err := executor.ExecuteNext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.PlanCompleted || got.BatchIndex != -1 || got.ExecutedUnits != 5 {
		t.Fatalf("completed plan response = %+v", got)
	}
}

func TestMaintenanceExecutorCopiesPlanAndDiskMetadata(t *testing.T) {
	item := MaintenanceWorkItem{ProviderID: "p", WorkID: "w", DiskIDs: []string{"disk-a"}}
	plan := MaintenancePlan{Batches: []MaintenanceBatch{{Items: []MaintenanceWorkItem{item}}}}
	executor, err := NewMaintenanceExecutor(MaintenanceExecutorRequest{
		Plan: plan,
		Providers: map[MaintenanceProviderID]MaintenanceWorkProvider{
			"p": maintenanceProviderFunc(func(context.Context, MaintenanceWorkID) (MaintenanceWorkResult, error) {
				return MaintenanceWorkResult{Completed: true}, nil
			}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan.Batches[0].Items[0].DiskIDs[0] = "mutated"
	got, err := executor.ExecuteNext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.WorkItem.DiskIDs, []string{"disk-a"}) {
		t.Fatalf("executor plan was not copied: %+v", got.WorkItem)
	}
	got.WorkItem.DiskIDs[0] = "response-mutated"
	if !reflect.DeepEqual(item.DiskIDs, []string{"disk-a"}) {
		t.Fatalf("response aliases caller data: %+v", item)
	}
}

func TestMaintenanceExecutorRejectsInvalidPlan(t *testing.T) {
	tests := []struct {
		name      string
		plan      MaintenancePlan
		providers map[MaintenanceProviderID]MaintenanceWorkProvider
	}{
		{
			name: "missing provider ID",
			plan: MaintenancePlan{Batches: []MaintenanceBatch{{Items: []MaintenanceWorkItem{{WorkID: "w"}}}}},
		},
		{
			name: "missing work ID",
			plan: MaintenancePlan{Batches: []MaintenanceBatch{{Items: []MaintenanceWorkItem{{ProviderID: "p"}}}}},
			providers: map[MaintenanceProviderID]MaintenanceWorkProvider{"p": maintenanceProviderFunc(func(context.Context, MaintenanceWorkID) (MaintenanceWorkResult, error) {
				return MaintenanceWorkResult{}, nil
			})},
		},
		{
			name: "unknown provider",
			plan: MaintenancePlan{Batches: []MaintenanceBatch{{Items: []MaintenanceWorkItem{{ProviderID: "missing", WorkID: "w"}}}}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewMaintenanceExecutor(MaintenanceExecutorRequest{Plan: test.plan, Providers: test.providers}); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestMaintenanceExecutorKeepsWorkItemAfterProviderError(t *testing.T) {
	expected := errors.New("temporary provider failure")
	attempts := 0
	executor, err := NewMaintenanceExecutor(MaintenanceExecutorRequest{
		Plan: MaintenancePlan{Batches: []MaintenanceBatch{{Items: []MaintenanceWorkItem{{ProviderID: "p", WorkID: "w"}}}}},
		Providers: map[MaintenanceProviderID]MaintenanceWorkProvider{
			"p": maintenanceProviderFunc(func(context.Context, MaintenanceWorkID) (MaintenanceWorkResult, error) {
				attempts++
				if attempts == 1 {
					return MaintenanceWorkResult{}, expected
				}
				return MaintenanceWorkResult{Completed: true}, nil
			}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ExecuteNext(context.Background()); !errors.Is(err, expected) {
		t.Fatalf("first execution error = %v, want %v", err, expected)
	}
	got, err := executor.ExecuteNext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.PlanCompleted || got.ExecutedUnits != 1 || attempts != 2 {
		t.Fatalf("retry response = %+v, attempts = %d", got, attempts)
	}
}

func TestMaintenanceExecutorHonorsCanceledContextBeforeUnit(t *testing.T) {
	called := false
	executor, err := NewMaintenanceExecutor(MaintenanceExecutorRequest{
		Plan: MaintenancePlan{Batches: []MaintenanceBatch{{Items: []MaintenanceWorkItem{{ProviderID: "p", WorkID: "w"}}}}},
		Providers: map[MaintenanceProviderID]MaintenanceWorkProvider{
			"p": maintenanceProviderFunc(func(context.Context, MaintenanceWorkID) (MaintenanceWorkResult, error) {
				called = true
				return MaintenanceWorkResult{Completed: true}, nil
			}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := executor.ExecuteNext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("execution error = %v, want context.Canceled", err)
	}
	if called {
		t.Fatal("provider ran with canceled context")
	}
}

func TestMaintenanceExecutorRunsAsSchedulerBackgroundTask(t *testing.T) {
	called := make(chan struct{}, 1)
	executor, err := NewMaintenanceExecutor(MaintenanceExecutorRequest{
		Plan: MaintenancePlan{Batches: []MaintenanceBatch{{
			Items: []MaintenanceWorkItem{{ProviderID: "p", WorkID: "w"}},
		}}},
		Providers: map[MaintenanceProviderID]MaintenanceWorkProvider{
			"p": maintenanceProviderFunc(func(context.Context, MaintenanceWorkID) (MaintenanceWorkResult, error) {
				called <- struct{}{}
				return MaintenanceWorkResult{Completed: true}, nil
			}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	scheduler := NewScheduler(executor)
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, called, "maintenance provider unit")
	deadline := time.After(2 * time.Second)
	for scheduler.BackgroundError() != nil {
		t.Fatalf("maintenance background task failed: %v", scheduler.BackgroundError())
	}
	select {
	case <-time.After(10 * time.Millisecond):
	case <-deadline:
		t.Fatal("maintenance scheduler did not settle")
	}
	stopAndWaitScheduler(t, scheduler)
}
