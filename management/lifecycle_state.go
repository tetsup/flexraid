package management

import (
	"fmt"
	"syscall"
)

// LifecyclePhase is the externally meaningful phase of the filesystem lifecycle.
type LifecyclePhase uint8

const (
	LifecycleInitializing LifecyclePhase = iota
	LifecycleReady
	LifecycleDraining
	LifecycleMaintenance
	LifecycleFailed
)

// ExclusiveKind identifies a task that requires exclusive execution rights.
type ExclusiveKind uint8

const (
	ExclusiveMaintenance ExclusiveKind = iota
	ExclusiveStop
)

// ExclusiveRequest is a queued request for exclusive execution.
type ExclusiveRequest struct {
	ID   string
	Kind ExclusiveKind
}

// LifecycleState contains every fact needed to make deterministic lifecycle
// transition decisions. The scheduler must treat the returned state as the
// authoritative snapshot and must not mutate its slices or pointers.
type LifecycleState struct {
	Phase                 LifecyclePhase
	ActiveOperations      []string
	ExclusiveQueue        []ExclusiveRequest
	RunningExclusive      *ExclusiveRequest
	StopRequested         bool
	BackgroundUnit        string
	InitializationStarted bool
}

// LifecycleMachine is a pure lifecycle state reducer.
type LifecycleMachine struct{}

type LifecycleInput interface {
	isLifecycleInput()
}

type LifecycleOutput interface {
	isLifecycleOutput()
}

type InitializationStarted struct{}
type InitializationUnitCompleted struct {
	More bool
}
type InitializationFailed struct {
	Reason string
}

type NormalOperationRequested struct {
	ID string
}
type NormalOperationCompleted struct {
	ID string
}

type BackgroundUnitRequested struct {
	TaskID string
}
type BackgroundUnitCompleted struct {
	TaskID string
}

type ExclusiveRequested struct {
	Request ExclusiveRequest
}
type ExclusiveUnitCompleted struct {
	RequestID string
	More      bool
}
type ExclusiveFailed struct {
	RequestID string
	Reason    string
}

type FatalError struct {
	Reason string
}

type AdmitNormalOperation struct {
	ID string
}
type RejectNormalOperation struct {
	ID    string
	Errno syscall.Errno
}
type RunInitializationUnit struct{}
type RunBackgroundUnit struct {
	TaskID string
}
type PauseBackground struct{}
type StartExclusive struct {
	Request ExclusiveRequest
}
type RunExclusiveUnit struct {
	Request ExclusiveRequest
}
type InterruptExclusiveAtBoundary struct {
	RequestID string
}
type CompleteExclusive struct {
	RequestID   string
	Interrupted bool
	Reason      string
}
type LifecycleChanged struct {
	From LifecyclePhase
	To   LifecyclePhase
}
type LifecycleFailed struct {
	Reason string
}

func (InitializationStarted) isLifecycleInput()       {}
func (InitializationUnitCompleted) isLifecycleInput()  {}
func (InitializationFailed) isLifecycleInput()         {}
func (NormalOperationRequested) isLifecycleInput()     {}
func (NormalOperationCompleted) isLifecycleInput()     {}
func (BackgroundUnitRequested) isLifecycleInput()      {}
func (BackgroundUnitCompleted) isLifecycleInput()      {}
func (ExclusiveRequested) isLifecycleInput()           {}
func (ExclusiveUnitCompleted) isLifecycleInput()       {}
func (ExclusiveFailed) isLifecycleInput()              {}
func (FatalError) isLifecycleInput()                   {}

func (AdmitNormalOperation) isLifecycleOutput()        {}
func (RejectNormalOperation) isLifecycleOutput()       {}
func (RunInitializationUnit) isLifecycleOutput()       {}
func (RunBackgroundUnit) isLifecycleOutput()            {}
func (PauseBackground) isLifecycleOutput()              {}
func (StartExclusive) isLifecycleOutput()               {}
func (RunExclusiveUnit) isLifecycleOutput()             {}
func (InterruptExclusiveAtBoundary) isLifecycleOutput() {}
func (CompleteExclusive) isLifecycleOutput()            {}
func (LifecycleChanged) isLifecycleOutput()             {}
func (LifecycleFailed) isLifecycleOutput()              {}

var _ StateMachine[LifecycleState, LifecycleInput, LifecycleOutput] = LifecycleMachine{}

// StateMachine transforms a state and an input event into a new state and
// declarative outputs. Implementations must not perform I/O or side effects.
type StateMachine[S, I, O any] interface {
	Transition(state S, input I) (S, []O, error)
}

