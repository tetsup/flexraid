package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime/metrics"
	"syscall"
	"time"

	"github.com/tetsup/flexraid/distribution"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

type grpcServer struct {
	server   *grpc.Server
	listener net.Listener
	socket   string
	serveErr chan error
}

type workerServiceServer interface {
	GetStatus(context.Context, *emptypb.Empty) (*structpb.Struct, error)
	StartTrace(context.Context, *emptypb.Empty) (*structpb.Struct, error)
	StopTrace(context.Context, *emptypb.Empty) (*structpb.Struct, error)
	GetTrace(context.Context, *emptypb.Empty) (*structpb.Struct, error)
	GetRuntimeMetrics(context.Context, *emptypb.Empty) (*structpb.Struct, error)
	GetDetailedStatus(context.Context, *emptypb.Empty) (*structpb.Struct, error)
	Inspect(context.Context, *emptypb.Empty) (*structpb.Struct, error)
	SpinUp(context.Context, *structpb.Struct) (*structpb.Struct, error)
	SpinDown(context.Context, *structpb.Struct) (*structpb.Struct, error)
	GetDriveCleanupPermissions(context.Context, *structpb.Struct) (*structpb.Struct, error)
	SetDriveDeprecatedVersionDeletionPermission(context.Context, *structpb.Struct) (*structpb.Struct, error)
	SetDriveAbnormalDataPreservationPermission(context.Context, *structpb.Struct) (*structpb.Struct, error)
	SetDriveLogicalObjectDeletionPermission(context.Context, *structpb.Struct) (*structpb.Struct, error)
}

type workerService struct {
	management *Management
}

func (service *workerService) StartTrace(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	response, err := service.management.StartTrace(StartTraceRequest{Context: ctx})
	if err != nil {
		return nil, managementRPCError(err)
	}
	return encodeWorkerResponse(response)
}

func (service *workerService) StopTrace(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	response, err := service.management.StopTrace(StopTraceRequest{Context: ctx})
	if err != nil {
		return nil, managementRPCError(err)
	}
	return encodeWorkerResponse(response)
}

func (service *workerService) GetTrace(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	response, err := service.management.GetTrace(GetTraceRequest{Context: ctx})
	if err != nil {
		return nil, managementRPCError(err)
	}
	return encodeWorkerResponse(response)
}

func (service *workerService) GetRuntimeMetrics(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, managementRPCError(err)
	}
	descriptions := metrics.All()
	samples := make([]metrics.Sample, len(descriptions))
	for index, description := range descriptions {
		samples[index].Name = description.Name
	}
	metrics.Read(samples)
	values := make(map[string]any, len(samples))
	for _, sample := range samples {
		switch sample.Value.Kind() {
		case metrics.KindUint64:
			values[sample.Name] = float64(sample.Value.Uint64())
		case metrics.KindFloat64:
			values[sample.Name] = sample.Value.Float64()
		}
	}
	if service.management.distribution != nil {
		cache, err := service.management.distribution.GetMetrics(distribution.GetMetricsRequest{Context: ctx, SummaryOnly: true})
		if err != nil {
			return nil, managementRPCError(err)
		}
		values["flexraid_distribution_cache"] = map[string]any{
			"object_index_entries": float64(cache.CacheMemory.ObjectIndexEntries),
			"stripe_index_entries": float64(cache.CacheMemory.StripeIndexEntries),
			"version_index_entries": float64(cache.CacheMemory.VersionIndexEntries),
			"orphan_chunk_entries": float64(cache.CacheMemory.OrphanChunkEntries),
			"cached_chunk_count": float64(cache.CacheMemory.CachedChunkCount),
			"cached_chunk_bytes": float64(cache.CacheMemory.CachedChunkBytes),
		}
	}
	return structpb.NewStruct(values)
}

func (service *workerService) GetStatus(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	response, err := service.management.GetStatus(GetStatusRequest{Context: ctx})
	if err != nil {
		return nil, managementRPCError(err)
	}
	return encodeWorkerResponse(response)
}

