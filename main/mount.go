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
	"github.com/tetsup/flexraid/orchestration"
	"github.com/tetsup/flexraid/pkg/shared"
)

func mountFilesystem(config mountConfig) (*gofuse.Server, error) {
	if len(config.DiskRoots) < minimumDiskCount {
		return nil, fmt.Errorf("got %d disk roots; want at least %d", len(config.DiskRoots), minimumDiskCount)
	}
	if config.Mountpoint == "" {
		return nil, fmt.Errorf("mountpoint is required")
	}

	ioLayers := make([]*physicalio.IO, 0, len(config.DiskRoots))
	for index, root := range config.DiskRoots {
		if root == "" {
			return nil, fmt.Errorf("disk root %d is empty", index+1)
		}
		if err := os.MkdirAll(root, 0700); err != nil {
			return nil, fmt.Errorf("create disk root %d: %w", index+1, err)
		}
		ioLayers = append(ioLayers, physicalio.New(physicalio.NewRequest{Root: root}).Layer)
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

	adapter, err := flexfuse.New(layer, rootID)
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
