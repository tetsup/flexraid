// Package management provides the serialized entry points for Orchestration operations.
package management

import (
	"context"
	"fmt"
	"sync"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/orchestration"
	"github.com/tetsup/flexraid/pkg/shared"
)

type Management struct {
	mu                     sync.Mutex
	orchestration          *orchestration.Orchestration
	distribution           *distribution.Distribution
	driveOrder             []string
	drives                 map[string]DriveConfig
	cleanupPermissions     map[string]shared.DriveCleanupPermission
	cleanupPermissionStore *shared.CleanupPermissionStore
	scheduler              *Scheduler
}

func New(request NewRequest) (*Management, error) {
	if request.Orchestration == nil && request.Initialization == nil {
		return nil, fmt.Errorf("Orchestration layer or initialization task is required")
	}
	drives, err := validateDriveConfig(request.Config.Disks)
	if err != nil {
		return nil, err
	}
	permissions := make(map[string]shared.DriveCleanupPermission, len(drives))
	for wwn, drive := range drives {
		permissions[wwn] = shared.DriveCleanupPermission{
			DeleteAllowed:   drive.CleanupDeleteAllowed,
			PreserveAllowed: drive.CleanupPreserveAllowed,
		}
	}
	driveOrder := configuredDriveOrder(request.Config.Disks)
	sharedPermissions := make([]shared.DriveCleanupPermission, len(driveOrder))
	for index, wwn := range driveOrder {
		current := permissions[wwn]
		sharedPermissions[index] = shared.DriveCleanupPermission{DeleteAllowed: current.DeleteAllowed, PreserveAllowed: current.PreserveAllowed}
	}
	permissionStore := request.CleanupPermissions
	if permissionStore == nil {
		permissionStore = shared.NewCleanupPermissionStore(sharedPermissions)
	}
	layer := &Management{
		orchestration:          request.Orchestration,
		distribution:           request.Distribution,
		driveOrder:             driveOrder,
		drives:                 drives,
		cleanupPermissions:     permissions,
		cleanupPermissionStore: permissionStore,
	}
	if receiver, ok := request.Initialization.(cleanupPermissionStoreReceiver); ok {
		receiver.SetCleanupPermissionStore(permissionStore)
	}
	if request.Orchestration != nil {
		request.Orchestration.SetCleanupPermissionStore(permissionStore)
	}
	if request.Distribution != nil {
		if err := request.Distribution.SetCleanupPermissionStore(permissionStore); err != nil {
			return nil, err
		}
	}
	if request.Initialization != nil {
		layer.scheduler = NewSchedulerWithInitialization(request.Initialization, nil)
	}
	if layer.distribution != nil {
		if err := layer.publishCleanupPermissionsLocked(); err != nil {
			return nil, err
		}
		if err := layer.distribution.StartCleanupWorker(context.Background()); err != nil {
			return nil, err
		}
	}
	return layer, nil
}

func (layer *Management) Cleanup(request orchestration.CleanupRequest) (orchestration.CleanupResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.CleanupLocked(request)
}

func (layer *Management) Create(request orchestration.CreateRequest) (orchestration.CreateResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.CreateLocked(request)
}

func (layer *Management) Flush(request orchestration.FlushRequest) (orchestration.FlushResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.FlushLocked(request)
}

func (layer *Management) Fsync(request orchestration.FsyncRequest) (orchestration.FsyncResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.FsyncLocked(request)
}

func (layer *Management) GetAttr(request orchestration.GetAttrRequest) (orchestration.GetAttrResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.GetAttrLocked(request)
}

func (layer *Management) Link(request orchestration.LinkRequest) (orchestration.LinkResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.LinkLocked(request)
}

func (layer *Management) Lookup(request orchestration.LookupRequest) (orchestration.LookupResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.LookupLocked(request)
}

func (layer *Management) Mkdir(request orchestration.MkdirRequest) (orchestration.MkdirResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.MkdirLocked(request)
}

func (layer *Management) Open(request orchestration.OpenRequest) (orchestration.OpenResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.OpenLocked(request)
}

func (layer *Management) Release(request orchestration.ReleaseRequest) (orchestration.ReleaseResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.ReleaseLocked(request)
}

func (layer *Management) Read(request orchestration.ReadRequest) (orchestration.ReadResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.ReadLocked(request)
}

func (layer *Management) ReadDir(request orchestration.ReadDirRequest) (orchestration.ReadDirResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.ReadDirLocked(request)
}

func (layer *Management) ReadLink(request orchestration.ReadLinkRequest) (orchestration.ReadLinkResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.ReadLinkLocked(request)
}

func (layer *Management) Rename(request orchestration.RenameRequest) (orchestration.RenameResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.RenameLocked(request)
}

func (layer *Management) Rmdir(request orchestration.RmdirRequest) (orchestration.RmdirResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.RmdirLocked(request)
}

func (layer *Management) SetAttr(request orchestration.SetAttrRequest) (orchestration.SetAttrResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.SetAttrLocked(request)
}

func (layer *Management) StatFs(request orchestration.StatFsRequest) (orchestration.StatFsResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.StatFsLocked(request)
}

func (layer *Management) Symlink(request orchestration.SymlinkRequest) (orchestration.SymlinkResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.SymlinkLocked(request)
}

func (layer *Management) Truncate(request orchestration.TruncateRequest) (orchestration.TruncateResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.TruncateLocked(request)
}

func (layer *Management) Unlink(request orchestration.UnlinkRequest) (orchestration.UnlinkResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.UnlinkLocked(request)
}

func (layer *Management) Write(request orchestration.WriteRequest) (orchestration.WriteResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.WriteLocked(request)
}
