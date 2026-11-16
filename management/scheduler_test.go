package management

import (
	"context"
	"errors"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func waitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func stopScheduler(t *testing.T, scheduler *Scheduler) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := scheduler.Stop(ctx); err != nil {
		t.Fatalf("scheduler did not stop: %v", err)
	}
}

func TestSchedulerRunsInitializationUnitsBeforeReady(t *testing.T) {
	initializationStarted := make(chan struct{})
	releaseInitialization := make(chan struct{})
	var units atomic.Int32
	scheduler := NewSchedulerWithInitialization(
		InitializationTaskFunc(func(context.Context) (bool, error) {
			switch units.Add(1) {
			case 1:
				close(initializationStarted)
				<-releaseInitialization
				return false, nil
			case 2:
				return true, nil
			default:
				t.Fatal("initialization ran more than two units")
				return true, nil
			}
		}),
		nil,
	)
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, initializationStarted, "first initialization unit")
	if _, err := scheduler.SubmitForeground(context.Background(), "too-early", func(context.Context) error {
		return nil
	}); !errors.Is(err, syscall.EBUSY) {
		t.Fatalf("expected EBUSY during initialization, got %v", err)
	}

	close(releaseInitialization)
	deadline := time.After(2 * time.Second)
	for {
		handle, err := scheduler.SubmitForeground(context.Background(), "after-init", func(context.Context) error {
			return nil
		})
		if err == nil {
			if err := handle.Wait(context.Background()); err != nil {
				t.Fatal(err)
			}
			break
		}
		if !errors.Is(err, syscall.EBUSY) {
			t.Fatalf("unexpected admission error after initialization: %v", err)
		}
		select {
		case <-deadline:
			t.Fatal("initialization did not complete")
		case <-time.After(time.Millisecond):
		}
	}
	if got := units.Load(); got != 2 {
		t.Fatalf("expected two initialization units, got %d", got)
	}
	stopScheduler(t, scheduler)
}

func TestSchedulerForegroundWaitsForBackgroundSafeBoundary(t *testing.T) {
	backgroundStarted := make(chan struct{})
	releaseBackground := make(chan struct{})
	foregroundStarted := make(chan struct{})
	releaseForeground := make(chan struct{})
	secondBackgroundStarted := make(chan struct{})
	var units atomic.Int32

	task := BackgroundTaskFunc(func(context.Context) (bool, error) {
		switch units.Add(1) {
		case 1:
			close(backgroundStarted)
			<-releaseBackground
			return false, nil
		case 2:
			close(secondBackgroundStarted)
			return true, nil
		default:
			return true, nil
		}
	})
	scheduler := NewScheduler(task)
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, backgroundStarted, "first background unit")

	handle, err := scheduler.SubmitForeground(context.Background(), "read-1", func(context.Context) error {
		close(foregroundStarted)
		<-releaseForeground
		return nil
	})
	if err != nil {
		t.Fatalf("foreground request was not admitted during background work: %v", err)
	}
	select {
	case <-foregroundStarted:
		t.Fatal("foreground operation started before the background safe boundary")
	default:
	}

	close(releaseBackground)
	waitSignal(t, foregroundStarted, "foreground operation")
	select {
	case <-secondBackgroundStarted:
		t.Fatal("background started another unit while foreground work was active")
	default:
	}

	close(releaseForeground)
	if err := handle.Wait(context.Background()); err != nil {
		t.Fatalf("foreground operation failed: %v", err)
	}
	waitSignal(t, secondBackgroundStarted, "second background unit")
	stopScheduler(t, scheduler)
}

