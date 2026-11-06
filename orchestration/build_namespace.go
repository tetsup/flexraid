package orchestration

import "github.com/tetsup/flexraid/pkg/shared"

func buildNamespace(objects map[shared.ObjectID]*runtimeObject, protected map[shared.ObjectID]struct{}) {
	childrenByParent := make(map[shared.ObjectID]map[string][]*runtimeObject)

	for id, object := range objects {
		if object.header.Deleted {
			continue
		}
		switch object.header.Kind {
		case objectKindFile:
			object.file = &runtimeFile{objectID: id, header: object.header, stripeCache: make(map[uint64]*stripeCacheEntry)}
		case objectKindHardLinkedFile:
			object.file = &runtimeFile{objectID: id, header: object.header, stripeCache: make(map[uint64]*stripeCacheEntry)}
		case objectKindDirectory:
			object.children = make(map[string]*runtimeObject)
		}
	}

	for id, object := range objects {
		h := object.header
		if h.Deleted || h.Kind != objectKindHardLink {
			continue
		}
		targetID := *h.TargetID
		target, exists := objects[targetID]
		if !exists || target.header.Deleted || target.header.Kind != objectKindHardLinkedFile || target.file == nil {
			protected[id] = struct{}{}
			if exists && !target.header.Deleted {
				protected[targetID] = struct{}{}
			}
			continue
		}
		object.file = target.file
	}

	for id, object := range objects {
		h := object.header
		if h.Deleted || (h.Kind == objectKindHardLinkedFile && h.ParentID == nil) || (h.Kind == objectKindDirectory && h.ParentID == nil) {
			continue
		}
		if h.Kind == objectKindSymlink && !object.linkTargetLoaded {
			protected[id] = struct{}{}
			continue
		}
		if h.Kind == objectKindHardLink && object.file == nil {
			continue
		}
		if h.ParentID == nil {
			protected[id] = struct{}{}
			continue
		}
		parentID := *h.ParentID
		parent, exists := objects[parentID]
		if !exists || parent.header.Deleted || parent.header.Kind != objectKindDirectory || parent.children == nil {
			protected[id] = struct{}{}
			continue
		}
		if childrenByParent[parentID] == nil {
			childrenByParent[parentID] = make(map[string][]*runtimeObject)
		}
		childrenByParent[parentID][h.Name] = append(childrenByParent[parentID][h.Name], object)
	}

	for parentID, names := range childrenByParent {
		parent := objects[parentID]
		for name, children := range names {
			if len(children) != 1 {
				for _, child := range children {
					protected[child.header.ObjectID] = struct{}{}
				}
				continue
			}
			parent.children[name] = children[0]
		}
	}
}
