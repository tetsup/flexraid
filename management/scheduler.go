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

// InitializationTask advances one safe initialization unit.
type InitializationTask interface {
	Next(context.Context) (done bool, err error)
}

// InitializationTaskFunc adapts a function to InitializationTask.
type InitializationTaskFunc func(context.Context) (done bool, err error)

func (run InitializationTaskFunc) Next(ctx context.Context) (bool, error) {
	return run(ctx)
}

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

// ForegroundOperation is one accepted foreground execution unit.
type ForegroundOperation func(context.Context) error

// ExclusiveTask advances one unit of an exclusive maintenance request.
type ExclusiveTask interface {
	Next(context.Context) (done bool, err error)
}

// ExclusiveTaskFunc adapts a function to ExclusiveTask.
type ExclusiveTaskFunc func(context.Context) (done bool, err error)

func (run ExclusiveTaskFunc) Next(ctx context.Context) (bool, error) {
	return run(ctx)
}

// ForegroundHandle represents an accepted foreground request.
type ForegroundHandle struct {
	done chan struct{}
	err  error
}

// Wait waits for the operation to complete. A wait-context timeout does not
// cancel the operation; cancellation is controlled by the submitted context.
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

// ExclusiveHandle represents an accepted exclusive task.
type ExclusiveHandle struct {
	done chan struct{}
	err  error
}