func (service *workerService) GetDetailedStatus(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	response, err := service.management.GetDetailedStatus(GetDetailedStatusRequest{Context: ctx})
	if err != nil {
		return nil, managementRPCError(err)
	}
	return encodeWorkerResponse(response)
}

func (service *workerService) Inspect(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	response, err := service.management.Inspect(InspectRequest{Context: ctx})
	if err != nil {
		return nil, managementRPCError(err)
	}
	return encodeWorkerResponse(response)
}

func (service *workerService) SpinUp(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	wwn, err := requiredString(request, "wwn")
	if err != nil {
		return nil, err
	}
	response, err := service.management.SpinUp(SpinUpRequest{Context: ctx, WWN: wwn})
	if err != nil {
		return nil, managementRPCError(err)
	}
	return encodeWorkerResponse(response)
}

func (service *workerService) SpinDown(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	wwn, err := requiredString(request, "wwn")
	if err != nil {
		return nil, err
	}
	response, err := service.management.SpinDown(SpinDownRequest{Context: ctx, WWN: wwn})
	if err != nil {
		return nil, managementRPCError(err)
	}
	return encodeWorkerResponse(response)
}

func (service *workerService) GetDriveCleanupPermissions(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	wwn, err := requiredString(request, "wwn")
	if err != nil {
		return nil, err
	}
	response, err := service.management.GetDriveCleanupPermissions(GetDriveCleanupPermissionsRequest{Context: ctx, WWN: wwn})
	if err != nil {
		return nil, managementRPCError(err)
	}
	return encodeWorkerResponse(response)
}

func (service *workerService) SetDriveDeprecatedVersionDeletionPermission(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	wwn, allowed, err := drivePermissionInput(request)
	if err != nil {
		return nil, err
	}
	response, err := service.management.SetDriveDeprecatedVersionDeletionPermission(SetDriveDeprecatedVersionDeletionPermissionRequest{Context: ctx, WWN: wwn, Allowed: allowed})
	if err != nil {
		return nil, managementRPCError(err)
	}
	return encodeWorkerResponse(response)
}

func (service *workerService) SetDriveAbnormalDataPreservationPermission(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	wwn, allowed, err := drivePermissionInput(request)
	if err != nil {
		return nil, err
	}
	response, err := service.management.SetDriveAbnormalDataPreservationPermission(SetDriveAbnormalDataPreservationPermissionRequest{Context: ctx, WWN: wwn, Allowed: allowed})
	if err != nil {
		return nil, managementRPCError(err)
	}
	return encodeWorkerResponse(response)
}

func (service *workerService) SetDriveLogicalObjectDeletionPermission(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	wwn, allowed, err := drivePermissionInput(request)
	if err != nil {
		return nil, err
	}
	response, err := service.management.SetDriveLogicalObjectDeletionPermission(SetDriveLogicalObjectDeletionPermissionRequest{Context: ctx, WWN: wwn, Allowed: allowed})
	if err != nil {
		return nil, managementRPCError(err)
	}
	return encodeWorkerResponse(response)
}

func requiredString(request *structpb.Struct, key string) (string, error) {
	if request == nil {
		return "", status.Errorf(codes.InvalidArgument, "%s is required", key)
	}
	value := request.Fields[key]
	if value == nil || value.GetStringValue() == "" {
		return "", status.Errorf(codes.InvalidArgument, "%s is required", key)
	}
	return value.GetStringValue(), nil
}

func drivePermissionInput(request *structpb.Struct) (string, bool, error) {
	wwn, err := requiredString(request, "wwn")
	if err != nil {
		return "", false, err
	}
	allowed := request.Fields["allowed"]
	if allowed == nil || allowed.Kind == nil {
		return "", false, status.Error(codes.InvalidArgument, "allowed is required")
	}
	if _, ok := allowed.Kind.(*structpb.Value_BoolValue); !ok {
		return "", false, status.Error(codes.InvalidArgument, "allowed must be a boolean")
	}
	return wwn, allowed.GetBoolValue(), nil
}

func encodeWorkerResponse(value any) (*structpb.Struct, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encode WorkerService response: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, status.Errorf(codes.Internal, "decode WorkerService response: %v", err)
	}
	result, err := structpb.NewStruct(fields)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "convert WorkerService response: %v", err)
	}
	return result, nil
}

