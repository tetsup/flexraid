package management

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

func newGRPCConcurrencyFixture(b testing.TB) (*Management, *grpc.ClientConn) {
	b.Helper()
	socketPath := filepath.Join(b.TempDir(), "worker.sock")
	layer, err := New(NewRequest{
		Config: Config{
			Disks: []DriveConfig{{
				Root:       filepath.Join(b.TempDir(), "storage"),
				Mountpoint: filepath.Join(b.TempDir(), "device"),
				WWN:        "wwn-concurrency-test",
			}},
			GRPCSocketPath: socketPath,
		},
	})
	if err != nil {
		b.Fatal(err)
	}
	if err := layer.Start(); err != nil {
		b.Fatal(err)
	}
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readyCancel()
	if err := layer.WaitReady(readyCtx); err != nil {
		b.Fatalf("wait for Management Ready: %v", err)
	}
	connection, err := grpc.NewClient(
		"passthrough:///worker-concurrency-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		}),
	)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = connection.Close()
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := layer.Stop(stopCtx); err != nil {
			b.Errorf("stop Management: %v", err)
		}
	})
	return layer, connection
}

func invokeGetStatus(ctx context.Context, connection *grpc.ClientConn) error {
	response := new(structpb.Struct)
	return connection.Invoke(ctx, "/flexraid.worker.v1.WorkerService/GetStatus", &emptypb.Empty{}, response)
}

func TestWorkerGRPCMonitoringConcurrentWithCancelledForegroundOperations(t *testing.T) {
	layer, connection := newGRPCConcurrencyFixture(t)

	monitorCtx, stopMonitor := context.WithCancel(context.Background())
	defer stopMonitor()
	var rpcCalls atomic.Int64
	var rpcErrors atomic.Int64
	var monitorReady atomic.Bool
	firstRPC := make(chan struct{})
	var firstRPCOnce sync.Once
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			callCtx, cancel := context.WithTimeout(monitorCtx, time.Second)
			err := invokeGetStatus(callCtx, connection)
			cancel()
			if err == nil {
				rpcCalls.Add(1)
				if monitorReady.CompareAndSwap(false, true) {
					firstRPCOnce.Do(func() { close(firstRPC) })
				}
			} else if monitorReady.Load() && monitorCtx.Err() == nil {
				rpcErrors.Add(1)
			}
			select {
			case <-monitorCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	select {
	case <-firstRPC:
	case <-time.After(5 * time.Second):
		t.Fatal("GetStatus monitoring did not start")
	}

	blockerStarted := make(chan struct{})
	releaseBlocker := make(chan struct{})
	blocker, err := layer.scheduler.SubmitForeground(context.Background(), "cancellation-blocker", func(context.Context) error {
		close(blockerStarted)
		<-releaseBlocker
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-blockerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("blocking foreground operation did not start")
	}

	const requestCount = 64
	type queuedRequest struct {
		handle *ForegroundHandle
		cancel context.CancelFunc
	}
	requests := make([]queuedRequest, 0, requestCount)
	var executed atomic.Int64
	for index := 0; index < requestCount; index++ {
		ctx, cancel := context.WithCancel(context.Background())
		handle, submitErr := layer.scheduler.SubmitForeground(ctx, fmt.Sprintf("queued-%d", index), func(context.Context) error {
			executed.Add(1)
			return nil
		})
		if submitErr != nil {
			cancel()
			close(releaseBlocker)
			t.Fatalf("submit queued request %d: %v", index, submitErr)
		}
		requests = append(requests, queuedRequest{handle: handle, cancel: cancel})
	}
	for index, request := range requests {
		if index%2 == 0 {
			request.cancel()
		}
	}
	close(releaseBlocker)
	if err := blocker.Wait(context.Background()); err != nil {
		t.Fatalf("blocking operation: %v", err)
	}
	for index, request := range requests {
		err := request.handle.Wait(context.Background())
		if index%2 == 0 && err != context.Canceled {
			t.Errorf("cancelled queued operation %d: error = %v, want context.Canceled", index, err)
		}
		if index%2 != 0 && err != nil {
			t.Errorf("queued operation %d: %v", index, err)
		}
		request.cancel()
	}
	stopMonitor()
	select {
	case <-monitorDone:
	case <-time.After(5 * time.Second):
		t.Fatal("GetStatus monitor did not stop")
	}

	if got, want := executed.Load(), requestCount/2; got != int64(want) {
		t.Fatalf("executed queued operations = %d, want %d; cancelled requests must not run", got, want)
	}
	if got := rpcCalls.Load(); got == 0 {
		t.Fatal("no monitoring RPCs were completed")
	}
	if got := rpcErrors.Load(); got != 0 {
		t.Fatalf("monitoring RPC errors = %d, want 0", got)
	}

	finalOperation, err := layer.scheduler.SubmitForeground(context.Background(), "after-cancellation", func(context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("submit operation after cancellations: %v", err)
	}
	if err := finalOperation.Wait(context.Background()); err != nil {
		t.Fatalf("operation after cancellations: %v", err)
	}
}

func BenchmarkSchedulerForegroundCancellationWithGRPCMonitoring(b *testing.B) {
	for _, test := range []struct {
		name      string
		cancelled bool
		monitor   bool
	}{
		{name: "normal-no-rpc"},
		{name: "cancelled-no-rpc", cancelled: true},
		{name: "normal-with-rpc", monitor: true},
		{name: "cancelled-with-rpc", cancelled: true, monitor: true},
	} {
		b.Run(test.name, func(b *testing.B) {
			layer, connection := newGRPCConcurrencyFixture(b)
			monitorCtx, stopMonitor := context.WithCancel(context.Background())
			monitorDone := make(chan struct{})
			if test.monitor {
				go func() {
					defer close(monitorDone)
					ticker := time.NewTicker(time.Millisecond)
					defer ticker.Stop()
					for {
						callCtx, cancel := context.WithTimeout(monitorCtx, time.Second)
						_ = invokeGetStatus(callCtx, connection)
						cancel()
						select {
						case <-monitorCtx.Done():
							return
						case <-ticker.C:
						}
					}
				}()
			}
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				ctx, cancel := context.WithCancel(context.Background())
				if test.cancelled {
					cancel()
				}
				handle, err := layer.scheduler.SubmitForeground(ctx, fmt.Sprintf("bench-%d", index), func(context.Context) error {
					return nil
				})
				if err != nil {
					cancel()
					b.Fatal(err)
				}
				if err := handle.Wait(context.Background()); test.cancelled {
					if err != context.Canceled {
						cancel()
						b.Fatalf("cancelled operation error = %v, want context.Canceled", err)
					}
				} else if err != nil {
					cancel()
					b.Fatal(err)
				}
				cancel()
			}
			b.StopTimer()
			stopMonitor()
			if test.monitor {
				<-monitorDone
			}
		})
	}
}
