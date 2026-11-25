package distribution

import (
	"context"
	"fmt"
	"time"

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
func (d *Distribution) GetStripeInfo(request GetStripeInfoRequest) (response GetStripeInfoResponse, err error) {
	started := time.Now()
	defer func() {
		logOperation(d, "GetStripeInfo", started, err, "object_id", request.ObjectID.String(), "stripe_id", request.StripeID)
	}()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	key := stripeIDKey{objectID: request.ObjectID, stripeID: request.StripeID}
	d.mapMu.RLock()
	defer d.mapMu.RUnlock()
	stripes := d.objects[request.ObjectID]
	if stripes == nil || stripes[request.StripeID] == nil {
		err = ErrStripeNotFound
		return GetStripeInfoResponse{}, err
	}
	info := stripes[request.StripeID]
	if info == nil {
		err = fmt.Errorf("stripe %d has no selected version: %w", request.StripeID, ErrStripeNotFound)
		return GetStripeInfoResponse{}, err
	}
	result := GetStripeInfoResponse{
		Version:   info.version,
		Layout:    info.method.layout(),
		ChunkSize: info.chunkSize,
	}
	return result, nil
}
