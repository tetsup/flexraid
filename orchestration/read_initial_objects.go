package orchestration

import (
	"context"
	"fmt"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type initialObject struct {
	objectID shared.ObjectID
	data     []byte
}

func readInitialObjects(ctx context.Context, layer *distribution.Distribution) ([]initialObject, error) {
	objects, err := layer.ListObjects(distribution.ListObjectsRequest{Context: ctx})
	if err != nil {
		return nil, fmt.Errorf("list objects for initialization: %w", err)
	}

	initialObjects := make([]initialObject, 0, len(objects.ObjectIDs))
	for _, objectID := range objects.ObjectIDs {
		data, err := readInitialHeaderData(ctx, layer, objectID)
		if err != nil {
			return nil, err
		}
		objectHeader, err := decodeInitialHeader(objectID, data)
		if err != nil {
			return nil, err
		}
		if objectHeader.Kind == objectKindSymlink {
			if objectHeader.Size > uint64(int(^uint(0)>>1)-headerSize) {
				// Preserve the Header so initialization can exclude and protect the
				// symlink instead of making the entire namespace unavailable.
				initialObjects = append(initialObjects, initialObject{objectID: objectID, data: data})
				continue
			}
			targetData, targetErr := readInitialObjectData(ctx, layer, objectID, headerSize+int(objectHeader.Size))
			if targetErr != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				// A broken symlink target is a localized namespace issue. Keep
				// its Header; initialize will mark the target unavailable.
			} else {
				data = targetData
			}
		}
		initialObjects = append(initialObjects, initialObject{objectID: objectID, data: data})
	}
	return initialObjects, nil
}

func readInitialHeaderData(ctx context.Context, layer *distribution.Distribution, objectID shared.ObjectID) ([]byte, error) {
	return readInitialObjectData(ctx, layer, objectID, headerSize)
}

// readInitialObjectData reads the leading bytes of an object across stripe boundaries.
func readInitialObjectData(ctx context.Context, layer *distribution.Distribution, objectID shared.ObjectID, size int) ([]byte, error) {
	if size < 0 {
		return nil, fmt.Errorf("negative object data size")
	}
	data := make([]byte, 0, size)
	for stripeID := uint64(0); len(data) < size; stripeID++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		read, err := layer.ReadStripe(distribution.ReadStripeRequest{
			Context:   ctx,
			ObjectID:  objectID,
			StripeID:  storage.StripeID(stripeID),
			ChunkSize: 4096,
		})
		if err != nil {
			return nil, fmt.Errorf("read object %s at stripe %d: %w", objectID.String(), stripeID, err)
		}
		if len(read.Data) == 0 {
			return nil, fmt.Errorf("%w: empty stripe while reading object %s", errInvalidHeader, objectID.String())
		}
		data = append(data, read.Data...)
	}
	return data[:size], nil
}
