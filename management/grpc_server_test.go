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

func TestWorkerGRPCServerRemainsAvailableAcrossLifecyclePhases(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "worker.sock")
	initializationGate := make(chan struct{})
	layer, err := New(NewRequest{
		Config: Config{
			Disks:          []DriveConfig{{Root: t.TempDir(), Mountpoint: t.TempDir(), WWN: "wwn-grpc-test"}},
			GRPCSocketPath: socketPath,
		},
		Initialization: InitializationTaskFunc(func(ctx context.Context) (bool, error) {
			select {
			case <-initializationGate:
				return true, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := layer.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
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
	if got := response.Fields["Lifecycle"].GetNumberValue(); got != float64(LifecycleInitializing) {
		t.Fatalf("initial lifecycle phase = %v, want %d", got, LifecycleInitializing)
	}

	close(initializationGate)
	if err := layer.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	response = new(structpb.Struct)
	if err := connection.Invoke(ctx, "/flexraid.worker.v1.WorkerService/GetStatus", &emptypb.Empty{}, response); err != nil {
		t.Fatalf("GetStatus RPC in Ready: %v", err)
	}
	if got := response.Fields["Lifecycle"].GetNumberValue(); got != float64(LifecycleReady) {
		t.Fatalf("ready lifecycle phase = %v, want %d", got, LifecycleReady)
	}

	permissionRequest, err := structpb.NewStruct(map[string]any{"wwn": "wwn-grpc-test", "allowed": true})
	if err != nil {
		t.Fatal(err)
	}
	permissionResponse := new(structpb.Struct)
	if err := connection.Invoke(ctx, "/flexraid.worker.v1.WorkerService/SetDriveDeprecatedVersionDeletionPermission", permissionRequest, permissionResponse); err != nil {
		t.Fatalf("SetDriveDeprecatedVersionDeletionPermission RPC: %v", err)
	}
	getPermissionRequest, err := structpb.NewStruct(map[string]any{"wwn": "wwn-grpc-test"})
	if err != nil {
		t.Fatal(err)
	}
	permissionResponse = new(structpb.Struct)
	if err := connection.Invoke(ctx, "/flexraid.worker.v1.WorkerService/GetDriveCleanupPermissions", getPermissionRequest, permissionResponse); err != nil {
		t.Fatalf("GetDriveCleanupPermissions RPC: %v", err)
	}
	permissions := permissionResponse.Fields["Permissions"].GetStructValue()
	if permissions == nil || !permissions.Fields["DeleteDeprecatedVersionsAllowed"].GetBoolValue() {
		t.Fatalf("cleanup permissions response = %v", permissionResponse)
	}

	if err := layer.Stop(ctx); err != nil {
		t.Fatalf("Management Stop: %v", err)
	}
	if _, err := os.Lstat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Worker socket remains after Stop: %v", err)
	}
}
