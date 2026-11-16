package management

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManagementStartupLifecycleReachesReady(t *testing.T) {
	units := 0
	layer, err := New(NewRequest{
		Config: Config{Disks: []DriveConfig{{Root: "/data", Mountpoint: "/mnt/data", WWN: "wwn-test"}}},
		Initialization: InitializationTaskFunc(func(context.Context) (bool, error) {
			units++
			return units == 2, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if layer.Phase() != LifecycleInitializing {
		t.Fatalf("initial phase = %v, want Initializing", layer.Phase())
	}
	if err := layer.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := layer.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	if layer.Phase() != LifecycleReady {
		t.Fatalf("final phase = %v, want Ready", layer.Phase())
	}
	if units != 2 {
		t.Fatalf("initialization units = %d, want 2", units)
	}
	if err := layer.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestManagementStartupFailureDoesNotReachReady(t *testing.T) {
	expected := errors.New("startup failed")
	layer, err := New(NewRequest{
		Config: Config{Disks: []DriveConfig{{Root: "/data", Mountpoint: "/mnt/data", WWN: "wwn-test"}}},
		Initialization: InitializationTaskFunc(func(context.Context) (bool, error) {
			return true, expected
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := layer.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := layer.WaitReady(ctx); !errors.Is(err, expected) {
		t.Fatalf("WaitReady error = %v, want %v", err, expected)
	}
	if layer.Phase() != LifecycleFailed {
		t.Fatalf("phase = %v, want Failed", layer.Phase())
	}
	if err := layer.Stop(ctx); err != nil {
		t.Fatalf("Stop after failed startup: %v", err)
	}
}