func managementRPCError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func registerWorkerServiceServer(server *grpc.Server, service workerServiceServer) {
	server.RegisterService(&workerServiceDescription, service)
}

func workerGetStatusHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(emptypb.Empty)
	if err := decoder(in); err != nil {
		return nil, err
	}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(workerServiceServer).GetStatus(ctx, request.(*emptypb.Empty))
	}
	if interceptor == nil {
		return handler(ctx, in)
	}
	return interceptor(ctx, in, &grpc.UnaryServerInfo{Server: server, FullMethod: "/flexraid.worker.v1.WorkerService/GetStatus"}, handler)
}

func workerStartTraceHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	return workerEmptyHandler(server, ctx, decoder, interceptor, "StartTrace", func(ctx context.Context, service workerServiceServer) (any, error) {
		return service.StartTrace(ctx, &emptypb.Empty{})
	})
}
func workerStopTraceHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	return workerEmptyHandler(server, ctx, decoder, interceptor, "StopTrace", func(ctx context.Context, service workerServiceServer) (any, error) {
		return service.StopTrace(ctx, &emptypb.Empty{})
	})
}
func workerGetTraceHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	return workerEmptyHandler(server, ctx, decoder, interceptor, "GetTrace", func(ctx context.Context, service workerServiceServer) (any, error) {
		return service.GetTrace(ctx, &emptypb.Empty{})
	})
}
func workerEmptyHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor, method string, call func(context.Context, workerServiceServer) (any, error)) (any, error) {
	in := new(emptypb.Empty)
	if err := decoder(in); err != nil {
		return nil, err
	}
	handler := func(ctx context.Context, request any) (any, error) { return call(ctx, server.(workerServiceServer)) }
	if interceptor == nil {
		return handler(ctx, in)
	}
	return interceptor(ctx, in, &grpc.UnaryServerInfo{Server: server, FullMethod: "/flexraid.worker.v1.WorkerService/" + method}, handler)
}

func workerGetRuntimeMetricsHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	return workerEmptyHandler(server, ctx, decoder, interceptor, "GetRuntimeMetrics", func(ctx context.Context, service workerServiceServer) (any, error) {
		return service.GetRuntimeMetrics(ctx, &emptypb.Empty{})
	})
}

func workerGetDetailedStatusHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(emptypb.Empty)
	if err := decoder(in); err != nil {
		return nil, err
	}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(workerServiceServer).GetDetailedStatus(ctx, request.(*emptypb.Empty))
	}
	if interceptor == nil {
		return handler(ctx, in)
	}
	return interceptor(ctx, in, &grpc.UnaryServerInfo{Server: server, FullMethod: "/flexraid.worker.v1.WorkerService/GetDetailedStatus"}, handler)
}

func workerInspectHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(emptypb.Empty)
	if err := decoder(in); err != nil {
		return nil, err
	}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(workerServiceServer).Inspect(ctx, request.(*emptypb.Empty))
	}
	if interceptor == nil {
		return handler(ctx, in)
	}
	return interceptor(ctx, in, &grpc.UnaryServerInfo{Server: server, FullMethod: "/flexraid.worker.v1.WorkerService/Inspect"}, handler)
}

func workerSpinUpHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	return workerStructHandler(server, ctx, decoder, interceptor, "SpinUp", func(ctx context.Context, service workerServiceServer, request *structpb.Struct) (any, error) {
		return service.SpinUp(ctx, request)
	})
}

func workerSpinDownHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	return workerStructHandler(server, ctx, decoder, interceptor, "SpinDown", func(ctx context.Context, service workerServiceServer, request *structpb.Struct) (any, error) {
		return service.SpinDown(ctx, request)
	})
}

func workerGetDriveCleanupPermissionsHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	return workerStructHandler(server, ctx, decoder, interceptor, "GetDriveCleanupPermissions", func(ctx context.Context, service workerServiceServer, request *structpb.Struct) (any, error) {
		return service.GetDriveCleanupPermissions(ctx, request)
	})
}

