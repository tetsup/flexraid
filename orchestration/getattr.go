package orchestration

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

type GetAttrRequest struct {
	Context  context.Context
	ObjectID shared.ObjectID
}

type ObjectAttributes struct {
	ObjectID shared.ObjectID
	Kind     shared.ObjectKind
	Name     string
	Mode     uint32
	UID      uint32
	GID      uint32
	Nlink    uint32
	Size     uint64
	Atime    time.Time
	Mtime    time.Time
	Ctime    time.Time
}

type GetAttrResponse struct{ Attributes ObjectAttributes }

// GetAttr returns the in-memory logical attributes of an object.
func (layer *Orchestration) GetAttr(request GetAttrRequest) (GetAttrResponse, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return GetAttrResponse{}, err
	}
	object, exists := layer.objects[request.ObjectID]
	if !exists || object.header.Deleted {
		return GetAttrResponse{}, os.ErrNotExist
	}
	if object.header.Kind == objectKindHardLinkedFile {
		return GetAttrResponse{}, fmt.Errorf("HardLinkedFile %s is not a namespace object: %w", request.ObjectID.String(), os.ErrNotExist)
	}
	h := object.header
	if object.file != nil {
		object.file.mu.RLock()
		backing := object.file.header
		h.Mode, h.UID, h.GID = backing.Mode, backing.UID, backing.GID
		h.Nlink, h.Size = backing.Nlink, backing.Size
		h.Atime, h.Mtime, h.Ctime = backing.Atime, backing.Mtime, backing.Ctime
		object.file.mu.RUnlock()
	}
	attributes := ObjectAttributes{
		ObjectID: h.ObjectID, Kind: h.Kind, Name: h.Name, Mode: h.Mode,
		UID: h.UID, GID: h.GID, Nlink: h.Nlink, Size: h.Size,
		Atime: h.Atime, Mtime: h.Mtime, Ctime: h.Ctime,
	}
	if layer.logger != nil {
		layer.logger.Debug("orchestration GetAttr completed", slog.String("object_id", request.ObjectID.String()), slog.String("kind", fmt.Sprint(h.Kind)))
	}
	return GetAttrResponse{Attributes: attributes}, nil
}
