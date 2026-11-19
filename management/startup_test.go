package management

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/tetsup/flexraid/distribution"
	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/orchestration"
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

func TestLifecycleErrnoTracksEveryPhase(t *testing.T) {
	t.Run("initializing", func(t *testing.T) {
		continueInitialization := make(chan struct{})
		layer, err := New(NewRequest{
			Config: Config{Disks: []DriveConfig{{Root: "/data", Mountpoint: "/mnt/data", WWN: "wwn-test"}}},
			Initialization: InitializationTaskFunc(func(context.Context) (bool, error) {
				<-continueInitialization
				return true, nil
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := layer.LifecycleErrno(); got != syscall.EBUSY {
			t.Fatalf("LifecycleErrno = %v, want EBUSY", got)
		}
		if err := layer.Start(); err != nil {
			t.Fatal(err)
		}
		close(continueInitialization)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := layer.WaitReady(ctx); err != nil {
			t.Fatal(err)
		}
		if got := layer.LifecycleErrno(); got != 0 {
			t.Fatalf("Ready LifecycleErrno = %v, want success", got)
		}
		if err := layer.Stop(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("draining and maintenance", func(t *testing.T) {
		layer, err := New(NewRequest{
			Config:         Config{Disks: []DriveConfig{{Root: "/data", Mountpoint: "/mnt/data", WWN: "wwn-test"}}},
			Initialization: InitializationTaskFunc(func(context.Context) (bool, error) { return true, nil }),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := layer.Start(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := layer.WaitReady(ctx); err != nil {
			t.Fatal(err)
		}

		foregroundStarted, finishForeground := make(chan struct{}), make(chan struct{})
		foreground, err := layer.scheduler.SubmitForeground(ctx, "foreground-test", func(context.Context) error {
			close(foregroundStarted)
			<-finishForeground
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		<-foregroundStarted
		exclusiveStarted, finishExclusive := make(chan struct{}), make(chan struct{})
		exclusive, err := layer.scheduler.SubmitExclusive(ctx, "exclusive-test", ExclusiveTaskFunc(func(context.Context) (bool, error) {
			close(exclusiveStarted)
			<-finishExclusive
			return true, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		waitForLifecyclePhase(t, layer, LifecycleDraining)
		if got := layer.LifecycleErrno(); got != syscall.EBUSY {
			t.Fatalf("Draining LifecycleErrno = %v, want EBUSY", got)
		}
		close(finishForeground)
		if err := foreground.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		<-exclusiveStarted
		waitForLifecyclePhase(t, layer, LifecycleMaintenance)
		if got := layer.LifecycleErrno(); got != syscall.EBUSY {
			t.Fatalf("Maintenance LifecycleErrno = %v, want EBUSY", got)
		}
		close(finishExclusive)
		if err := exclusive.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		if got := layer.LifecycleErrno(); got != 0 {
			t.Fatalf("LifecycleErrno after maintenance = %v, want success", got)
		}
		if err := layer.Stop(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("failed", func(t *testing.T) {
		layer, err := New(NewRequest{
			Config: Config{Disks: []DriveConfig{{Root: "/data", Mountpoint: "/mnt/data", WWN: "wwn-test"}}},
			Initialization: InitializationTaskFunc(func(context.Context) (bool, error) {
				return true, errors.New("initialization failed")
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
		if err := layer.WaitReady(ctx); err == nil {
			t.Fatal("WaitReady succeeded after initialization failure")
		}
		if got := layer.LifecycleErrno(); got != syscall.EIO {
			t.Fatalf("Failed LifecycleErrno = %v, want EIO", got)
		}
		if err := layer.Stop(ctx); err != nil {
			t.Fatal(err)
		}
	})
}

func waitForLifecyclePhase(t *testing.T, layer *Management, want LifecyclePhase) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if layer.Phase() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("lifecycle phase = %v, want %v", layer.Phase(), want)
}

func TestBackgroundProcessesStartOnlyAfterReady(t *testing.T) {
	ioLayers := []*physicalio.IO{
		physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer,
		physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer,
	}
	distributed, err := distribution.New(distribution.NewRequest{IOs: ioLayers})
	if err != nil {
		t.Fatal(err)
	}
	created, err := orchestration.CreateRoot(orchestration.CreateRootRequest{
		Context: context.Background(), Distribution: distributed.Layer, Mode: 0755,
	})
	if err != nil {
		t.Fatal(err)
	}
	layer, err := New(NewRequest{
		Orchestration: created.Layer,
		Distribution:  distributed.Layer,
		Config: Config{Disks: []DriveConfig{
			{Root: t.TempDir(), Mountpoint: t.TempDir(), WWN: "wwn-test-1"},
			{Root: t.TempDir(), Mountpoint: t.TempDir(), WWN: "wwn-test-2"},
		}},
		Initialization: InitializationTaskFunc(func(context.Context) (bool, error) {
			return true, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := layer.StartBackgroundProcesses(context.Background()); err == nil {
		t.Fatal("background processes started before Management became Ready")
	}
	if err := layer.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := layer.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if !layer.backgroundProcessesStarted {
		t.Fatal("Ready OnEnter did not start both cleanup workers")
	}
	if err := layer.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if layer.backgroundProcessesStarted {
		t.Fatal("Ready OnExit did not stop both cleanup workers")
	}
}