func workerSetDriveDeprecatedVersionDeletionPermissionHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	return workerStructHandler(server, ctx, decoder, interceptor, "SetDriveDeprecatedVersionDeletionPermission", func(ctx context.Context, service workerServiceServer, request *structpb.Struct) (any, error) {
		return service.SetDriveDeprecatedVersionDeletionPermission(ctx, request)
	})
}

func workerSetDriveAbnormalDataPreservationPermissionHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	return workerStructHandler(server, ctx, decoder, interceptor, "SetDriveAbnormalDataPreservationPermission", func(ctx context.Context, service workerServiceServer, request *structpb.Struct) (any, error) {
		return service.SetDriveAbnormalDataPreservationPermission(ctx, request)
	})
}

func workerSetDriveLogicalObjectDeletionPermissionHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	return workerStructHandler(server, ctx, decoder, interceptor, "SetDriveLogicalObjectDeletionPermission", func(ctx context.Context, service workerServiceServer, request *structpb.Struct) (any, error) {
		return service.SetDriveLogicalObjectDeletionPermission(ctx, request)
	})
}

func workerStructHandler(
	server any,
	ctx context.Context,
	decoder func(any) error,
	interceptor grpc.UnaryServerInterceptor,
	method string,
	call func(context.Context, workerServiceServer, *structpb.Struct) (any, error),
) (any, error) {
	in := new(structpb.Struct)
	if err := decoder(in); err != nil {
		return nil, err
	}
	handler := func(ctx context.Context, request any) (any, error) {
		return call(ctx, server.(workerServiceServer), request.(*structpb.Struct))
	}
	if interceptor == nil {
		return handler(ctx, in)
	}
	return interceptor(ctx, in, &grpc.UnaryServerInfo{Server: server, FullMethod: "/flexraid.worker.v1.WorkerService/" + method}, handler)
}

var workerServiceDescription = grpc.ServiceDesc{
	ServiceName: "flexraid.worker.v1.WorkerService",
	HandlerType: (*workerServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "GetStatus", Handler: workerGetStatusHandler},
		{MethodName: "StartTrace", Handler: workerStartTraceHandler},
		{MethodName: "StopTrace", Handler: workerStopTraceHandler},
		{MethodName: "GetTrace", Handler: workerGetTraceHandler},
		{MethodName: "GetRuntimeMetrics", Handler: workerGetRuntimeMetricsHandler},
		{MethodName: "GetDetailedStatus", Handler: workerGetDetailedStatusHandler},
		{MethodName: "Inspect", Handler: workerInspectHandler},
		{MethodName: "SpinUp", Handler: workerSpinUpHandler},
		{MethodName: "SpinDown", Handler: workerSpinDownHandler},
		{MethodName: "GetDriveCleanupPermissions", Handler: workerGetDriveCleanupPermissionsHandler},
		{MethodName: "SetDriveDeprecatedVersionDeletionPermission", Handler: workerSetDriveDeprecatedVersionDeletionPermissionHandler},
		{MethodName: "SetDriveAbnormalDataPreservationPermission", Handler: workerSetDriveAbnormalDataPreservationPermissionHandler},
		{MethodName: "SetDriveLogicalObjectDeletionPermission", Handler: workerSetDriveLogicalObjectDeletionPermissionHandler},
	},
	Metadata: "api/worker/v1/worker.proto",
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
	if info, err := os.Lstat(layer.grpcSocketPath); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("Worker gRPC socket path %q exists and is not a socket", layer.grpcSocketPath)
		}
		connection, dialErr := net.DialTimeout("unix", layer.grpcSocketPath, 250*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			return fmt.Errorf("Worker gRPC socket %q is already accepting connections", layer.grpcSocketPath)
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
			return fmt.Errorf("check existing Worker gRPC socket %q: %w", layer.grpcSocketPath, dialErr)
		}
		if err := os.Remove(layer.grpcSocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale Worker gRPC socket %q: %w", layer.grpcSocketPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Worker gRPC socket path: %w", err)
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

	server := grpc.NewServer(grpc.MaxSendMsgSize(64 << 20))
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
