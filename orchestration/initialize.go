package orchestration

import (
	"fmt"

	"github.com/tetsup/flexraid/pkg/shared"
)

func (layer *Orchestration) initialize(initialObjects []initialObject) error {
	headers := make(map[shared.ObjectID]header, len(initialObjects))
	deletionQueue := make(map[shared.ObjectID]struct{})

	for _, initial := range initialObjects {
		objectHeader, err := decodeInitialHeader(initial.objectID, initial.data)
		if err != nil {
			return err
		}
		if _, exists := headers[initial.objectID]; exists {
			return fmt.Errorf("%w: duplicate object ID %s", errInvalidHeader, initial.objectID.String())
		}
		headers[initial.objectID] = objectHeader
		if objectHeader.Deleted {
			deletionQueue[initial.objectID] = struct{}{}
		}
	}

	layer.headers = headers
	layer.deletionQueue = deletionQueue
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
