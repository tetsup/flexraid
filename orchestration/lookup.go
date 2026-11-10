package orchestration

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/tetsup/flexraid/pkg/shared"
)

type LookupRequest struct {
	Context  context.Context
	ParentID shared.ObjectID
	Name     string
}

type LookupResponse struct {
	ObjectID shared.ObjectID
	Kind     shared.ObjectKind
	Name     string
}

// Lookup resolves a child name in a directory.
func (layer *Orchestration) Lookup(request LookupRequest) (LookupResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.lookupLocked(request)
}

func (layer *Orchestration) lookupLocked(request LookupRequest) (LookupResponse, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return LookupResponse{}, err
	}
	if request.Name == "" || request.Name == "." || request.Name == ".." || strings.Contains(request.Name, "/") {
		return LookupResponse{}, fmt.Errorf("invalid child name %q", request.Name)
	}
	parent, exists := layer.objects[request.ParentID]
	if !exists || parent.header.Deleted {
		return LookupResponse{}, os.ErrNotExist
	}
	if parent.header.Kind != objectKindDirectory {
		return LookupResponse{}, fmt.Errorf("parent %s: %w", request.ParentID.String(), errNotDirectory)
	}
	child, exists := parent.children[request.Name]
	if !exists {
		return LookupResponse{}, os.ErrNotExist
	}
	response := LookupResponse{ObjectID: child.header.ObjectID, Kind: child.header.Kind, Name: child.header.Name}
	if layer.logger != nil {
		layer.logger.Debug("orchestration Lookup completed", slog.String("parent_id", request.ParentID.String()), slog.String("name", request.Name), slog.String("object_id", response.ObjectID.String()))
	}
	return response, nil
}
