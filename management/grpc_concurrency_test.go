package management

import (
	"context"
	"net"
	"path/filepath"
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

// runLockedForegroundOperation models the locking and cancellation boundary
// used by Management foreground operations.
func runLockedForegroundOperation(ctx context.Context, layer *Management, operation func()) error {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	operation()
	return nil
}

func TestWorkerGRPCMonitoringConcurrentWithCancelledForegroundOperations(t *testing.T) {
	layer, connection := newGRPCConcurrencyFixture(t)

	monitorCtx, stopMonitor := context.WithCancel(context.Background())
	var rpcCalls atomic.Int64
	var rpcErrors atomic.Int64
	var monitorReady atomic.Bool
	firstRPC := make(chan struct{})
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
					close(firstRPC)
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
		stopMonitor()
		t.Fatal("GetStatus monitoring did not start")
	}

	blockerStarted := make(chan struct{})
	releaseBlocker := make(chan struct{})
	blockerDone := make(chan struct{})
	go func() {
		defer close(blockerDone)
		layer.mu.Lock()
		defer layer.mu.Unlock()
		close(blockerStarted)
		<-releaseBlocker
	}()
	select {
	case <-blockerStarted:
	case <-time.After(5 * time.Second):
		close(releaseBlocker)
		t.Fatal("blocking Management operation did not acquire the lock")
	}

	const requestCount = 64
	type queuedRequest struct {
		cancel context.CancelFunc
		done   chan error
	}
	requests := make([]queuedRequest, requestCount)
	started := make(chan struct{}, requestCount)
	var executed atomic.Int64
	for index := range requests {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		requests[index] = queuedRequest{cancel: cancel, done: done}
		go func() {
			started <- struct{}{}
			done <- runLockedForegroundOperation(ctx, layer, func() {
				executed.Add(1)
			})
		}()
	}
	for range requests {
		<-started
	}
	for index, request := range requests {
		if index%2 == 0 {
			request.cancel()
		}
	}
	close(releaseBlocker)
	<-blockerDone
	for index, request := range requests {
		err := <-request.done
		if index%2 == 0 && err != context.Canceled {
			t.Errorf("cancelled operation %d: error = %v, want context.Canceled", index, err)
		}
		if index%2 != 0 && err != nil {
			t.Errorf("operation %d: %v", index, err)
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
		t.Fatalf("executed operations = %d, want %d; cancelled operations must not execute", got, want)
	}
	if got := rpcCalls.Load(); got == 0 {
		t.Fatal("no monitoring RPCs were completed")
	}
	if got := rpcErrors.Load(); got != 0 {
		t.Fatalf("monitoring RPC errors = %d, want 0", got)
	}
	if err := invokeGetStatus(context.Background(), connection); err != nil {
		t.Fatalf("GetStatus after lock contention: %v", err)
	}
}

func BenchmarkManagementMutexWithGRPCMonitoring(b *testing.B) {
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

			var sink atomic.Uint64
			var buffer [4096]byte
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				ctx, cancel := context.WithCancel(context.Background())
				if test.cancelled {
					cancel()
				}
				err := runLockedForegroundOperation(ctx, layer, func() {
					for offset := range buffer {
						buffer[offset] = byte((index + offset*31) % 251)
					}
					sink.Add(uint64(buffer[index%len(buffer)]))
				})
				if test.cancelled {
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
			_ = sink.Load()
			stopMonitor()
			if test.monitor {
				<-monitorDone
			}
		})
	}
}
