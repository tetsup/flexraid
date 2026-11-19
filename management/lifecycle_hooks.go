package management

import (
	"context"
	"errors"
	"fmt"
)

// StateHooks contains the side effects associated with entering and leaving a
// lifecycle phase. Hooks run serially as part of a lifecycle transition.
type StateHooks struct {
	OnEnter func(context.Context) error
	OnExit  func(context.Context) error
}

// StateHookRegistry maps lifecycle phases to their entry and exit hooks.
type StateHookRegistry map[LifecyclePhase]StateHooks

// LifecycleTransitionExecutor applies state hooks around the pure lifecycle
// reducer. The reducer remains responsible only for deciding the next state.
type LifecycleTransitionExecutor struct {
	Hooks StateHookRegistry
}

// Transition executes the hooks for a phase change before publishing it.
// Hook failures move the returned state to Failed so partially applied
// lifecycle side effects are never reported as a healthy phase.
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

	if exit := executor.Hooks[state.Phase].OnExit; exit != nil {
		if err := exit(ctx); err != nil {
			next.Phase = LifecycleFailed
			return next, append(outputs, LifecycleChanged{From: state.Phase, To: LifecycleFailed},
					LifecycleFailureReported{Reason: err.Error()}),
				fmt.Errorf("exit lifecycle phase %d: %w", state.Phase, err)
		}
	}

	if enter := executor.Hooks[next.Phase].OnEnter; enter != nil {
		if err := enter(ctx); err != nil {
			rollbackErr := error(nil)
			if rollback := executor.Hooks[next.Phase].OnExit; rollback != nil {
				rollbackErr = rollback(ctx)
			}
			next.Phase = LifecycleFailed
			reason := errors.Join(err, rollbackErr)
			return next, append(outputs, LifecycleChanged{From: state.Phase, To: LifecycleFailed},
					LifecycleFailureReported{Reason: reason.Error()}),
				fmt.Errorf("enter lifecycle phase %d: %w", next.Phase, reason)
		}
	}

	return next, outputs, nil
}

func lifecycleFailureOutputs(outputs []LifecycleOutput, from LifecyclePhase, err error) []LifecycleOutput {
	filtered := make([]LifecycleOutput, 0, len(outputs)+2)
	for _, output := range outputs {
		if changed, ok := output.(LifecycleChanged); ok && changed.From == from {
			continue
		}
		filtered = append(filtered, output)
	}
	return append(filtered,
		LifecycleChanged{From: from, To: LifecycleFailed},
		LifecycleFailureReported{Reason: err.Error()},
	)
}
