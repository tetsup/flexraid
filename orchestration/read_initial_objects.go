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
		initialObjects = append(initialObjects, initialObject{objectID: objectID, data: data})
	}
	return initialObjects, nil
}

func readInitialHeaderData(ctx context.Context, layer *distribution.Distribution, objectID shared.ObjectID) ([]byte, error) {
	headerData := make([]byte, 0, headerSize)
	for stripeID := uint64(0); len(headerData) < headerSize; stripeID++ {
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
			return nil, fmt.Errorf("read object header %s at stripe %d: %w", objectID.String(), stripeID, err)
		}
		if len(read.Data) == 0 {
			return nil, fmt.Errorf("%w: empty stripe while reading header for %s", errInvalidHeader, objectID.String())
		}
		headerData = append(headerData, read.Data...)
	}
	return headerData[:headerSize], nil
}
