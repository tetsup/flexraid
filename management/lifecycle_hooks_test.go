package management

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestLifecycleTransitionExecutorRunsHooksOnlyForPhaseChanges(t *testing.T) {
	var calls []string
	executor := LifecycleTransitionExecutor{Hooks: StateHookRegistry{
		LifecycleReady: {
			OnEnter: func(context.Context) error {
				calls = append(calls, "enter-ready")
				return nil
			},
			OnExit: func(context.Context) error {
				calls = append(calls, "exit-ready")
				return nil
			},
		},
	}}
	machine := LifecycleMachine{}
	state := LifecycleState{Phase: LifecycleInitializing}

	var err error
	state, _, err = executor.Transition(context.Background(), machine, state, InitializationStarted{})
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = executor.Transition(context.Background(), machine, state, InitializationUnitCompleted{})
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != LifecycleReady {
		t.Fatalf("phase = %v, want Ready", state.Phase)
	}

	// Events that do not change phase must not repeat phase hooks.
	state, _, err = executor.Transition(context.Background(), machine, state, BackgroundUnitRequested{TaskID: "cleanup"})
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = executor.Transition(context.Background(), machine, state, BackgroundUnitCompleted{TaskID: "cleanup"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"enter-ready"}) {
		t.Fatalf("hook calls = %v, want [enter-ready]", calls)
	}

	state, _, err = executor.Transition(context.Background(), machine, state,
		ExclusiveRequested{Request: ExclusiveRequest{ID: "maintenance", Kind: ExclusiveMaintenance}})
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != LifecycleMaintenance {
		t.Fatalf("phase = %v, want Maintenance", state.Phase)
	}
	state, _, err = executor.Transition(context.Background(), machine, state, ExclusiveUnitCompleted{RequestID: "maintenance"})
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != LifecycleReady {
		t.Fatalf("phase after maintenance = %v, want Ready", state.Phase)
	}
	if !reflect.DeepEqual(calls, []string{"enter-ready", "exit-ready", "enter-ready"}) {
		t.Fatalf("hook calls = %v", calls)
	}

	state, _, err = executor.Transition(context.Background(), machine, state, FatalError{Reason: "fatal"})
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != LifecycleFailed {
		t.Fatalf("phase = %v, want Failed", state.Phase)
	}
	if !reflect.DeepEqual(calls, []string{"enter-ready", "exit-ready", "enter-ready", "exit-ready"}) {
		t.Fatalf("hook calls = %v", calls)
	}
}

func TestLifecycleTransitionExecutorRollsBackFailedOnEnter(t *testing.T) {
	enterErr := errors.New("worker startup failed")
	var calls []string
	executor := LifecycleTransitionExecutor{Hooks: StateHookRegistry{
		LifecycleReady: {
			OnEnter: func(context.Context) error {
				calls = append(calls, "enter")
				return enterErr
			},
			OnExit: func(context.Context) error {
				calls = append(calls, "rollback")
				return nil
			},
		},
	}}
	state := LifecycleState{Phase: LifecycleInitializing, InitializationStarted: true}
	next, outputs, err := executor.Transition(context.Background(), LifecycleMachine{}, state, InitializationUnitCompleted{})
	if !errors.Is(err, enterErr) {
		t.Fatalf("error = %v, want wrapped %v", err, enterErr)
	}
	if next.Phase != LifecycleFailed {
		t.Fatalf("phase = %v, want Failed", next.Phase)
	}
	if state.Phase != LifecycleInitializing {
		t.Fatalf("input state was mutated: %+v", state)
	}
	if !reflect.DeepEqual(calls, []string{"enter", "rollback"}) {
		t.Fatalf("hook calls = %v, want [enter rollback]", calls)
	}
	if !hasOutput[LifecycleFailureReported](outputs) {
		t.Fatalf("missing lifecycle failure output: %#v", outputs)
	}
	for _, output := range outputs {
		if changed, ok := output.(LifecycleChanged); ok && changed.To == LifecycleReady {
			t.Fatalf("failed transition published Ready: %#v", outputs)
		}
	}
}

func TestLifecycleTransitionExecutorDoesNotEnterTargetAfterFailedOnExit(t *testing.T) {
	exitErr := errors.New("worker shutdown failed")
	targetEntered := false
	executor := LifecycleTransitionExecutor{Hooks: StateHookRegistry{
		LifecycleReady: {OnExit: func(context.Context) error { return exitErr }},
		LifecycleMaintenance: {OnEnter: func(context.Context) error {
			targetEntered = true
			return nil
		}},
	}}
	state := LifecycleState{Phase: LifecycleReady}
	next, _, err := executor.Transition(context.Background(), LifecycleMachine{}, state,
		ExclusiveRequested{Request: ExclusiveRequest{ID: "maintenance", Kind: ExclusiveMaintenance}})
	if !errors.Is(err, exitErr) {
		t.Fatalf("error = %v, want wrapped %v", err, exitErr)
	}
	if next.Phase != LifecycleFailed {
		t.Fatalf("phase = %v, want Failed", next.Phase)
	}
	if targetEntered {
		t.Fatal("target OnEnter ran after source OnExit failed")
	}
}
