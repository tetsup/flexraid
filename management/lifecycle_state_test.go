package management

import (
	"syscall"
	"testing"
)

func transition(t *testing.T, state LifecycleState, input LifecycleInput) (LifecycleState, []LifecycleOutput) {
	t.Helper()
	next, outputs, err := (LifecycleMachine{}).Transition(state, input)
	if err != nil {
		t.Fatalf("Transition(%T): %v", input, err)
	}
	return next, outputs
}

func hasOutput[T LifecycleOutput](outputs []LifecycleOutput) bool {
	for _, output := range outputs {
		if _, ok := output.(T); ok {
			return true
		}
	}
	return false
}

func TestLifecycleInitialization(t *testing.T) {
	state := LifecycleState{Phase: LifecycleInitializing}
	state, outputs := transition(t, state, InitializationStarted{})
	if !state.InitializationStarted || !hasOutput[RunInitializationUnit](outputs) {
		t.Fatalf("initialization did not start: state=%+v outputs=%#v", state, outputs)
	}
	state, outputs = transition(t, state, InitializationUnitCompleted{More: true})
	if state.Phase != LifecycleInitializing || !hasOutput[RunInitializationUnit](outputs) {
		t.Fatalf("initialization did not continue: state=%+v outputs=%#v", state, outputs)
	}
	state, outputs = transition(t, state, InitializationUnitCompleted{})
	if state.Phase != LifecycleReady || state.InitializationStarted || !hasOutput[LifecycleChanged](outputs) {
		t.Fatalf("initialization did not enter Ready: state=%+v outputs=%#v", state, outputs)
	}
}

func TestNormalAdmissionAndDrain(t *testing.T) {
	state := LifecycleState{Phase: LifecycleReady}
	state, outputs := transition(t, state, NormalOperationRequested{ID: "op-1"})
	if len(state.ActiveOperations) != 1 || !hasOutput[AdmitNormalOperation](outputs) {
		t.Fatalf("normal operation was not admitted: state=%+v outputs=%#v", state, outputs)
	}
	state, outputs = transition(t, state, ExclusiveRequested{Request: ExclusiveRequest{ID: "maintenance", Kind: ExclusiveMaintenance}})
	if state.Phase != LifecycleDraining || state.RunningExclusive != nil || !hasOutput[PauseBackground](outputs) {
		t.Fatalf("exclusive request did not drain: state=%+v outputs=%#v", state, outputs)
	}
	state, outputs = transition(t, state, NormalOperationRequested{ID: "op-2"})
	if !hasOutput[RejectNormalOperation](outputs) {
		t.Fatalf("normal operation admitted during drain: %#v", outputs)
	}
	rejected := false
	for _, output := range outputs {
		if result, ok := output.(RejectNormalOperation); ok {
			rejected = result.Errno == syscall.EBUSY
		}
	}
	if !rejected {
		t.Fatalf("draining rejection must use EBUSY: %#v", outputs)
	}
	state, outputs = transition(t, state, NormalOperationCompleted{ID: "op-1"})
	if state.Phase != LifecycleMaintenance || state.RunningExclusive == nil ||
		state.RunningExclusive.ID != "maintenance" || !hasOutput[StartExclusive](outputs) {
		t.Fatalf("exclusive did not start after drain: state=%+v outputs=%#v", state, outputs)
	}
}

func TestExclusiveQueueStaysClosedUntilEmpty(t *testing.T) {
	state := LifecycleState{Phase: LifecycleReady}
	state, _ = transition(t, state, ExclusiveRequested{Request: ExclusiveRequest{ID: "one", Kind: ExclusiveMaintenance}})
	state, _ = transition(t, state, ExclusiveRequested{Request: ExclusiveRequest{ID: "two", Kind: ExclusiveMaintenance}})
	state, outputs := transition(t, state, ExclusiveUnitCompleted{RequestID: "one"})
	if state.Phase != LifecycleMaintenance || state.RunningExclusive == nil ||
		state.RunningExclusive.ID != "two" || !hasOutput[StartExclusive](outputs) {
		t.Fatalf("second exclusive did not start without reopening: state=%+v outputs=%#v", state, outputs)
	}
	state, outputs = transition(t, state, ExclusiveUnitCompleted{RequestID: "two"})
	if state.Phase != LifecycleReady || state.RunningExclusive != nil || !hasOutput[LifecycleChanged](outputs) {
		t.Fatalf("lifecycle did not reopen after queue emptied: state=%+v outputs=%#v", state, outputs)
	}
}

