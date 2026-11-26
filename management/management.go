// Package management provides the serialized entry points for Orchestration operations.
package management

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tetsup/flexraid/distribution"
	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/orchestration"
	"github.com/tetsup/flexraid/pkg/shared"
)

type Management struct {
	cleanupPermissionUpdateMu  sync.Mutex
	orchestration              *orchestration.Orchestration
	distribution               *distribution.Distribution
	ios                        []*physicalio.IO
	driveOrder                 []string
	drives                     map[string]DriveConfig
	cleanupPermissionStore     *shared.CleanupPermissionStore
	backgroundProcessesStarted bool
	scheduler                  *Scheduler
	metrics                    *metricsCollector
	trace                      *traceRecorder
	grpcMu                     sync.Mutex
	grpcServer                 grpcServer
	grpcSocketPath             string
}

func New(request NewRequest) (*Management, error) {
	if request.Orchestration == nil && request.Initialization == nil && request.Config.GRPCSocketPath == "" {
		return nil, fmt.Errorf("Orchestration layer or initialization task is required")
	}
	drives, err := validateDriveConfig(request.Config.Disks)
	if err != nil {
		return nil, err
	}
	driveOrder := configuredDriveOrder(request.Config.Disks)
	if request.Distribution != nil && len(request.IOs) != len(driveOrder) {
		return nil, fmt.Errorf("I/O metrics source count %d does not match configured drive count %d", len(request.IOs), len(driveOrder))
	}
	sharedPermissions := make([]shared.DriveCleanupPermission, len(driveOrder))
	for index, drive := range request.Config.Disks {
		sharedPermissions[index] = shared.DriveCleanupPermission{DeleteDeprecatedVersionsAllowed: drive.CleanupDeleteDeprecatedVersionsAllowed, PreserveAbnormalDataAllowed: drive.CleanupPreserveAbnormalDataAllowed, DeleteLogicalObjectsAllowed: drive.CleanupDeleteLogicalObjectsAllowed}
	}
	permissionStore := request.CleanupPermissions
	if permissionStore == nil {
		permissionStore = shared.NewCleanupPermissionStore(sharedPermissions)
	} else if len(permissionStore.Snapshot().Drives) != len(driveOrder) {
		return nil, fmt.Errorf("cleanup permission store drive count does not match Management configuration")
	}
	layer := &Management{
		orchestration:          request.Orchestration,
		distribution:           request.Distribution,
		ios:                    append([]*physicalio.IO(nil), request.IOs...),
		driveOrder:             driveOrder,
		drives:                 drives,
		cleanupPermissionStore: permissionStore,
		metrics:                newMetricsCollector(),
		trace:                  newTraceRecorder(),
		grpcSocketPath:         request.Config.GRPCSocketPath,
	}
	if request.Distribution != nil {
		request.Distribution.SetTraceRecorder(layer.trace)
	}
	if request.Orchestration != nil {
		request.Orchestration.SetTraceRecorder(layer.trace)
	}
	if receiver, ok := request.Initialization.(cleanupPermissionStoreReceiver); ok {
		receiver.SetCleanupPermissionStore(permissionStore)
	}
	if request.Orchestration != nil {
		if err := request.Orchestration.SetCleanupPermissionStore(permissionStore); err != nil {
			return nil, err
		}
	}
	if request.Distribution != nil {
		if err := request.Distribution.SetCleanupPermissionStore(permissionStore); err != nil {
			return nil, err
		}
	}
	initialization := request.Initialization
	if initialization == nil && request.Config.GRPCSocketPath != "" {
		initialization = InitializationTaskFunc(func(context.Context) (bool, error) { return true, nil })
	}
	if initialization != nil {
		layer.scheduler = NewSchedulerWithLifecycleHooks(initialization, nil, layer.lifecycleHooks())
	}

	return layer, nil
}

func (layer *Management) Cleanup(request orchestration.CleanupRequest) (response orchestration.CleanupResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Cleanup", started, err) }()
	lockContext, lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.CleanupLocked(request)
}

