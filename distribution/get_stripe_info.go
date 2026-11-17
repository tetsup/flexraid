package distribution

import (
	"context"
	"fmt"

	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type GetStripeInfoRequest struct {
	Context  context.Context
	ObjectID shared.ObjectID
	StripeID storage.StripeID
}

type GetStripeInfoResponse struct {
	Version   storage.StripeVersion
	Layout    Layout
	ChunkSize uint32
}

// GetStripeInfo returns the newest recoverable version's persisted layout metadata.
func (d *Distribution) GetStripeInfo(request GetStripeInfoRequest) (GetStripeInfoResponse, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	key := stripeIDKey{objectID: request.ObjectID, stripeID: request.StripeID}
	unlockStripe, unlockMap := d.lockStripe(key)
	defer unlockStripe()
	if err := d.cacheStripeLocked(ctx, key); err != nil {
		unlockMap()
		return GetStripeInfoResponse{}, err
	}
	stripes := d.objects[request.ObjectID]
	if stripes == nil || stripes[request.StripeID] == nil {
		unlockMap()
		return GetStripeInfoResponse{}, ErrStripeNotFound
	}
	info := stripes[request.StripeID]
	if info == nil {
		unlockMap()
		return GetStripeInfoResponse{}, fmt.Errorf("stripe %d has no selected version: %w", request.StripeID, ErrStripeNotFound)
	}
	result := GetStripeInfoResponse{
		Version:   info.version,
		Layout:    info.method.layout(),
		ChunkSize: info.chunkSize,
	}
	unlockMap()
	return result, nil
}
