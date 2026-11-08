package orchestration

import (
	"errors"
	"fmt"

	"github.com/tetsup/flexraid/pkg/shared"
)

var errInvalidNamespace = errors.New("invalid namespace")

func (layer *Orchestration) inspect() error {
	roots := make([]*runtimeObject, 0, 1)
	for _, object := range layer.objects {
		h := object.header
		if !h.Deleted && h.Kind == objectKindDirectory && h.ParentID == nil {
			roots = append(roots, object)
		}
	}
	if len(roots) != 1 {
		return fmt.Errorf("%w: expected exactly one root directory, found %d", errInvalidNamespace, len(roots))
	}
	root := roots[0]

	reachable := make(map[shared.ObjectID]struct{}, len(layer.objects))
	reachableFiles := make(map[shared.ObjectID]struct{})
	queue := []*runtimeObject{root}
	for len(queue) > 0 {
		object := queue[0]
		queue = queue[1:]
		id := object.header.ObjectID
		if _, exists := reachable[id]; exists {
			continue
		}
		reachable[id] = struct{}{}
		if object.header.Kind == objectKindHardLink && object.file != nil {
			reachableFiles[object.file.objectID] = struct{}{}
		}
		for _, child := range object.children {
			queue = append(queue, child)
		}
	}

	for id, object := range layer.objects {
		if object.header.Deleted {
			continue
		}
		switch object.header.Kind {
		case objectKindFile:
			_, directlyReachable := reachable[id]
			_, referenced := reachableFiles[id]
			if !directlyReachable && !referenced {
				layer.protectedObjects[id] = struct{}{}
				layer.addInitializationWarning(id, "File has neither a reachable parent entry nor a reachable HardLink")
			}
		default:
			if _, exists := reachable[id]; !exists {
				layer.protectedObjects[id] = struct{}{}
				layer.addInitializationWarning(id, "Object is not reachable from root or was excluded from Namespace")
			}
		}
	}

	for id := range layer.protectedObjects {
		delete(layer.deletionQueue, id)
	}
	layer.root = root
	return nil
}

func (layer *Orchestration) addInitializationWarning(id shared.ObjectID, reason string) {
	for _, warning := range layer.warnings {
		if warning.objectID == id && warning.reason == reason {
			return
		}
	}
	layer.warnings = append(layer.warnings, initializationWarning{objectID: id, reason: reason})
	if layer.logger != nil {
		layer.logger.Warn("orchestration initialization skipped inconsistent object", "object_id", id.String(), "reason", reason)
	}
}