func (LifecycleMachine) Transition(state LifecycleState, input LifecycleInput) (LifecycleState, []LifecycleOutput, error) {
	next := cloneLifecycleState(state)
	var outputs []LifecycleOutput

	switch event := input.(type) {
	case InitializationStarted:
		if state.Phase != LifecycleInitializing || state.InitializationStarted {
			return state, nil, fmt.Errorf("initialization cannot start in phase %d", state.Phase)
		}
		next.InitializationStarted = true
		outputs = append(outputs, RunInitializationUnit{})

	case InitializationUnitCompleted:
		if state.Phase != LifecycleInitializing || !state.InitializationStarted {
			return state, nil, fmt.Errorf("initialization unit completed while initialization is not running")
		}
		if state.StopRequested {
			next.Phase = LifecycleMaintenance
			outputs = append(outputs, LifecycleChanged{From: state.Phase, To: next.Phase})
			outputs = append(outputs, CompleteExclusive{RequestID: stopRequestID(next), Interrupted: true, Reason: "initialization interrupted for stop"})
			outputs = append(outputs, startNextExclusive(&next)...)
			break
		}
		if event.More {
			outputs = append(outputs, RunInitializationUnit{})
			break
		}
		next.InitializationStarted = false
		if len(next.ExclusiveQueue) > 0 {
			next.Phase = LifecycleDraining
			outputs = append(outputs, LifecycleChanged{From: state.Phase, To: next.Phase})
			outputs = append(outputs, PauseBackground{})
			outputs = append(outputs, startNextExclusive(&next)...)
		} else {
			next.Phase = LifecycleReady
			outputs = append(outputs, LifecycleChanged{From: state.Phase, To: next.Phase})
		}

	case InitializationFailed:
		if state.Phase != LifecycleInitializing {
			return state, nil, fmt.Errorf("initialization failed in phase %d", state.Phase)
		}
		next.Phase = LifecycleFailed
		next.InitializationStarted = false
		next.ExclusiveQueue = nil
		outputs = append(outputs, LifecycleChanged{From: state.Phase, To: next.Phase}, LifecycleFailed{Reason: event.Reason})

	case NormalOperationRequested:
		if event.ID == "" {
			return state, nil, fmt.Errorf("normal operation ID is required")
		}
		if state.Phase != LifecycleReady {
			errno := syscall.EBUSY
			if state.Phase == LifecycleFailed {
				errno = syscall.EIO
			}
			outputs = append(outputs, RejectNormalOperation{ID: event.ID, Errno: errno})
			break
		}
		if containsString(state.ActiveOperations, event.ID) {
			return state, nil, fmt.Errorf("normal operation %q is already active", event.ID)
		}
		next.ActiveOperations = append(next.ActiveOperations, event.ID)
		outputs = append(outputs, AdmitNormalOperation{ID: event.ID})

	case NormalOperationCompleted:
		var found bool
		next.ActiveOperations, found = removeString(next.ActiveOperations, event.ID)
		if !found {
			return state, nil, fmt.Errorf("normal operation %q is not active", event.ID)
		}
		outputs = append(outputs, startNextExclusive(&next)...)

	case BackgroundUnitRequested:
		if event.TaskID == "" {
			return state, nil, fmt.Errorf("background task ID is required")
		}
		if state.Phase != LifecycleReady || len(state.ActiveOperations) != 0 || state.BackgroundUnit != "" {
			break
		}
		next.BackgroundUnit = event.TaskID
		outputs = append(outputs, RunBackgroundUnit{TaskID: event.TaskID})

	case BackgroundUnitCompleted:
		if state.BackgroundUnit == "" || state.BackgroundUnit != event.TaskID {
			return state, nil, fmt.Errorf("background task %q is not running", event.TaskID)
		}
		next.BackgroundUnit = ""
		outputs = append(outputs, startNextExclusive(&next)...)

	case ExclusiveRequested:
		request := event.Request
		if request.ID == "" {
			return state, nil, fmt.Errorf("exclusive request ID is required")
		}
		if request.Kind != ExclusiveMaintenance && request.Kind != ExclusiveStop {
			return state, nil, fmt.Errorf("unsupported exclusive kind %d", request.Kind)
		}
		if state.Phase == LifecycleFailed {
			return state, nil, fmt.Errorf("exclusive request rejected in failed lifecycle")
		}
		if containsRequest(next.ExclusiveQueue, request.ID) ||
			(next.RunningExclusive != nil && next.RunningExclusive.ID == request.ID) {
			return state, nil, fmt.Errorf("exclusive request %q already exists", request.ID)
		}
		if request.Kind == ExclusiveStop {
			next.StopRequested = true
			next.ExclusiveQueue = append([]ExclusiveRequest{request}, next.ExclusiveQueue...)
			if state.Phase == LifecycleInitializing {
				outputs = append(outputs, PauseBackground{})
				break
			}
			if state.RunningExclusive != nil {
				outputs = append(outputs, InterruptExclusiveAtBoundary{RequestID: state.RunningExclusive.ID})
				break
			}
		} else {
			next.ExclusiveQueue = append(next.ExclusiveQueue, request)
		}
		if state.Phase == LifecycleReady {
			next.Phase = LifecycleDraining
			outputs = append(outputs, LifecycleChanged{From: state.Phase, To: next.Phase}, PauseBackground{})
		}
		outputs = append(outputs, startNextExclusive(&next)...)

	case ExclusiveUnitCompleted:
		if state.RunningExclusive == nil || state.RunningExclusive.ID != event.RequestID {
			return state, nil, fmt.Errorf("exclusive request %q is not running", event.RequestID)
		}
		request := *state.RunningExclusive
		if state.StopRequested && request.Kind != ExclusiveStop {
			next.RunningExclusive = nil
			outputs = append(outputs, CompleteExclusive{RequestID: request.ID, Interrupted: true, Reason: "preempted by stop"})
			outputs = append(outputs, startNextExclusive(&next)...)
			break
		}
		if event.More {
			outputs = append(outputs, RunExclusiveUnit{Request: request})
			break
		}
		next.RunningExclusive = nil
		outputs = append(outputs, CompleteExclusive{RequestID: request.ID})
		outputs = append(outputs, startNextExclusive(&next)...)

	case ExclusiveFailed:
		if state.RunningExclusive == nil || state.RunningExclusive.ID != event.RequestID {
			return state, nil, fmt.Errorf("exclusive request %q is not running", event.RequestID)
		}
		request := *state.RunningExclusive
		next.RunningExclusive = nil
		outputs = append(outputs, CompleteExclusive{RequestID: request.ID, Reason: event.Reason})
		outputs = append(outputs, startNextExclusive(&next)...)

	case FatalError:
		if state.Phase == LifecycleFailed {
			return state, nil, fmt.Errorf("lifecycle is already failed")
		}
		next.Phase = LifecycleFailed
		next.ActiveOperations = nil
		next.ExclusiveQueue = nil
		next.RunningExclusive = nil
		next.BackgroundUnit = ""
		next.StopRequested = false
		outputs = append(outputs, LifecycleChanged{From: state.Phase, To: next.Phase}, LifecycleFailed{Reason: event.Reason})

	default:
		return state, nil, fmt.Errorf("unsupported lifecycle input %T", input)
	}

	return next, outputs, nil
}