// Wait waits for the exclusive task to complete.
func (handle *ExclusiveHandle) Wait(ctx context.Context) error {
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

type exclusiveRequest struct {
	request  ExclusiveRequest
	ctx      context.Context
	task     ExclusiveTask
	handle   *ExclusiveHandle
	accepted chan error
}

type foregroundCompleted struct {
	id      string
	handle  *ForegroundHandle
	err     error
	release func()
}

type initializationCompleted struct {
	done    bool
	err     error
	release func()
}

type backgroundCompleted struct {
	done    bool
	err     error
	release func()
}

type exclusiveCompleted struct {
	id      string
	done    bool
	err     error
	release func()
}

type stopScheduler struct {
	accepted chan error
}

type stopCompleted struct {
	release func()
}

// Scheduler serializes all Management execution units through one shared
// execution lock. Foreground operations, Background units, Exclusive units,
// and Stop never execute concurrently.
type Scheduler struct {
	initialization     InitializationTask
	background         BackgroundTask
	execution          sync.Mutex
	commands           chan any
	done               chan struct{}
	ready              chan struct{}
	initializationDone chan struct{}
	readyOnce          sync.Once
	initializationOnce sync.Once

	phaseMu sync.RWMutex
	phase   LifecyclePhase

	mu                  sync.Mutex
	started             bool
	stopRequested       bool
	backgroundError     error
	initializationError error
}

func NewScheduler(background BackgroundTask) *Scheduler {
	return NewSchedulerWithInitialization(nil, background)
}

// NewSchedulerWithInitialization configures initialization and background
// tasks. Initialization runs in safe units before the scheduler enters Ready.
func NewSchedulerWithInitialization(initialization InitializationTask, background BackgroundTask) *Scheduler {
	return &Scheduler{
		initialization: initialization,
		background:     background,
		commands:           make(chan any, 128),
		done:               make(chan struct{}),
		ready:              make(chan struct{}),
		initializationDone: make(chan struct{}),
	}
}

// Start starts the scheduler coordinator.
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

// WaitReady waits until initialization has completed successfully or failed.
func (scheduler *Scheduler) WaitReady(ctx context.Context) error {
	if ctx == nil {
		return errors.New("wait context is required")
	}
	select {
	case <-scheduler.ready:
		return nil
	case <-scheduler.initializationDone:
		if err := scheduler.InitializationError(); err != nil {
			return err
		}
		return errors.New("initialization completed without entering Ready")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Phase returns the latest lifecycle phase published by the scheduler.
func (scheduler *Scheduler) Phase() LifecyclePhase {
	scheduler.phaseMu.RLock()
	defer scheduler.phaseMu.RUnlock()
	return scheduler.phase
}

// SubmitForeground queues a foreground operation. Accepted requests execute
// one at a time in FIFO order and take priority over the next Background unit.
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
	if err := scheduler.checkAccepting(); err != nil {
		return nil, err
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

// SubmitExclusive queues a maintenance task. It closes normal admission,
// waits for the current safe unit and accepted foreground work to finish, and
// then executes each exclusive unit with the same shared execution lock.
func (scheduler *Scheduler) SubmitExclusive(
	ctx context.Context,
	id string,
	task ExclusiveTask,
) (*ExclusiveHandle, error) {
	if ctx == nil {
		return nil, errors.New("operation context is required")
	}
	if id == "" {
		return nil, errors.New("exclusive request ID is required")
	}
	if task == nil {
		return nil, errors.New("exclusive task is required")
	}
	if err := scheduler.checkAccepting(); err != nil {
		return nil, err
	}

	handle := &ExclusiveHandle{done: make(chan struct{})}
	request := &exclusiveRequest{
		request:  ExclusiveRequest{ID: id, Kind: ExclusiveMaintenance},
		ctx:      ctx,
		task:     task,
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

func (scheduler *Scheduler) checkAccepting() error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if !scheduler.started {
		return ErrSchedulerNotStarted
	}
	if scheduler.stopRequested {
		return ErrSchedulerStopping
	}
	return nil
}

// Stop closes admission, waits for accepted foreground work and the current
// safe unit, and then exits through the highest-priority Exclusive request.
// A context timeout only stops this caller waiting; shutdown continues.
func (scheduler *Scheduler) Stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("stop context is required")
	}
	scheduler.mu.Lock()
	if !scheduler.started {
		scheduler.mu.Unlock()
		return ErrSchedulerNotStarted
	}
	firstRequest := !scheduler.stopRequested
	scheduler.stopRequested = true
	scheduler.mu.Unlock()

	if firstRequest {
		request := stopScheduler{accepted: make(chan error, 1)}
		select {
		case scheduler.commands <- request:
		case <-scheduler.done:
		}
		select {
		case err := <-request.accepted:
			if err != nil && !errors.Is(err, ErrSchedulerStopping) {
				return err
			}
		case <-scheduler.done:
		}
	}

	select {
	case <-scheduler.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// BackgroundError returns the error that stopped Background processing.
func (scheduler *Scheduler) BackgroundError() error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return scheduler.backgroundError
}

// InitializationError returns the error that prevented the scheduler from
// entering Ready.
func (scheduler *Scheduler) InitializationError() error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return scheduler.initializationError
}

func (scheduler *Scheduler) setInitializationError(err error) {
	scheduler.mu.Lock()
	scheduler.initializationError = err
	scheduler.mu.Unlock()
}

func (scheduler *Scheduler) setBackgroundError(err error) {
	scheduler.mu.Lock()
	scheduler.backgroundError = err
	scheduler.mu.Unlock()
}

func (scheduler *Scheduler) run() {
	machine := LifecycleMachine{}
	state := LifecycleState{Phase: LifecycleInitializing}
	foregroundQueue := make([]*foregroundRequest, 0)
	exclusiveTasks := make(map[string]*exclusiveRequest)
	active := false
	backgroundMore := scheduler.background != nil
	initializationStarted := false
	stopping := false
	finished := false

	publishPhase := func() {
		scheduler.phaseMu.Lock()
		scheduler.phase = state.Phase
		scheduler.phaseMu.Unlock()
	}
	completeInitialization := func() {
		scheduler.initializationOnce.Do(func() { close(scheduler.initializationDone) })
		if state.Phase == LifecycleReady {
			scheduler.readyOnce.Do(func() { close(scheduler.ready) })
		}
	}
	transition := func(input LifecycleInput) ([]LifecycleOutput, error) {
		next, outputs, err := machine.Transition(state, input)
		if err != nil {
			return nil, err
		}
		state = next
		publishPhase()
		return outputs, nil
	}
	publishPhase()
	finishForeground := func(request *ForegroundHandle, err error) {
		request.err = err
		close(request.done)
	}
	finishExclusive := func(request *exclusiveRequest, err error) {
		if request == nil {
			return
		}
		request.handle.err = err
		close(request.handle.done)
		delete(exclusiveTasks, request.request.ID)
	}

	var dispatch func()
	dispatch = func() {
		if active || finished {
			return
		}

		if state.Phase == LifecycleInitializing {
			if !initializationStarted {
				if _, err := transition(InitializationStarted{}); err != nil {
					_, _ = transition(InitializationFailed{Reason: err.Error()})
					return
				}
				initializationStarted = true
			}
			if scheduler.initialization == nil {
				_, err := transition(InitializationUnitCompleted{})
				if err != nil {
					scheduler.setInitializationError(err)
				}
				completeInitialization()
				dispatch()
				return
			}
			active = true
			go func() {
				done, err, release := runInitializationUnit(&scheduler.execution, scheduler.initialization)
				scheduler.commands <- initializationCompleted{done: done, err: err, release: release}
			}()
			return
		}

		if state.RunningExclusive != nil {
			running := *state.RunningExclusive
			if running.Kind == ExclusiveStop {
				active = true
				go func() {
					scheduler.execution.Lock()
					scheduler.commands <- stopCompleted{release: scheduler.execution.Unlock}
				}()
				return
			}
			request := exclusiveTasks[running.ID]
			if request == nil {
				_, err := transition(ExclusiveFailed{RequestID: running.ID, Reason: "exclusive task is missing"})
				if err != nil {
					finished = true
					close(scheduler.done)
					return
				}
				dispatch()
				return
			}
			active = true
			go func() {
				done, err, release := runExclusiveUnit(&scheduler.execution, request.ctx, request.task)
				scheduler.commands <- exclusiveCompleted{id: running.ID, done: done, err: err, release: release}
			}()
			return
		}

		if len(foregroundQueue) > 0 {
			request := foregroundQueue[0]
			foregroundQueue = foregroundQueue[1:]
			active = true
			go func() {
				err, release := runForegroundOperation(&scheduler.execution, request.ctx, request.run)
				scheduler.commands <- foregroundCompleted{id: request.id, handle: request.handle, err: err, release: release}
			}()
			return
		}

		if !stopping && backgroundMore && state.Phase == LifecycleReady &&
			len(state.ActiveOperations) == 0 && state.BackgroundUnit == "" &&
			len(scheduler.commands) == 0 {
			if _, err := transition(BackgroundUnitRequested{TaskID: "background"}); err != nil {
				scheduler.setBackgroundError(err)
				backgroundMore = false
				return
			}
			active = true
			go func() {
				done, err, release := runBackgroundUnit(&scheduler.execution, scheduler.background)
				scheduler.commands <- backgroundCompleted{done: done, err: err, release: release}
			}()
			return
		}

		if stopping && state.RunningExclusive == nil &&
			len(state.ActiveOperations) == 0 && state.BackgroundUnit == "" {
			finished = true
			close(scheduler.done)
		}
	}

	dispatch()
	for !finished {
		command := <-scheduler.commands
		switch event := command.(type) {
		case *foregroundRequest:
			if stopping {
				event.accepted <- ErrSchedulerStopping
				continue
			}
			outputs, err := transition(NormalOperationRequested{ID: event.id})
			if err != nil {
				event.accepted <- err
				continue
			}
			if err = rejectedNormalOperation(outputs); err != nil {
				event.accepted <- err
				continue
			}
			foregroundQueue = append(foregroundQueue, event)
			event.accepted <- nil

		case *exclusiveRequest:
			if stopping {
				event.accepted <- ErrSchedulerStopping
				continue
			}
			_, err := transition(ExclusiveRequested{Request: event.request})
			if err != nil {
				event.accepted <- err
				continue
			}
			exclusiveTasks[event.request.ID] = event
			event.accepted <- nil

		case initializationCompleted:
			active = false
			if event.err != nil {
				_, err := transition(InitializationFailed{Reason: event.err.Error()})
				if err != nil {
					event.err = errors.Join(event.err, err)
				}
				scheduler.setInitializationError(event.err)
			} else {
				_, err := transition(InitializationUnitCompleted{More: !event.done})
				if err != nil {
					scheduler.setInitializationError(err)
				}
			}
			event.release()
			if state.Phase != LifecycleInitializing {
				completeInitialization()
			}

		case foregroundCompleted:
			active = false
			_, err := transition(NormalOperationCompleted{ID: event.id})
			if err != nil {
				event.err = errors.Join(event.err, err)
			}
			event.release()
			finishForeground(event.handle, event.err)

		case backgroundCompleted:
			active = false
			_, err := transition(BackgroundUnitCompleted{TaskID: "background"})
			if err != nil {
				event.err = errors.Join(event.err, err)
			}
			backgroundMore = !event.done && event.err == nil
			if event.err != nil {
				scheduler.setBackgroundError(event.err)
			}
			event.release()

		case exclusiveCompleted:
			active = false
			request := exclusiveTasks[event.id]
			var completionErr error
			complete := false
			if event.err != nil {
				_, err := transition(ExclusiveFailed{RequestID: event.id, Reason: event.err.Error()})
				if err != nil {
					event.err = errors.Join(event.err, err)
				}
				completionErr = event.err
				complete = true
			} else {
				outputs, err := transition(ExclusiveUnitCompleted{RequestID: event.id, More: !event.done})
				if err != nil {
					completionErr = err
					complete = true
				} else if containsCompleteExclusive(outputs, event.id) {
					if stopping {
						completionErr = ErrSchedulerStopping
					}
					complete = true
				}
			}
			event.release()
			if complete {
				finishExclusive(request, completionErr)
			}

		case stopScheduler:
			if stopping {
				event.accepted <- nil
				continue
			}
			stopping = true
			// Stop has priority over queued Exclusive requests. Requests that
			// have not started cannot be completed after the process exits.
			runningID := ""
			if state.RunningExclusive != nil {
				runningID = state.RunningExclusive.ID
			}
			for id, request := range exclusiveTasks {
				if id != runningID {
					finishExclusive(request, ErrSchedulerStopping)
				}
			}
			_, err := transition(ExclusiveRequested{Request: ExclusiveRequest{ID: "scheduler-stop", Kind: ExclusiveStop}})
			event.accepted <- err

		case stopCompleted:
			active = false
			_, err := transition(ExclusiveUnitCompleted{RequestID: "scheduler-stop"})
			if err != nil {
				// The stop request is terminal even if the reducer reports an
				// inconsistent state; do not leave the process half-stopped.
				scheduler.setBackgroundError(fmt.Errorf("stop lifecycle transition: %w", err))
			}
			event.release()
			finished = true
			close(scheduler.done)
		}
		dispatch()
	}
}

func rejectedNormalOperation(outputs []LifecycleOutput) error {
	for _, output := range outputs {
		if rejected, ok := output.(RejectNormalOperation); ok {
			return rejected.Errno
		}
	}
	return nil
}

func containsCompleteExclusive(outputs []LifecycleOutput, id string) bool {
	for _, output := range outputs {
		if completed, ok := output.(CompleteExclusive); ok && completed.RequestID == id {
			return true
		}
	}
	return false
}

func runForegroundOperation(lock *sync.Mutex, ctx context.Context, run ForegroundOperation) (err error, release func()) {
	lock.Lock()
	release = lock.Unlock
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("foreground operation panicked: %v", recovered)
		}
	}()
	if err := ctx.Err(); err != nil {
		return err, release
	}
	return run(ctx), release
}

func runInitializationUnit(lock *sync.Mutex, task InitializationTask) (done bool, err error, release func()) {
	lock.Lock()
	release = lock.Unlock
	defer func() {
		if recovered := recover(); recovered != nil {
			done = true
			err = fmt.Errorf("initialization unit panicked: %v", recovered)
		}
	}()
	done, err = task.Next(context.Background())
	return done, err, release
}

func runBackgroundUnit(lock *sync.Mutex, task BackgroundTask) (done bool, err error, release func()) {
	lock.Lock()
	release = lock.Unlock
	defer func() {
		if recovered := recover(); recovered != nil {
			done = true
			err = fmt.Errorf("background unit panicked: %v", recovered)
		}
	}()
	done, err = task.Next(context.Background())
	return done, err, release
}

func runExclusiveUnit(lock *sync.Mutex, ctx context.Context, task ExclusiveTask) (done bool, err error, release func()) {
	lock.Lock()
	release = lock.Unlock
	defer func() {
		if recovered := recover(); recovered != nil {
			done = true
			err = fmt.Errorf("exclusive unit panicked: %v", recovered)
		}
	}()
	if err := ctx.Err(); err != nil {
		return true, err, release
	}
	done, err = task.Next(ctx)
	return done, err, release
}