func (layer *Management) Create(request orchestration.CreateRequest) (response orchestration.CreateResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Create", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.ParentID)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.CreateLocked(request)
}

func (layer *Management) Flush(request orchestration.FlushRequest) (response orchestration.FlushResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Flush", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.Handle.ObjectID())
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.FlushLocked(request)
}

func (layer *Management) Fsync(request orchestration.FsyncRequest) (response orchestration.FsyncResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Fsync", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.Handle.ObjectID())
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.FsyncLocked(request)
}

func (layer *Management) GetAttr(request orchestration.GetAttrRequest) (response orchestration.GetAttrResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("GetAttr", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.ObjectID)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.GetAttrLocked(request)
}

func (layer *Management) Link(request orchestration.LinkRequest) (response orchestration.LinkResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Link", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.SourceID, request.ParentID)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.LinkLocked(request)
}

func (layer *Management) Lookup(request orchestration.LookupRequest) (response orchestration.LookupResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Lookup", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.ParentID)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.LookupLocked(request)
}

func (layer *Management) Mkdir(request orchestration.MkdirRequest) (response orchestration.MkdirResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Mkdir", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.ParentID)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.MkdirLocked(request)
}

func (layer *Management) Open(request orchestration.OpenRequest) (response orchestration.OpenResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Open", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.ObjectID)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.OpenLocked(request)
}

func (layer *Management) Release(request orchestration.ReleaseRequest) (response orchestration.ReleaseResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Release", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.Handle.ObjectID())
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.ReleaseLocked(request)
}

func (layer *Management) Read(request orchestration.ReadRequest) (response orchestration.ReadResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Read", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.ObjectID)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.ReadLocked(request)
}

func (layer *Management) ReadDir(request orchestration.ReadDirRequest) (response orchestration.ReadDirResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("ReadDir", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.ObjectID)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.ReadDirLocked(request)
}

func (layer *Management) ReadLink(request orchestration.ReadLinkRequest) (response orchestration.ReadLinkResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("ReadLink", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.ObjectID)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.ReadLinkLocked(request)
}

func (layer *Management) Rename(request orchestration.RenameRequest) (response orchestration.RenameResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Rename", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.SourceParentID, request.DestinationParentID)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.RenameLocked(request)
}

func (layer *Management) Rmdir(request orchestration.RmdirRequest) (response orchestration.RmdirResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Rmdir", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.ParentID)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.RmdirLocked(request)
}

func (layer *Management) SetAttr(request orchestration.SetAttrRequest) (response orchestration.SetAttrResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("SetAttr", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.ObjectID)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.SetAttrLocked(request)
}

func (layer *Management) StatFs(request orchestration.StatFsRequest) (response orchestration.StatFsResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("StatFs", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.StatFsLocked(request)
}

func (layer *Management) Symlink(request orchestration.SymlinkRequest) (response orchestration.SymlinkResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Symlink", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.ParentID)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.SymlinkLocked(request)
}

func (layer *Management) Truncate(request orchestration.TruncateRequest) (response orchestration.TruncateResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Truncate", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.ObjectID)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.TruncateLocked(request)
}

func (layer *Management) Unlink(request orchestration.UnlinkRequest) (response orchestration.UnlinkResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Unlink", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.ParentID)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.UnlinkLocked(request)
}

func (layer *Management) Write(request orchestration.WriteRequest) (response orchestration.WriteResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Write", started, err) }()
	lockContext, unlock, lockErr := layer.lockObjectGroup(request.Context, request.ObjectID)
	if lockErr != nil {
		return response, lockErr
	}
	defer unlock()
	request.Context = lockContext
	return layer.orchestration.WriteLocked(request)
}

func (layer *Management) lockObjectGroup(ctx context.Context, ids ...shared.ObjectID) (context.Context, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := layer.orchestration.LockObjectGroup(orchestration.LockObjectGroupRequest{
		Context: ctx, ObjectIDs: ids,
	})
	if err != nil {
		return nil, nil, err
	}
	return result.Context, result.Unlock, nil
}
