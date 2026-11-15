package orchestration

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"syscall"

	"github.com/tetsup/flexraid/pkg/shared"
	cfg "github.com/tetsup/flexraid/pkg/shared/config"
)

type ReadLinkRequest struct {
	Context  context.Context
	ObjectID shared.ObjectID
}

type ReadLinkResponse struct {
	Target string
}

// ReadLink returns the target path stored in a symlink without resolving it.
// The target is loaded during initialization, so this operation performs no I/O.
func (layer *Orchestration) ReadLinkLocked(request ReadLinkRequest) (ReadLinkResponse, error) {
	return layer.readLinkLocked(request)
}

func (layer *Orchestration) readLinkLocked(request ReadLinkRequest) (ReadLinkResponse, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ReadLinkResponse{}, err
	}
	object, exists := layer.objects[request.ObjectID]
	if !exists || object.header.Deleted {
		return ReadLinkResponse{}, os.ErrNotExist
	}
	if object.header.Kind != objectKindSymlink {
		return ReadLinkResponse{}, syscall.EINVAL
	}
	if !object.linkTargetLoaded {
		return ReadLinkResponse{}, fmt.Errorf("symlink target is unavailable: %w", os.ErrNotExist)
	}
	if layer.logger != nil {
		layer.logger.Debug("orchestration ReadLink completed", slog.String("object_id", request.ObjectID.String()))
	}
	return ReadLinkResponse{Target: object.linkTarget}, nil
}

func decodeSymlinkTarget(data []byte, size uint64) (string, error) {
	offset := int(cfg.HeaderReservedSize)
	if len(data) < offset || size > uint64(len(data)-offset) {
		return "", fmt.Errorf("symlink target data is incomplete")
	}
	target := data[offset : offset+int(size)]
	if len(target) == 0 {
		return "", fmt.Errorf("symlink target is empty")
	}
	return string(target), nil
}