func TestSchedulerSerializesForegroundOperations(t *testing.T) {
	scheduler := NewScheduler(nil)
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	first, err := scheduler.SubmitForeground(context.Background(), "first", func(context.Context) error {
		close(firstStarted)
		<-releaseFirst
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	waitSignal(t, firstStarted, "first foreground operation")

	second, err := scheduler.SubmitForeground(context.Background(), "second", func(context.Context) error {
		close(secondStarted)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondStarted:
		t.Fatal("second foreground operation overlapped the first")
	case <-time.After(30 * time.Millisecond):
	}

	close(releaseFirst)
	if err := first.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, secondStarted, "second foreground operation")
	if err := second.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	stopScheduler(t, scheduler)
}

func TestSchedulerExclusiveDrainsForegroundAndBlocksNewRequests(t *testing.T) {
	scheduler := NewScheduler(nil)
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}

	foregroundStarted := make(chan struct{})
	releaseForeground := make(chan struct{})
	foreground, err := scheduler.SubmitForeground(context.Background(), "foreground", func(context.Context) error {
		close(foregroundStarted)
		<-releaseForeground
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	waitSignal(t, foregroundStarted, "foreground operation")

	var units atomic.Int32
	exclusiveStarted := make(chan struct{})
	exclusive, err := scheduler.SubmitExclusive(context.Background(), "maintenance", ExclusiveTaskFunc(func(context.Context) (bool, error) {
		if units.Add(1) == 1 {
			close(exclusiveStarted)
			return true, nil
		}
		return true, nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := scheduler.SubmitForeground(context.Background(), "during-maintenance", func(context.Context) error { return nil }); !errors.Is(err, syscall.EBUSY) {
		t.Fatalf("expected EBUSY while draining, got %v", err)
	}
	select {
	case <-exclusiveStarted:
		t.Fatal("exclusive task started before foreground drained")
	default:
	}
	close(releaseForeground)
	if err := foreground.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, exclusiveStarted, "exclusive task")
	if err := exclusive.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.SubmitForeground(context.Background(), "after-maintenance", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("foreground admission did not reopen: %v", err)
	}
	stopScheduler(t, scheduler)
}

func TestSchedulerStopWaitsForBackgroundAndAcceptedForeground(t *testing.T) {
	backgroundStarted := make(chan struct{})
	releaseBackground := make(chan struct{})
	foregroundRan := make(chan struct{})
	task := BackgroundTaskFunc(func(context.Context) (bool, error) {
		close(backgroundStarted)
		<-releaseBackground
		return true, nil
	})
	scheduler := NewScheduler(task)
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, backgroundStarted, "background unit")

	handle, err := scheduler.SubmitForeground(context.Background(), "accepted-before-stop", func(context.Context) error {
		close(foregroundRan)
		return nil
	})
	if err != nil {
		t.Fatalf("foreground request was not accepted: %v", err)
	}

	stopped := make(chan error, 1)
	go func() {
		stopped <- scheduler.Stop(context.Background())
	}()
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned before the background safe boundary: %v", err)
	case <-time.After(30 * time.Millisecond):
	}

	close(releaseBackground)
	waitSignal(t, foregroundRan, "accepted foreground operation during shutdown")
	if err := handle.Wait(context.Background()); err != nil {
		t.Fatalf("accepted foreground operation failed: %v", err)
	}
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after accepted work completed")
	}
}

func TestSchedulerBackgroundFailureDoesNotBlockForeground(t *testing.T) {
	expected := errors.New("background failure")
	scheduler := NewScheduler(BackgroundTaskFunc(func(context.Context) (bool, error) {
		return true, expected
	}))
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for scheduler.BackgroundError() == nil {
		select {
		case <-deadline:
			t.Fatal("background error was not recorded")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if !errors.Is(scheduler.BackgroundError(), expected) {
		t.Fatalf("unexpected background error: %v", scheduler.BackgroundError())
	}

	handle, err := scheduler.SubmitForeground(context.Background(), "after-background-error", func(context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("foreground request rejected after background failure: %v", err)
	}
	if err := handle.Wait(context.Background()); err != nil {
		t.Fatalf("foreground operation failed: %v", err)
	}
	stopScheduler(t, scheduler)
}

func TestSchedulerRejectsRequestsBeforeStartAndAfterStop(t *testing.T) {
	scheduler := NewScheduler(nil)
	operation := func(context.Context) error { return nil }
	if _, err := scheduler.SubmitForeground(context.Background(), "before-start", operation); !errors.Is(err, ErrSchedulerNotStarted) {
		t.Fatalf("expected not-started error, got %v", err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	stopScheduler(t, scheduler)
	if _, err := scheduler.SubmitForeground(context.Background(), "after-stop", operation); !errors.Is(err, ErrSchedulerStopping) {
		t.Fatalf("expected stopping error, got %v", err)
	}
}

func TestSchedulerExclusiveTasksRunInQueueOrder(t *testing.T) {
	scheduler := NewScheduler(nil)
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	var order atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	first, err := scheduler.SubmitExclusive(context.Background(), "first", ExclusiveTaskFunc(func(context.Context) (bool, error) {
		if order.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		return true, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	waitSignal(t, firstStarted, "first exclusive task")
	secondStarted := make(chan struct{})
	second, err := scheduler.SubmitExclusive(context.Background(), "second", ExclusiveTaskFunc(func(context.Context) (bool, error) {
		close(secondStarted)
		return true, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	close(releaseFirst)
	if err := first.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, secondStarted, "second exclusive task")
	if err := second.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	stopScheduler(t, scheduler)
}
