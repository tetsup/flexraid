package orchestration

import (
	"errors"
	"fmt"

	"github.com/tetsup/flexraid/pkg/shared"
)

var errInvalidNamespace = errors.New("invalid namespace")

func (layer *Orchestration) initialize(initialObjects []initialObject) error {
	state := &Orchestration{
		logger:           layer.logger,
		headers:          make(map[shared.ObjectID]header, len(initialObjects)),
		objects:          make(map[shared.ObjectID]*runtimeObject, len(initialObjects)),
		deletionQueue:    make(map[shared.ObjectID]struct{}),
		protectedObjects: make(map[shared.ObjectID]struct{}),
	}

	for _, initial := range initialObjects {
		objectHeader, err := decodeInitialHeader(initial.objectID, initial.data)
		if err != nil {
			return err
		}
		if _, exists := state.headers[initial.objectID]; exists {
			return fmt.Errorf("%w: duplicate object ID %s", errInvalidHeader, initial.objectID.String())
		}
		state.headers[initial.objectID] = objectHeader
		state.objects[initial.objectID] = &runtimeObject{header: objectHeader}
		if objectHeader.Deleted {
			state.deletionQueue[initial.objectID] = struct{}{}
		}
	}

	buildNamespace(state.objects, state.protectedObjects)
	if err := state.inspect(); err != nil {
		return err
	}

	layer.headers = state.headers
	layer.objects = state.objects
	layer.root = state.root
	layer.deletionQueue = state.deletionQueue
	layer.protectedObjects = state.protectedObjects
	layer.warnings = state.warnings
	return nil
}

func decodeInitialHeader(objectID shared.ObjectID, data []byte) (header, error) {
	var objectHeader header
	if len(data) < headerSize {
		return objectHeader, fmt.Errorf("%w: incomplete header for %s", errInvalidHeader, objectID.String())
	}
	if err := objectHeader.UnmarshalBinary(data[:headerSize]); err != nil {
		return header{}, fmt.Errorf("decode object header %s: %w", objectID.String(), err)
	}
	if objectHeader.ObjectID != objectID {
		return header{}, fmt.Errorf("%w: object ID mismatch for %s", errInvalidHeader, objectID.String())
	}
	return objectHeader, nil
}
