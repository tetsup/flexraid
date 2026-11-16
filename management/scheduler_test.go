package management

import (
	"context"
	"errors"
	"sync/atomic"
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := scheduler.Stop(ctx); err != nil {
		t.Fatalf("scheduler did not stop: %v", err)
	}
}

func TestSchedulerRunsForegroundOperationsConcurrently(t *testing.T) {
	scheduler := NewScheduler(nil)
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	run := func(context.Context) error {
		started <- struct{}{}
		<-release
		return nil
	}
	first, err := scheduler.SubmitForeground(context.Background(), "first", run)
	if err != nil {
		t.Fatal(err)
	}
	second, err := scheduler.SubmitForeground(context.Background(), "second", run)
	if err != nil {
		t.Fatal(err)
	}

	waitSignal(t, started, "first foreground operation")
	waitSignal(t, started, "second foreground operation")
	close(release)

	if err := first.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := scheduler.Stop(ctx); err != nil {
		t.Fatal(err)
	}
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := scheduler.Stop(ctx); err != nil {
		t.Fatal(err)
	}
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
	if err := scheduler.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.SubmitForeground(context.Background(), "after-stop", operation); !errors.Is(err, ErrSchedulerStopping) {
		t.Fatalf("expected stopping error, got %v", err)
	}
}

func TestSchedulerAcquiresObjectLocksBeforeForegroundCallback(t *testing.T) {
	scheduler := NewScheduler(nil)
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	held, err := scheduler.locks.AcquireOperation(context.Background(), []ObjectLockRequest{
		{ObjectID: "object-1", Mode: ObjectLockExclusive},
	})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	handle, err := scheduler.SubmitForegroundWithLocks(
		context.Background(),
		"write-object-1",
		[]ObjectLockRequest{{ObjectID: "object-1", Mode: ObjectLockExclusive}},
		func(context.Context) error {
			close(started)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
		t.Fatal("foreground callback ran before its Object lock was acquired")
	case <-time.After(30 * time.Millisecond):
	}
	held()
	waitSignal(t, started, "foreground callback after Object lock release")
	if err := handle.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type lockAwareBackgroundTask struct {
	called chan struct{}
}

func (task *lockAwareBackgroundTask) Next(context.Context) (bool, error) {
	return true, nil
}

func (task *lockAwareBackgroundTask) NextWithLocks(ctx context.Context, locks *ObjectLockManager) (bool, error) {
	release, err := locks.AcquireOperation(ctx, []ObjectLockRequest{
		{ObjectID: "background-object", Mode: ObjectLockExclusive},
	})
	if err != nil {
		return true, err
	}
	defer release()
	close(task.called)
	return true, nil
}

func TestSchedulerBackgroundUsesSharedObjectLockManager(t *testing.T) {
	task := &lockAwareBackgroundTask{called: make(chan struct{})}
	scheduler := NewScheduler(task)
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, task.called, "background unit using ObjectLockManager")
	if err := scheduler.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
