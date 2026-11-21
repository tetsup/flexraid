package orchestration

import (
	"context"
	"fmt"
	"sort"

	"github.com/tetsup/flexraid/pkg/shared"
)

// NamespaceObjectInspection is a detached view of one persisted logical object.
type NamespaceObjectInspection struct {
	ObjectID shared.ObjectID
	ParentID *shared.ObjectID
	TargetID *shared.ObjectID
	Kind shared.ObjectKind
	Name string
	Mode uint32
	UID uint32
	GID uint32
	Size uint64
	Deleted bool
	AtimeUnixNano int64
	MtimeUnixNano int64
	CtimeUnixNano int64
	OpenCount uint64
	Reachable bool
	Protected bool
}

// NamespaceInspectionWarning describes an inconsistency detected during startup.
type NamespaceInspectionWarning struct {
	ObjectID shared.ObjectID
	Reason string
}

// InspectNamespaceRequest requests a consistent logical namespace snapshot.
type InspectNamespaceRequest struct { Context context.Context }

// InspectNamespaceResponse contains logical objects and initialization warnings.
type InspectNamespaceResponse struct {
	RootID shared.ObjectID
	Objects []NamespaceObjectInspection
	Warnings []NamespaceInspectionWarning
	ProtectedObjectCount uint64
	DeletionQueueCount uint64
}

// InspectNamespace returns a detached audit snapshot. Management serializes this
// call with filesystem operations so the returned object graph is consistent.
func (layer *Orchestration) InspectNamespace(request InspectNamespaceRequest) (InspectNamespaceResponse, error) {
	if request.Context == nil { return InspectNamespaceResponse{}, fmt.Errorf("context is required") }
	if err := request.Context.Err(); err != nil { return InspectNamespaceResponse{}, err }
	result := InspectNamespaceResponse{Objects: make([]NamespaceObjectInspection,0,len(layer.objects)), Warnings: make([]NamespaceInspectionWarning,0,len(layer.warnings))}
	if layer.root != nil { result.RootID = layer.root.header.ObjectID }
	for id, object := range layer.objects {
		if err := request.Context.Err(); err != nil { return InspectNamespaceResponse{}, err }
		h := object.header
		entry := NamespaceObjectInspection{ObjectID:id, ParentID:cloneObjectID(h.ParentID), TargetID:cloneObjectID(h.TargetID), Kind:h.Kind, Name:h.Name, Mode:h.Mode, UID:h.UID, GID:h.GID, Size:h.Size, Deleted:h.Deleted, AtimeUnixNano:h.Atime.UnixNano(), MtimeUnixNano:h.Mtime.UnixNano(), CtimeUnixNano:h.Ctime.UnixNano()}
		if object.file != nil { entry.OpenCount = object.file.openCount }
		if _, ok := layer.protectedObjects[id]; ok { entry.Protected = true }
		if !h.Deleted {
			if h.ParentID == nil { entry.Reachable = object == layer.root
			} else if parent := layer.objects[*h.ParentID]; parent != nil && parent.children[h.Name] == object { entry.Reachable = true }
		}
		result.Objects = append(result.Objects, entry)
	}
	for _, warning := range layer.warnings { result.Warnings = append(result.Warnings, NamespaceInspectionWarning{ObjectID:warning.objectID,Reason:warning.reason}) }
	result.ProtectedObjectCount = uint64(len(layer.protectedObjects))
	result.DeletionQueueCount = uint64(len(layer.deletionQueue))
	sort.Slice(result.Objects,func(i,j int)bool{return result.Objects[i].ObjectID.String()<result.Objects[j].ObjectID.String()})
	sort.Slice(result.Warnings,func(i,j int)bool{if result.Warnings[i].ObjectID!=result.Warnings[j].ObjectID{return result.Warnings[i].ObjectID.String()<result.Warnings[j].ObjectID.String()};return result.Warnings[i].Reason<result.Warnings[j].Reason})
	return result,nil
}

func cloneObjectID(id *shared.ObjectID) *shared.ObjectID {
	if id == nil { return nil }
	copyID := *id
	return &copyID
}
