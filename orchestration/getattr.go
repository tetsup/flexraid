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

// ObjectAttributes is the POSIX-facing view of a namespace object. For a HardLink,
// ObjectID and Kind describe its backing File rather than the internal link entry.
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
	if !exists {
		return GetAttrResponse{}, os.ErrNotExist
	}
	if object.header.Deleted {
		if object.file == nil || openHandleCount(object.file) == 0 {
			return GetAttrResponse{}, os.ErrNotExist
		}
	}
	if _, protected := layer.protectedObjects[request.ObjectID]; protected {
		return GetAttrResponse{}, os.ErrNotExist
	}
	h := object.header
	if object.header.Kind == objectKindHardLink && (object.header.TargetID == nil || object.file == nil) {
		return GetAttrResponse{}, fmt.Errorf("HardLink %s has no resolvable File target: %w", request.ObjectID, errInvalidHeader)
	}
	if object.header.Kind == objectKindFile && object.header.ParentID == nil && layer.deriveHardLinkCount(object.header.ObjectID) == 0 &&
		(object.file == nil || openHandleCount(object.file) == 0) {
		return GetAttrResponse{}, os.ErrNotExist
	}
	if object.file != nil {
		object.file.mu.RLock()
		backing := object.file.header
		h.Mode, h.UID, h.GID = backing.Mode, backing.UID, backing.GID
		h.Size = backing.Size
		h.Atime, h.Mtime, h.Ctime = backing.Atime, backing.Mtime, backing.Ctime
		if object.header.Kind == objectKindHardLink {
			h.ObjectID = backing.ObjectID
			h.Kind = objectKindFile
		}
		object.file.mu.RUnlock()
	}
	attributes := ObjectAttributes{
		ObjectID: h.ObjectID, Kind: h.Kind, Name: object.header.Name, Mode: h.Mode,
		UID: h.UID, GID: h.GID, Nlink: layer.deriveNlink(object), Size: h.Size,
		Atime: h.Atime, Mtime: h.Mtime, Ctime: h.Ctime,
	}
	if layer.logger != nil {
		layer.logger.Debug("orchestration GetAttr completed", slog.String("object_id", request.ObjectID.String()), slog.String("kind", fmt.Sprint(h.Kind)))
	}
	return GetAttrResponse{Attributes: attributes}, nil
}

// deriveNlink calculates POSIX st_nlink from the currently reachable Namespace.
func (layer *Orchestration) deriveNlink(object *runtimeObject) uint32 {
	switch object.header.Kind {
	case objectKindDirectory:
		nlink := uint32(2)
		for _, child := range object.children {
			if child.header.Kind == objectKindDirectory && !child.header.Deleted {
				nlink++
			}
		}
		return nlink
	case objectKindFile:
		return layer.deriveHardLinkCount(object.header.ObjectID)
	case objectKindSymlink:
		return 1
	case objectKindHardLink:
		if object.header.TargetID == nil {
			return 0
		}
		return layer.deriveHardLinkCount(*object.header.TargetID)
	default:
		return 0
	}
}

// deriveHardLinkCount counts the direct File name, when present, and all reachable HardLink names.
func (layer *Orchestration) deriveHardLinkCount(targetID shared.ObjectID) uint32 {
	if layer.root == nil {
		return 0
	}
	var nlink uint32
	queue := []*runtimeObject{layer.root}
	visited := make(map[*runtimeObject]struct{})
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if _, exists := visited[current]; exists {
			continue
		}
		visited[current] = struct{}{}
		if current.header.Kind == objectKindFile && current.header.ObjectID == targetID && current.header.ParentID != nil {
			nlink++
		}
		if current.header.Kind == objectKindHardLink &&
			current.header.TargetID != nil && *current.header.TargetID == targetID {
			nlink++
		}
		if current.header.Kind == objectKindDirectory {
			for _, child := range current.children {
				queue = append(queue, child)
			}
		}
	}
	return nlink
}
