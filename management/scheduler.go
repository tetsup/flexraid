package management

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrSchedulerAlreadyStarted = errors.New("scheduler already started")
	ErrSchedulerNotStarted     = errors.New("scheduler has not started")
	ErrSchedulerStopping       = errors.New("scheduler is stopping")
)

// BackgroundTask advances one reconstructible unit. Progress is memory-only;
// after restart the task must rescan current state and safely reconsider work.
type BackgroundTask interface {
	Next(context.Context) (done bool, err error)
}

// BackgroundTaskFunc adapts a function to BackgroundTask.
type BackgroundTaskFunc func(context.Context) (done bool, err error)

func (run BackgroundTaskFunc) Next(ctx context.Context) (bool, error) {
	return run(ctx)
}

// ForegroundOperation is one accepted foreground request.
type ForegroundOperation func(context.Context) error

// ForegroundHandle represents an accepted foreground request. Wait may be
// called more than once and returns the same completion result.
type ForegroundHandle struct {
	done chan struct{}
	err  error
}

// Wait waits for the operation to complete. A wait-context timeout does not
// cancel the operation; cancellation of the operation itself is controlled
// by the context supplied to SubmitForeground.
func (handle *ForegroundHandle) Wait(ctx context.Context) error {
	if ctx == nil {
		return errors.New("wait context is required")
	}
	select {
	case <-handle.done:
		return handle.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type foregroundRequest struct {
	id       string
	ctx      context.Context
	run      ForegroundOperation
	handle   *ForegroundHandle
	accepted chan error
}

type foregroundCompleted struct {
	id     string
	handle *ForegroundHandle
	err    error
}

type backgroundCompleted struct {
	more bool
	err  error
}

type stopScheduler struct{}

// Scheduler coordinates foreground operations with a cooperative background
// task. Foreground requests are admitted while a background unit is running,
// but are not started until that unit reaches its safe boundary.
type Scheduler struct {
	background BackgroundTask
	commands   chan any
	done       chan struct{}

	mu              sync.Mutex
	started         bool
	stopRequested   bool
	backgroundError error
}

func NewScheduler(background BackgroundTask) *Scheduler {
	return &Scheduler{
		background: background,
		commands:   make(chan any, 128),
		done:       make(chan struct{}),
	}
}

// Start starts the scheduler's long-running coordinator.
func (scheduler *Scheduler) Start() error {
	scheduler.mu.Lock()
	if scheduler.started {
		scheduler.mu.Unlock()
		return ErrSchedulerAlreadyStarted
	}
	scheduler.started = true
	scheduler.mu.Unlock()

	go scheduler.run()
	return nil
}

// SubmitForeground admits a foreground request without waiting for its
// execution. If a background unit is in progress, the request remains queued
// until that unit completes. The operation context is passed to the callback.
func (scheduler *Scheduler) SubmitForeground(
	ctx context.Context,
	id string,
	run ForegroundOperation,
) (*ForegroundHandle, error) {
	if ctx == nil {
		return nil, errors.New("operation context is required")
	}
	if id == "" {
		return nil, errors.New("foreground operation ID is required")
	}
	if run == nil {
		return nil, errors.New("foreground operation is required")
	}

	scheduler.mu.Lock()
	started := scheduler.started
	stopping := scheduler.stopRequested
	scheduler.mu.Unlock()
	if !started {
		return nil, ErrSchedulerNotStarted
	}
	if stopping {
		return nil, ErrSchedulerStopping
	}

	handle := &ForegroundHandle{done: make(chan struct{})}
	request := &foregroundRequest{
		id:       id,
		ctx:      ctx,
		run:      run,
		handle:   handle,
		accepted: make(chan error, 1),
	}
	select {
	case scheduler.commands <- request:
	case <-scheduler.done:
		return nil, ErrSchedulerStopping
	}

	select {
	case err := <-request.accepted:
		if err != nil {
			return nil, err
		}
		return handle, nil
	case <-scheduler.done:
		return nil, ErrSchedulerStopping
	}
}

// Stop stops accepting requests and waits for all accepted foreground
// operations and the currently running background unit to reach completion.
// A context timeout only stops the caller waiting; shutdown continues.
func (scheduler *Scheduler) Stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("stop context is required")
	}
	scheduler.mu.Lock()
	if !scheduler.started {
		scheduler.mu.Unlock()
		return ErrSchedulerNotStarted
	}
	scheduler.stopRequested = true
	scheduler.mu.Unlock()

	select {
	case scheduler.commands <- stopScheduler{}:
	case <-scheduler.done:
	}
	select {
	case <-scheduler.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// BackgroundError returns the error that stopped background processing, if
// any. Foreground processing remains available after a background task error.
func (scheduler *Scheduler) BackgroundError() error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return scheduler.backgroundError
}

func (scheduler *Scheduler) run() {
	state := LifecycleState{Phase: LifecycleReady}
	var foregroundQueue []*foregroundRequest
	activeForeground := 0
	backgroundRunning := false
	backgroundMore := scheduler.background != nil
	stopping := false
	finished := false
	machine := LifecycleMachine{}

	transition := func(input LifecycleInput) error {
		next, _, err := machine.Transition(state, input)
		if err != nil {
			return err
		}
		state = next
		return nil
	}

	var dispatch func()
	dispatch = func() {
		if !backgroundRunning {
			for len(foregroundQueue) > 0 {
				request := foregroundQueue[0]
				foregroundQueue = foregroundQueue[1:]
				activeForeground++
				go func(request *foregroundRequest) {
					err := runForegroundOperation(request.ctx, request.run)
					scheduler.commands <- foregroundCompleted{
						id:     request.id,
						handle: request.handle,
						err:    err,
					}
				}(request)
			}
		}

		if !backgroundRunning && activeForeground == 0 &&
			len(foregroundQueue) == 0 && !stopping && backgroundMore {
			if err := transition(BackgroundUnitRequested{TaskID: "background"}); err != nil {
				scheduler.setBackgroundError(err)
				backgroundMore = false
			} else {
				backgroundRunning = true
				go func() {
					done, err := runBackgroundUnit(scheduler.background)
					scheduler.commands <- backgroundCompleted{more: !done, err: err}
				}()
			}
		}

		if stopping && !backgroundRunning && activeForeground == 0 &&
			len(foregroundQueue) == 0 {
			close(scheduler.done)
			finished = true
		}
	}

	dispatch()
	if finished {
		return
	}
	for {
		select {
		case command := <-scheduler.commands:
			switch event := command.(type) {
			case *foregroundRequest:
				if stopping {
					event.accepted <- ErrSchedulerStopping
					continue
				}
				if err := transition(NormalOperationRequested{ID: event.id}); err != nil {
					event.accepted <- err
					continue
				}
				foregroundQueue = append(foregroundQueue, event)
				event.accepted <- nil

			case foregroundCompleted:
				activeForeground--
				if err := transition(NormalOperationCompleted{ID: event.id}); err != nil {
					event.err = errors.Join(event.err, err)
				}
				event.handle.err = event.err
				close(event.handle.done)

			case backgroundCompleted:
				backgroundRunning = false
				if err := transition(BackgroundUnitCompleted{TaskID: "background"}); err != nil {
					event.err = errors.Join(event.err, err)
				}
				backgroundMore = event.more && event.err == nil
				if event.err != nil {
					scheduler.setBackgroundError(event.err)
				}

			case stopScheduler:
				stopping = true
			}
			dispatch()
			if finished {
				return
			}
		}
	}
}

func (scheduler *Scheduler) setBackgroundError(err error) {
	scheduler.mu.Lock()
	scheduler.backgroundError = err
	scheduler.mu.Unlock()
}

func runForegroundOperation(ctx context.Context, run ForegroundOperation) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("foreground operation panicked: %v", recovered)
		}
	}()
	return run(ctx)
}

func runBackgroundUnit(task BackgroundTask) (done bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			done = true
			err = fmt.Errorf("background unit panicked: %v", recovered)
		}
	}()
	return task.Next(context.Background())
}
