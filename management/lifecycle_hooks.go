package management

import (
	"context"
	"errors"
	"fmt"
)

// StateHooks contains side effects associated with entering and leaving a
// lifecycle phase. Hooks execute serially as part of a phase transition.
type StateHooks struct {
	OnEnter func(context.Context) error
	OnExit  func(context.Context) error
}

// StateHookRegistry maps lifecycle phases to their entry and exit hooks.
type StateHookRegistry map[LifecyclePhase]StateHooks

// LifecycleTransitionExecutor applies hooks around the pure lifecycle reducer.
// The reducer decides transitions; this executor owns transition side effects.
type LifecycleTransitionExecutor struct {
	Hooks StateHookRegistry
}

// Transition runs OnExit for the current phase and OnEnter for the target
// phase. A target phase is committed only when both hooks succeed. Hook
// failures move the lifecycle to Failed and return the original error.
func (executor LifecycleTransitionExecutor) Transition(
	ctx context.Context,
	machine LifecycleMachine,
	state LifecycleState,
	input LifecycleInput,
) (LifecycleState, []LifecycleOutput, error) {
	if ctx == nil {
		return cloneLifecycleState(state), nil, errors.New("lifecycle transition context is required")
	}

	next, outputs, err := machine.Transition(state, input)
	if err != nil {
		return cloneLifecycleState(state), nil, err
	}
	if state.Phase == next.Phase {
		return next, outputs, nil
	}

	target := next.Phase
	if exit := executor.Hooks[state.Phase].OnExit; exit != nil {
		if err := exit(ctx); err != nil {
			next.Phase = LifecycleFailed
			return next, lifecycleFailureOutputs(outputs, state.Phase, err),
				fmt.Errorf("exit lifecycle phase %d: %w", state.Phase, err)
		}
	}

	if enter := executor.Hooks[target].OnEnter; enter != nil {
		if err := enter(ctx); err != nil {
			rollbackErr := error(nil)
			if rollback := executor.Hooks[target].OnExit; rollback != nil {
				rollbackErr = rollback(ctx)
			}
			next.Phase = LifecycleFailed
			reason := errors.Join(err, rollbackErr)
			return next, lifecycleFailureOutputs(outputs, state.Phase, reason),
				fmt.Errorf("enter lifecycle phase %d: %w", target, reason)
		}
	}

	return next, outputs, nil
}

func lifecycleFailureOutputs(outputs []LifecycleOutput, from LifecyclePhase, err error) []LifecycleOutput {
	filtered := make([]LifecycleOutput, 0, len(outputs)+2)
	for _, output := range outputs {
		if _, ok := output.(LifecycleChanged); ok {
			continue
		}
		filtered = append(filtered, output)
	}
	return append(filtered,
		LifecycleChanged{From: from, To: LifecycleFailed},
		LifecycleFailureReported{Reason: err.Error()},
	)
}
