package management

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestWorkerGRPCServerFollowsManagementLifecycle(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "worker.sock")
	layer, err := New(NewRequest{
		Config: Config{
			Disks: []DriveConfig{{Root: t.TempDir(), Mountpoint: t.TempDir(), WWN: "wwn-grpc-test"}},
			GRPCSocketPath: socketPath,
		},
		Initialization: InitializationTaskFunc(func(context.Context) (bool, error) { return true, nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := layer.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := layer.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}

	connection, err := grpc.NewClient(
		"passthrough:///worker",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	response := new(structpb.Struct)
	if err := connection.Invoke(ctx, "/flexraid.worker.v1.WorkerService/GetStatus", &emptypb.Empty{}, response); err != nil {
		t.Fatalf("GetStatus RPC: %v", err)
	}
	if response.Fields["level"].GetStringValue() != string(StatusFailed) {
		t.Fatalf("status level = %v, want %q", response.Fields["level"], StatusFailed)
	}

	if err := layer.Stop(ctx); err != nil {
		t.Fatalf("Management Stop: %v", err)
	}
	if _, err := os.Lstat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Worker socket remains after Stop: %v", err)
	}
}
