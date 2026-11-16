package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	gofuse "github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tetsup/flexraid/distribution"
	flexfuse "github.com/tetsup/flexraid/fuse"
	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/management"
	"github.com/tetsup/flexraid/orchestration"
	"github.com/tetsup/flexraid/pkg/shared"
)

func mountFilesystem(config mountConfig) (*gofuse.Server, error) {
	if len(config.Disks) < minimumDiskCount {
		return nil, fmt.Errorf("got %d disks; want at least %d", len(config.Disks), minimumDiskCount)
	}
	if config.Mountpoint == "" {
		return nil, fmt.Errorf("mountpoint is required")
	}
	if err := management.ValidateDriveMappings(config.Disks); err != nil {
		return nil, fmt.Errorf("validate configured physical drives: %w", err)
	}

	ioLayers := make([]*physicalio.IO, 0, len(config.Disks))
	for index, disk := range config.Disks {
		if err := os.MkdirAll(disk.Root, 0700); err != nil {
			return nil, fmt.Errorf("create disk root %d: %w", index+1, err)
		}
		ioLayers = append(ioLayers, physicalio.New(physicalio.NewRequest{Root: disk.Root}).Layer)
	}
	ctx := context.Background()
	distributed, err := distribution.New(distribution.NewRequest{Context: ctx, IOs: ioLayers})
	if err != nil {
		return nil, fmt.Errorf("initialize Distribution: %w", err)
	}

	objects, err := distributed.Layer.ListObjects(distribution.ListObjectsRequest{Context: ctx})
	if err != nil {
		return nil, fmt.Errorf("list filesystem objects: %w", err)
	}

	var layer *orchestration.Orchestration
	var rootID shared.ObjectID
	if len(objects.ObjectIDs) == 0 {
		created, createErr := orchestration.CreateRoot(orchestration.CreateRootRequest{
			Context: ctx, Distribution: distributed.Layer,
			Mode: 0755, UID: uint32(os.Getuid()), GID: uint32(os.Getgid()),
		})
		if createErr != nil {
			return nil, fmt.Errorf("initialize filesystem root: %w", createErr)
		}
		layer = created.Layer
		rootID = created.RootID
	} else {
		initialized, initErr := orchestration.New(orchestration.NewRequest{
			Context: ctx, Distribution: distributed.Layer,
		})
		if initErr != nil {
			return nil, fmt.Errorf("restore filesystem namespace: %w", initErr)
		}
		layer = initialized.Layer
		rootID = initialized.RootID
	}

	managed, err := management.New(management.NewRequest{Orchestration: layer, Config: config})
	if err != nil {
		return nil, fmt.Errorf("initialize Management: %w", err)
	}
	adapter, err := flexfuse.New(managed, rootID)
	if err != nil {
		return nil, fmt.Errorf("create FUSE adapter: %w", err)
	}
	mountpoint, err := filepath.Abs(config.Mountpoint)
	if err != nil {
		return nil, fmt.Errorf("resolve mountpoint: %w", err)
	}
	if err := os.MkdirAll(mountpoint, 0755); err != nil {
		return nil, fmt.Errorf("create mountpoint: %w", err)
	}
	options := flexfuse.MountOptions()
	options.MountOptions.FsName = "flexraid-fuse"
	server, err := adapter.Mount(mountpoint, options)
	if err != nil {
		return nil, fmt.Errorf("mount at %s: %w", mountpoint, err)
	}
	return server, nil
}
