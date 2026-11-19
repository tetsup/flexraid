package orchestration

import (
	"context"
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
