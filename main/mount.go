package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	gofuse "github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tetsup/flexraid/distribution"
	flexfuse "github.com/tetsup/flexraid/fuse"
	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/management"
	"github.com/tetsup/flexraid/orchestration"
	"github.com/tetsup/flexraid/pkg/shared"
)

type mountedFilesystem struct {
	server     *gofuse.Server
	management *management.Management
}

type filesystemInitialization struct {
	config                 mountConfig
	managed                *management.Management
	rootID                 *shared.ObjectID
	ioLayers               []*physicalio.IO
	distributed            *distribution.Distribution
	cleanupPermissionStore *shared.CleanupPermissionStore
	pendingLayer           *orchestration.Orchestration
	stage                  int
	disk                   int
}

func (task *filesystemInitialization) SetCleanupPermissionStore(store *shared.CleanupPermissionStore) {
	task.cleanupPermissionStore = store
}

func (task *filesystemInitialization) Next(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	switch task.stage {
	case 0:
		if len(task.config.Disks) < minimumDiskCount {
			return false, fmt.Errorf("got %d disks; want at least %d", len(task.config.Disks), minimumDiskCount)
		}
		if task.config.Mountpoint == "" {
			return false, fmt.Errorf("mountpoint is required")
		}
		if err := management.ValidateDriveMappings(task.config.Disks); err != nil {
			return false, fmt.Errorf("validate configured physical drives: %w", err)
		}
		task.ioLayers = make([]*physicalio.IO, 0, len(task.config.Disks))
		task.stage++
		return false, nil

	case 1:
		if task.disk < len(task.config.Disks) {
			disk := task.config.Disks[task.disk]
			if err := os.MkdirAll(disk.Root, 0700); err != nil {
				return false, fmt.Errorf("create disk root %d: %w", task.disk+1, err)
			}
			layer := physicalio.New(physicalio.NewRequest{Root: disk.Root, PreservationRoot: disk.Mountpoint}).Layer
			if _, err := layer.InitializeDevice(physicalio.InitializeDeviceRequest{
				Context: ctx,
				Config:  physicalio.DeviceConfig{Mountpoint: disk.Mountpoint, WWN: disk.WWN},
			}); err != nil {
				return false, fmt.Errorf("initialize physical drive %d: %w", task.disk+1, err)
			}
			task.ioLayers = append(task.ioLayers, layer)
			task.disk++
			return false, nil
		}
		task.stage++
		return false, nil

	case 2:
		if err := task.managed.SetIOs(task.ioLayers); err != nil {
			return false, fmt.Errorf("publish initialized I/O layers: %w", err)
		}
		result, err := distribution.New(distribution.NewRequest{Context: ctx, IOs: task.ioLayers, CleanupPermissions: task.cleanupPermissionStore})
		if err != nil {
			return false, fmt.Errorf("initialize Distribution: %w", err)
		}
		task.distributed = result.Layer
		if err := task.managed.SetDistribution(task.distributed); err != nil {
			return false, fmt.Errorf("publish initialized Distribution: %w", err)
		}
		task.stage++
		return false, nil

	case 3:
		objects, err := task.distributed.ListObjects(distribution.ListObjectsRequest{Context: ctx})
		if err != nil {
			return false, fmt.Errorf("list filesystem objects: %w", err)
		}
		if len(objects.ObjectIDs) == 0 {
			task.stage = 4
		} else {
			task.stage = 5
		}
		return false, nil

	case 4:
		created, err := orchestration.CreateRoot(orchestration.CreateRootRequest{
			Context:      ctx,
			Distribution: task.distributed,
			Mode:         0755,
			UID:          uint32(os.Getuid()),
			GID:          uint32(os.Getgid()),
		})
		if err != nil {
			return false, fmt.Errorf("initialize filesystem root: %w", err)
		}
		if err := created.Layer.SetCleanupPermissionStore(task.cleanupPermissionStore); err != nil {
			return false, err
		}
		*task.rootID = created.RootID
		task.stage = 6
		// Root creation is a durable initialization unit. Publish the layer in
		// the following unit so Stop can take effect at this boundary.
		task.pendingLayer = created.Layer
		return false, nil

	case 5:
		initialized, err := orchestration.New(orchestration.NewRequest{
			Context:            ctx,
			Distribution:       task.distributed,
			CleanupPermissions: task.cleanupPermissionStore,
		})
		if err != nil {
			return false, fmt.Errorf("restore filesystem namespace: %w", err)
		}
		*task.rootID = initialized.RootID
		task.pendingLayer = initialized.Layer
		task.stage = 6
		return false, nil

	case 6:
		if err := task.managed.SetOrchestration(task.pendingLayer); err != nil {
			return false, fmt.Errorf("publish initialized Orchestration: %w", err)
		}
		task.stage++
		return true, nil

	default:
		return true, nil
	}
}

func mountFilesystem(config mountConfig) (*mountedFilesystem, error) {
	rootID := shared.ObjectID{}
	initialization := &filesystemInitialization{config: config, rootID: &rootID}
	managed, err := management.New(management.NewRequest{Config: config, Initialization: initialization})
	if err != nil {
		return nil, fmt.Errorf("create Management: %w", err)
	}
	initialization.managed = managed

	if err := managed.Start(); err != nil {
		return nil, fmt.Errorf("start Management lifecycle: %w", err)
	}
	if err := managed.WaitReady(context.Background()); err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = managed.Stop(stopCtx)
		return nil, fmt.Errorf("initialize FlexRAID: %w", err)
	}

	adapter, err := flexfuse.New(managed, rootID)
	if err != nil {
		stopManagement(managed)
		return nil, fmt.Errorf("create FUSE adapter: %w", err)
	}
	mountpoint, err := filepath.Abs(config.Mountpoint)
	if err != nil {
		stopManagement(managed)
		return nil, fmt.Errorf("resolve mountpoint: %w", err)
	}
	if err := os.MkdirAll(mountpoint, 0755); err != nil {
		stopManagement(managed)
		return nil, fmt.Errorf("create mountpoint: %w", err)
	}
	options := flexfuse.MountOptions()
	options.MountOptions.FsName = "flexraid-fuse"
	server, err := adapter.Mount(mountpoint, options)
	if err != nil {
		stopManagement(managed)
		return nil, fmt.Errorf("mount at %s: %w", mountpoint, err)
	}
	return &mountedFilesystem{server: server, management: managed}, nil
}

func stopManagement(managed *management.Management) {
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = managed.Stop(stopCtx)
}
