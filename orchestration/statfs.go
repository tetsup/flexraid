package orchestration

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/pkg/shared/config"
)

type StatFsRequest struct {
	Context context.Context
}

type StatFsResponse struct {
	Blocks uint64
	Bfree  uint64
	Bavail uint64
	Bsize  uint64
	Frsize uint64
	Files  uint64
	Ffree  uint64
}

// StatFs reports physical totals/free space and the logical space that can be
// newly placed using the configured Mirror and Parity layouts.
func (layer *Orchestration) StatFs(request StatFsRequest) (StatFsResponse, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return StatFsResponse{}, err
	}

	capacity, err := layer.distribution.GetCapacity(distribution.CapacityRequest{
		BlockSize: config.BlockSize, MirrorCopies: 2,
		DataChunks: config.DefaultDataChunks, ParityChunks: config.DefaultParityChunks,
	})
	if err != nil {
		return StatFsResponse{}, fmt.Errorf("get distribution capacity: %w", err)
	}
	var totalBytes, freeBytes uint64
	for _, ioCapacity := range capacity.Capacities {
		totalBytes += ioCapacity.TotalBytes
		freeBytes += ioCapacity.FreeBytes
	}

	response := StatFsResponse{
		Blocks: totalBytes / config.BlockSize,
		Bfree:  freeBytes / config.BlockSize,
		Bavail: min(capacity.MirrorAvailableBlocks, capacity.ParityAvailableBlocks),
		Bsize:  config.BlockSize,
		Frsize: config.BlockSize,
		Files:  layer.countReachableObjects(),
		Ffree:  0,
	}
	if layer.logger != nil {
		layer.logger.Debug("orchestration StatFs completed",
			slog.Uint64("blocks", response.Blocks),
			slog.Uint64("bfree", response.Bfree),
			slog.Uint64("bavail", response.Bavail),
			slog.Uint64("files", response.Files))
	}
	return response, nil
}

// countReachableObjects counts distinct logical objects reachable from root.
// HardLink names are aliases and do not increase the inode count.
func (layer *Orchestration) countReachableObjects() uint64 {
	if layer.root == nil {
		return 0
	}
	visited := make(map[shared.ObjectID]struct{})
	queue := []*runtimeObject{layer.root}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == nil || current.header.Deleted {
			continue
		}

		if current.header.Kind != objectKindHardLink {
			if _, exists := visited[current.header.ObjectID]; exists {
				continue
			}
			visited[current.header.ObjectID] = struct{}{}
		}

		switch current.header.Kind {
		case objectKindDirectory:
			for _, child := range current.children {
				queue = append(queue, child)
			}
		case objectKindHardLink:
			if current.header.TargetID != nil {
				if target, exists := layer.objects[*current.header.TargetID]; exists && !target.header.Deleted {
					queue = append(queue, target)
				}
			}
		}
	}
	return uint64(len(visited))
}
