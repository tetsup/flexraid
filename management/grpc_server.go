package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type grpcServer struct {
	server   *grpc.Server
	listener net.Listener
	socket   string
	serveErr chan error
}

type workerServiceServer interface {
	GetStatus(context.Context, *emptypb.Empty) (*wrapperspb.StringValue, error)
	GetDetailedStatus(context.Context, *emptypb.Empty) (*wrapperspb.StringValue, error)
	Inspect(context.Context, *emptypb.Empty) (*wrapperspb.StringValue, error)
}

type workerService struct {
	management *Management
}

func (service *workerService) GetStatus(ctx context.Context, _ *emptypb.Empty) (*wrapperspb.StringValue, error) {
	response, err := service.management.GetStatus(GetStatusRequest{Context: ctx})
	if err != nil {
		return nil, managementRPCError(err)
	}
	return encodeWorkerResponse(response)
}

func (service *workerService) GetDetailedStatus(ctx context.Context, _ *emptypb.Empty) (*wrapperspb.StringValue, error) {
	response, err := service.management.GetDetailedStatus(GetDetailedStatusRequest{Context: ctx})
	if err != nil {
		return nil, managementRPCError(err)
	}
	return encodeWorkerResponse(response)
}

func (service *workerService) Inspect(ctx context.Context, _ *emptypb.Empty) (*wrapperspb.StringValue, error) {
	response, err := service.management.Inspect(InspectRequest{Context: ctx})
	if err != nil {
		return nil, managementRPCError(err)
	}
	return encodeWorkerResponse(response)
}

func encodeWorkerResponse(value any) (*wrapperspb.StringValue, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encode WorkerService response: %v", err)
	}
	return wrapperspb.String(string(encoded)), nil
}

func managementRPCError(err error) error {
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, err.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}

func registerWorkerServiceServer(server *grpc.Server, service workerServiceServer) {
	server.RegisterService(&workerServiceDescription, service)
}

func workerServiceHandler[Request any](
	ctx context.Context,
	server any,
	decoder func(any) error,
	interceptor grpc.UnaryServerInterceptor,
	method string,
	in Request,
	call func(context.Context, workerServiceServer, Request) (any, error),
) (any, error) {
	if err := decoder(&in); err != nil {
		return nil, err
	}
	handler := func(ctx context.Context, request any) (any, error) {
		return call(ctx, server.(workerServiceServer), request.(Request))
	}
	if interceptor == nil {
		return handler(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: "/flexraid.worker.v1.WorkerService/" + method}
	return interceptor(ctx, in, info, handler)
}

var workerServiceDescription = grpc.ServiceDesc{
	ServiceName: "flexraid.worker.v1.WorkerService",
	HandlerType: (*workerServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "GetStatus", Handler: workerGetStatusHandler},
		{MethodName: "GetDetailedStatus", Handler: workerGetDetailedStatusHandler},
		{MethodName: "Inspect", Handler: workerInspectHandler},
	},
	Metadata: "api/worker/v1/worker.proto",
}

func workerGetStatusHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	return workerServiceHandler(ctx, server, decoder, interceptor, "GetStatus", &emptypb.Empty{}, func(ctx context.Context, service workerServiceServer, _ *emptypb.Empty) (any, error) {
		return service.GetStatus(ctx, &emptypb.Empty{})
	})
}

func workerGetDetailedStatusHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	return workerServiceHandler(ctx, server, decoder, interceptor, "GetDetailedStatus", &emptypb.Empty{}, func(ctx context.Context, service workerServiceServer, _ *emptypb.Empty) (any, error) {
		return service.GetDetailedStatus(ctx, &emptypb.Empty{})
	})
}

func workerInspectHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	return workerServiceHandler(ctx, server, decoder, interceptor, "Inspect", &emptypb.Empty{}, func(ctx context.Context, service workerServiceServer, _ *emptypb.Empty) (any, error) {
		return service.Inspect(ctx, &emptypb.Empty{})
	})
}

func (layer *Management) startGRPCServer(ctx context.Context) error {
	if layer.grpcSocketPath == "" {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("gRPC server start context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	layer.grpcMu.Lock()
	defer layer.grpcMu.Unlock()
	if layer.grpcServer.server != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(layer.grpcSocketPath), 0750); err != nil {
		return fmt.Errorf("create Worker gRPC socket directory: %w", err)
	}
	listener, err := net.Listen("unix", layer.grpcSocketPath)
	if err != nil {
		return fmt.Errorf("listen on Worker gRPC socket %q: %w", layer.grpcSocketPath, err)
	}
	if err := os.Chmod(layer.grpcSocketPath, 0600); err != nil {
		_ = listener.Close()
		_ = os.Remove(layer.grpcSocketPath)
		return fmt.Errorf("set Worker gRPC socket permissions: %w", err)
	}

	server := grpc.NewServer()
	registerWorkerServiceServer(server, &workerService{management: layer})
	state := grpcServer{server: server, listener: listener, socket: layer.grpcSocketPath, serveErr: make(chan error, 1)}
	layer.grpcServer = state
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			state.serveErr <- err
		}
	}()
	return nil
}

func (layer *Management) stopGRPCServer(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("gRPC server stop context is required")
	}
	layer.grpcMu.Lock()
	state := layer.grpcServer
	layer.grpcServer = grpcServer{}
	layer.grpcMu.Unlock()
	if state.server == nil {
		return nil
	}

	stopped := make(chan struct{})
	go func() {
		state.server.GracefulStop()
		close(stopped)
	}()
	var stopErr error
	select {
	case <-stopped:
	case serveErr := <-state.serveErr:
		if serveErr != nil {
			stopErr = fmt.Errorf("Worker gRPC server: %w", serveErr)
		}
		state.server.Stop()
		<-stopped
	case <-ctx.Done():
		state.server.Stop()
		<-stopped
		stopErr = ctx.Err()
	}
	_ = state.listener.Close()
	if err := os.Remove(state.socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		stopErr = errors.Join(stopErr, fmt.Errorf("remove Worker gRPC socket: %w", err))
	}
	return stopErr
}

var _ = sync.Mutex{}
var _ = insecure.NewCredentials
