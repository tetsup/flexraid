package orchestration

import "github.com/tetsup/flexraid/pkg/shared"

// getObject copies an Object pointer while holding the map lock only for lookup.
// The returned Object is protected by its Object lock, not by mapMu.
func (layer *Orchestration) getObject(id shared.ObjectID) (*runtimeObject, bool) {
	layer.mapMu.RLock()
	object, exists := layer.objects[id]
	layer.mapMu.RUnlock()
	return object, exists
}

func (layer *Orchestration) object(id shared.ObjectID) *runtimeObject {
	object, _ := layer.getObject(id)
	return object
}

func (layer *Orchestration) putObject(id shared.ObjectID, object *runtimeObject) {
	layer.mapMu.Lock()
	if layer.objects == nil {
		layer.objects = make(map[shared.ObjectID]*runtimeObject)
	}
	layer.objects[id] = object
	layer.mapMu.Unlock()
}

func (layer *Orchestration) deleteObject(id shared.ObjectID) {
	layer.mapMu.Lock()
	delete(layer.objects, id)
	layer.mapMu.Unlock()
}

func (layer *Orchestration) getHeader(id shared.ObjectID) (header, bool) {
	layer.mapMu.RLock()
	value, exists := layer.headers[id]
	layer.mapMu.RUnlock()
	return value, exists
}

func (layer *Orchestration) headerValue(id shared.ObjectID) header {
	value, _ := layer.getHeader(id)
	return value
}

func (layer *Orchestration) putHeader(id shared.ObjectID, value header) {
	layer.mapMu.Lock()
	if layer.headers == nil {
		layer.headers = make(map[shared.ObjectID]header)
	}
	layer.headers[id] = value
	layer.mapMu.Unlock()
}

func (layer *Orchestration) deleteHeader(id shared.ObjectID) {
	layer.mapMu.Lock()
	delete(layer.headers, id)
	layer.mapMu.Unlock()
}

// objectHeaderSnapshot returns independent map structure; Object pointers remain
// protected by their per-Object locks.
func (layer *Orchestration) objectHeaderSnapshot() (map[shared.ObjectID]*runtimeObject, map[shared.ObjectID]header) {
	layer.mapMu.RLock()
	objects := make(map[shared.ObjectID]*runtimeObject, len(layer.objects))
	for id, object := range layer.objects {
		objects[id] = object
	}
	headers := make(map[shared.ObjectID]header, len(layer.headers))
	for id, value := range layer.headers {
		headers[id] = value
	}
	layer.mapMu.RUnlock()
	return objects, headers
}

func (layer *Orchestration) objectHeaderCounts() (int, int) {
	layer.mapMu.RLock()
	objects, headers := len(layer.objects), len(layer.headers)
	layer.mapMu.RUnlock()
	return objects, headers
}