func startNextExclusive(state *LifecycleState) []LifecycleOutput {
	if state.Phase == LifecycleInitializing || state.Phase == LifecycleFailed ||
		state.RunningExclusive != nil || len(state.ActiveOperations) != 0 ||
		state.BackgroundUnit != "" || len(state.ExclusiveQueue) == 0 {
		return nil
	}
	request := state.ExclusiveQueue[0]
	state.ExclusiveQueue = state.ExclusiveQueue[1:]
	state.RunningExclusive = &request
	if state.Phase != LifecycleMaintenance {
		from := state.Phase
		state.Phase = LifecycleMaintenance
		return []LifecycleOutput{
			LifecycleChanged{From: from, To: state.Phase},
			StartExclusive{Request: request},
		}
	}
	return []LifecycleOutput{StartExclusive{Request: request}}
}

func stopRequestID(state LifecycleState) string {
	for _, request := range state.ExclusiveQueue {
		if request.Kind == ExclusiveStop {
			return request.ID
		}
	}
	return ""
}

func cloneLifecycleState(state LifecycleState) LifecycleState {
	state.ActiveOperations = append([]string(nil), state.ActiveOperations...)
	state.ExclusiveQueue = append([]ExclusiveRequest(nil), state.ExclusiveQueue...)
	if state.RunningExclusive != nil {
		request := *state.RunningExclusive
		state.RunningExclusive = &request
	}
	return state
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func removeString(values []string, value string) ([]string, bool) {
	for index, item := range values {
		if item == value {
			return append(values[:index], values[index+1:]...), true
		}
	}
	return values, false
}

func containsRequest(values []ExclusiveRequest, id string) bool {
	for _, request := range values {
		if request.ID == id {
			return true
		}
	}
	return false
}
