package orchestration

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"

	"github.com/tetsup/flexraid/pkg/shared"
)

type ReadDirRequest struct {
	Context  context.Context
	ObjectID shared.ObjectID
}

type DirectoryEntry struct {
	ObjectID shared.ObjectID
	Kind     shared.ObjectKind
	Name     string
}

type ReadDirResponse struct{ Entries []DirectoryEntry }

// ReadDir returns the immediate children of a directory in name order.
func (layer *Orchestration) ReadDir(request ReadDirRequest) (ReadDirResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.readDirLocked(request)
}

func (layer *Orchestration) readDirLocked(request ReadDirRequest) (ReadDirResponse, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ReadDirResponse{}, err
	}
	object, exists := layer.objects[request.ObjectID]
	if !exists || object.header.Deleted {
		return ReadDirResponse{}, os.ErrNotExist
	}
	if object.header.Kind != objectKindDirectory {
		return ReadDirResponse{}, fmt.Errorf("object %s: %w", request.ObjectID.String(), errNotDirectory)
	}
	entries := make([]DirectoryEntry, 0, len(object.children))
	for name, child := range object.children {
		entries = append(entries, DirectoryEntry{ObjectID: child.header.ObjectID, Kind: child.header.Kind, Name: name})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	if layer.logger != nil {
		layer.logger.Debug("orchestration ReadDir completed", slog.String("object_id", request.ObjectID.String()), slog.Int("entries", len(entries)))
	}
	return ReadDirResponse{Entries: entries}, nil
}
