package orchestration

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

// NamespaceObjectInspection is a detached view of one persisted logical object.
type NamespaceObjectInspection struct {
	ObjectID  shared.ObjectID
	ParentID  *shared.ObjectID
	TargetID  *shared.ObjectID
	Kind      shared.ObjectKind
	Name      string
	Mode      uint32
	UID       uint32
	GID       uint32
	Size      uint64
	Deleted   bool
	Atime     time.Time
	Mtime     time.Time
	Ctime     time.Time
	OpenCount uint64
	Reachable bool
	Protected bool
}

// NamespaceInspectionWarning describes an inconsistency detected during startup.
type NamespaceInspectionWarning struct {
	ObjectID shared.ObjectID
	Reason   string
}

// InspectNamespaceRequest requests a consistent logical namespace snapshot.
type InspectNamespaceRequest struct{ Context context.Context }

// InspectNamespaceResponse contains logical objects and initialization warnings.
type InspectNamespaceResponse struct {
	RootID               shared.ObjectID
	Objects              []NamespaceObjectInspection
	Warnings             []NamespaceInspectionWarning
	ProtectedObjectCount uint64
	DeletionQueueCount   uint64
}

// InspectNamespace returns a detached audit snapshot. Management serializes this
// call with filesystem operations so the returned object graph is consistent.
func (layer *Orchestration) InspectNamespace(request InspectNamespaceRequest) (InspectNamespaceResponse, error) {
	if request.Context == nil {
		return InspectNamespaceResponse{}, fmt.Errorf("context is required")
	}
	if err := request.Context.Err(); err != nil {
		return InspectNamespaceResponse{}, err
	}
	result := InspectNamespaceResponse{Objects: make([]NamespaceObjectInspection, 0, len(layer.objects)), Warnings: make([]NamespaceInspectionWarning, 0, len(layer.warnings))}
	if layer.root != nil {
		result.RootID = layer.root.header.ObjectID
	}
	reachable := make(map[shared.ObjectID]struct{}, len(layer.objects))
	reachableFiles := make(map[shared.ObjectID]struct{})
	queue := []*runtimeObject{layer.root}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == nil {
			continue
		}
		id := current.header.ObjectID
		if _, exists := reachable[id]; exists {
			continue
		}
		reachable[id] = struct{}{}
		if current.header.Kind == objectKindHardLink && current.file != nil {
			reachableFiles[current.file.objectID] = struct{}{}
		}
		for _, child := range current.children {
			queue = append(queue, child)
		}
	}
	for id, object := range layer.objects {
		if err := request.Context.Err(); err != nil {
			return InspectNamespaceResponse{}, err
		}
		h := object.header
		entry := NamespaceObjectInspection{ObjectID: id, ParentID: cloneObjectID(h.ParentID), TargetID: cloneObjectID(h.TargetID), Kind: h.Kind, Name: h.Name, Mode: h.Mode, UID: h.UID, GID: h.GID, Size: h.Size, Deleted: h.Deleted, Atime: h.Atime, Mtime: h.Mtime, Ctime: h.Ctime}
		if object.file != nil {
			entry.OpenCount = object.file.openCount
		}
		if _, ok := layer.protectedObjects[id]; ok {
			entry.Protected = true
		}
		if !h.Deleted {
			_, directlyReachable := reachable[id]
			_, hardLinkReachable := reachableFiles[id]
			entry.Reachable = directlyReachable || hardLinkReachable
		}
		result.Objects = append(result.Objects, entry)
	}
	for _, warning := range layer.warnings {
		result.Warnings = append(result.Warnings, NamespaceInspectionWarning{ObjectID: warning.objectID, Reason: warning.reason})
	}
	result.ProtectedObjectCount = uint64(len(layer.protectedObjects))
	result.DeletionQueueCount = uint64(len(layer.deletionQueue))
	sort.Slice(result.Objects, func(i, j int) bool { return result.Objects[i].ObjectID.String() < result.Objects[j].ObjectID.String() })
	sort.Slice(result.Warnings, func(i, j int) bool {
		if result.Warnings[i].ObjectID != result.Warnings[j].ObjectID {
			return result.Warnings[i].ObjectID.String() < result.Warnings[j].ObjectID.String()
		}
		return result.Warnings[i].Reason < result.Warnings[j].Reason
	})
	return result, nil
}

func cloneObjectID(id *shared.ObjectID) *shared.ObjectID {
	if id == nil {
		return nil
	}
	copyID := *id
	return &copyID
}
