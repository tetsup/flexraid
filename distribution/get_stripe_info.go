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
	unlock := d.lockStripe(key)
	defer unlock()
	if err := d.cacheStripeLocked(ctx, key); err != nil {
		return GetStripeInfoResponse{}, err
	}
	d.mapMu.Lock()
	defer d.mapMu.Unlock()
	stripes := d.objects[request.ObjectID]
	if stripes == nil || stripes[request.StripeID] == nil {
		return GetStripeInfoResponse{}, ErrStripeNotFound
	}
	info := stripes[request.StripeID]
	if info == nil {
		return GetStripeInfoResponse{}, fmt.Errorf("stripe %d has no selected version: %w", request.StripeID, ErrStripeNotFound)
	}
	return GetStripeInfoResponse{
		Version:   info.version,
		Layout:    info.method.layout(),
		ChunkSize: info.chunkSize,
	}, nil
}
