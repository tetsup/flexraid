package orchestration

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

func TestCleanupWorkerRunsAndStops(t *testing.T) {
	layer := &Orchestration{logger: slog.Default()}
	called := make(chan struct{}, 1)
	if err := layer.StartCleanupWorker(context.Background(), func(ctx context.Context) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case called <- struct{}{}:
		default:
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("cleanup callback was not invoked")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := layer.StopCleanupWorker(ctx); err != nil {
		t.Fatalf("StopCleanupWorker: %v", err)
	}
	if err := layer.StopCleanupWorker(ctx); err != nil {
		t.Fatalf("repeated StopCleanupWorker: %v", err)
	}
}

func TestCleanupWorkerStopRecordsCancellationCause(t *testing.T) {
	layer := &Orchestration{logger: slog.Default()}
	captured := make(chan context.Context, 1)
	if err := layer.StartCleanupWorker(context.Background(), func(ctx context.Context) error {
		select {
		case captured <- ctx:
		default:
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var workerContext context.Context
	select {
	case workerContext = <-captured:
	case <-time.After(time.Second):
		t.Fatal("cleanup callback was not invoked")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := layer.StopCleanupWorker(stopCtx); err != nil {
		t.Fatalf("StopCleanupWorker: %v", err)
	}
	if got := context.Cause(workerContext); !errors.Is(got, errOrchestrationCleanupWorkerStopped) {
		t.Fatalf("context cause = %v, want %v", got, errOrchestrationCleanupWorkerStopped)
	}
}

func TestCleanupWorkerPreservesParentCancellationCause(t *testing.T) {
	layer := &Orchestration{logger: slog.Default()}
	parent, cancelParent := context.WithCancelCause(context.Background())
	defer cancelParent(nil)
	captured := make(chan context.Context, 1)
	if err := layer.StartCleanupWorker(parent, func(ctx context.Context) error {
		select {
		case captured <- ctx:
		default:
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var workerContext context.Context
	select {
	case workerContext = <-captured:
	case <-time.After(time.Second):
		t.Fatal("cleanup callback was not invoked")
	}

	parentCause := errors.New("Management lifecycle ended")
	cancelParent(parentCause)
	deadline := time.After(time.Second)
	for workerContext.Err() == nil {
		select {
		case <-deadline:
			t.Fatal("cleanup worker context was not canceled")
		case <-time.After(time.Millisecond):
		}
	}
	if got := context.Cause(workerContext); !errors.Is(got, parentCause) {
		t.Fatalf("context cause = %v, want %v", got, parentCause)
	}
	stopCtx, cancelStop := context.WithTimeout(context.Background(), time.Second)
	defer cancelStop()
	if err := layer.StopCleanupWorker(stopCtx); err != nil {
		t.Fatalf("StopCleanupWorker after parent cancellation: %v", err)
	}
}
