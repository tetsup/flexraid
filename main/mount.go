package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hanwen/go-fuse/v2/fs"
	gofuse "github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tetsup/flexraid/distribution"
	flexfuse "github.com/tetsup/flexraid/fuse"
	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/orchestration"
)

func mountFilesystem(config mountConfig) (*gofuse.Server, error) {
	if len(config.DiskRoots) != diskCount {
		return nil, fmt.Errorf("got %d disk roots; want %d", len(config.DiskRoots), diskCount)
	}
	if config.Mountpoint == "" {
		return nil, fmt.Errorf("mountpoint is required")
	}

	ioLayers := make([]*physicalio.IO, 0, diskCount)
	for index, root := range config.DiskRoots {
		if root == "" {
			return nil, fmt.Errorf("disk root %d is empty", index+1)
		}
		if err := os.MkdirAll(root, 0700); err != nil {
			return nil, fmt.Errorf("create disk root %d: %w", index+1, err)
		}
		ioLayers = append(ioLayers, physicalio.New(physicalio.NewRequest{Root: root}).Layer)
	}
	distributed, err := distribution.New(distribution.NewRequest{Context: context.Background(), IOs: ioLayers})
	if err != nil {
		return nil, fmt.Errorf("initialize Distribution: %w", err)
	}
	created, err := orchestration.CreateRoot(orchestration.CreateRootRequest{
		Context: context.Background(),
		Distribution: distributed.Layer,
		Mode: 0755,
		UID: uint32(os.Getuid()),
		GID: uint32(os.Getgid()),
	})
	if err != nil {
		return nil, fmt.Errorf("initialize filesystem root: %w", err)
	}
	adapter, err := flexfuse.New(created.Layer, created.RootID)
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
	server, err := adapter.Mount(mountpoint, &fs.Options{NullPermissions: true})
	if err != nil {
		return nil, fmt.Errorf("mount at %s: %w", mountpoint, err)
	}
	return server, nil
}