func TestBackgroundYieldsToForegroundAndExclusive(t *testing.T) {
	state := LifecycleState{Phase: LifecycleReady}
	state, outputs := transition(t, state, BackgroundUnitRequested{TaskID: "cleanup"})
	if state.BackgroundUnit != "cleanup" || !hasOutput[RunBackgroundUnit](outputs) {
		t.Fatalf("background unit did not start: state=%+v outputs=%#v", state, outputs)
	}
	state, outputs = transition(t, state, NormalOperationRequested{ID: "foreground"})
	if len(state.ActiveOperations) != 1 || !hasOutput[AdmitNormalOperation](outputs) {
		t.Fatalf("foreground operation was not admitted while background unit runs: state=%+v outputs=%#v", state, outputs)
	}
	state, _ = transition(t, state, ExclusiveRequested{Request: ExclusiveRequest{ID: "maintenance", Kind: ExclusiveMaintenance}})
	state, outputs = transition(t, state, BackgroundUnitCompleted{TaskID: "cleanup"})
	if state.RunningExclusive != nil || hasOutput[StartExclusive](outputs) {
		t.Fatalf("exclusive started before foreground operation completed: state=%+v outputs=%#v", state, outputs)
	}
	state, outputs = transition(t, state, NormalOperationCompleted{ID: "foreground"})
	if state.RunningExclusive == nil || !hasOutput[StartExclusive](outputs) {
		t.Fatalf("exclusive did not start after foreground operation: state=%+v outputs=%#v", state, outputs)
	}
}

func TestStopInterruptsExclusiveAtSafeBoundary(t *testing.T) {
	state := LifecycleState{Phase: LifecycleReady}
	state, _ = transition(t, state, ExclusiveRequested{Request: ExclusiveRequest{ID: "maintenance", Kind: ExclusiveMaintenance}})
	state, outputs := transition(t, state, ExclusiveRequested{Request: ExclusiveRequest{ID: "stop", Kind: ExclusiveStop}})
	if !state.StopRequested || !hasOutput[InterruptExclusiveAtBoundary](outputs) {
		t.Fatalf("stop did not request boundary interruption: state=%+v outputs=%#v", state, outputs)
	}
	state, outputs = transition(t, state, ExclusiveUnitCompleted{RequestID: "maintenance", More: true})
	if state.RunningExclusive == nil || state.RunningExclusive.ID != "stop" ||
		!hasOutput[CompleteExclusive](outputs) || !hasOutput[StartExclusive](outputs) {
		t.Fatalf("stop did not take priority after safe boundary: state=%+v outputs=%#v", state, outputs)
	}
	state, outputs = transition(t, state, ExclusiveUnitCompleted{RequestID: "stop"})
	if !hasOutput[ExitProcess](outputs) || state.RunningExclusive != nil {
		t.Fatalf("stop completion did not request process exit: state=%+v outputs=%#v", state, outputs)
	}
}

func TestStopDuringInitialization(t *testing.T) {
	state := LifecycleState{Phase: LifecycleInitializing, InitializationStarted: true}
	state, outputs := transition(t, state, ExclusiveRequested{Request: ExclusiveRequest{ID: "stop", Kind: ExclusiveStop}})
	if !state.StopRequested || state.Phase != LifecycleInitializing || !hasOutput[PauseBackground](outputs) {
		t.Fatalf("stop changed initialization before safe boundary: state=%+v outputs=%#v", state, outputs)
	}
	state, outputs = transition(t, state, InitializationUnitCompleted{More: true})
	if state.Phase != LifecycleMaintenance || state.RunningExclusive == nil ||
		state.RunningExclusive.ID != "stop" || !hasOutput[StartExclusive](outputs) {
		t.Fatalf("stop did not start after initialization unit: state=%+v outputs=%#v", state, outputs)
	}
}

func TestFailedLifecycleAdmissionAndStop(t *testing.T) {
	state := LifecycleState{Phase: LifecycleFailed}
	state, outputs := transition(t, state, NormalOperationRequested{ID: "op"})
	foundEIO := false
	for _, output := range outputs {
		if rejected, ok := output.(RejectNormalOperation); ok && rejected.Errno == syscall.EIO {
			foundEIO = true
		}
	}
	if !foundEIO {
		t.Fatalf("failed lifecycle must reject normal operations with EIO: %#v", outputs)
	}
	state, outputs = transition(t, state, ExclusiveRequested{Request: ExclusiveRequest{ID: "stop", Kind: ExclusiveStop}})
	if state.RunningExclusive == nil || state.RunningExclusive.ID != "stop" || !hasOutput[StartExclusive](outputs) {
		t.Fatalf("stop was not accepted in failed lifecycle: state=%+v outputs=%#v", state, outputs)
	}
}

func TestInvalidEventsDoNotMutateInputState(t *testing.T) {
	state := LifecycleState{Phase: LifecycleReady, ActiveOperations: []string{"active"}}
	next, _, err := (LifecycleMachine{}).Transition(state, NormalOperationCompleted{ID: "unknown"})
	if err == nil {
		t.Fatal("expected unknown completion to fail")
	}
	if len(state.ActiveOperations) != 1 || state.ActiveOperations[0] != "active" ||
		len(next.ActiveOperations) != 1 || next.ActiveOperations[0] != "active" {
		t.Fatalf("expected rejected transition to preserve both independent states: input=%+v next=%+v", state, next)
	}
	next.ActiveOperations[0] = "changed"
	if state.ActiveOperations[0] != "active" {
		t.Fatalf("returned state aliases input state: input=%+v next=%+v", state, next)
	}
}
