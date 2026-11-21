package distribution

import (
	"context"
	"sort"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type StripeInfo struct {
	StripeID  storage.StripeID
	Version   storage.StripeVersion
	Layout    Layout
	ChunkSize uint32
}

type ListStripeInfosRequest struct {
	Context  context.Context
	ObjectID shared.ObjectID
}

type ListStripeInfosResponse struct {
	Stripes []StripeInfo
}

// ListStripeInfos returns metadata for every stripe with a selected recoverable version.
func (d *Distribution) ListStripeInfos(request ListStripeInfosRequest) (response ListStripeInfosResponse, err error) {
	started := time.Now()
	defer func(){ logOperation(d, "ListStripeInfos", started, err, "object_id", request.ObjectID.String(), "stripes", len(response.Stripes)) }()
	if request.Context == nil {
		request.Context = context.Background()
	}
	if err = request.Context.Err(); err != nil {
		return response, err
	}
	d.mapMu.Lock()
	ids := make([]storage.StripeID, 0, len(d.objects[request.ObjectID]))
	for id := range d.objects[request.ObjectID] {
		ids = append(ids, id)
	}
	d.mapMu.Unlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if err = request.Context.Err(); err != nil {
			return ListStripeInfosResponse{}, err
		}
		key := stripeIDKey{objectID: request.ObjectID, stripeID: id}
		unlockStripe, unlockMap := d.lockStripe(key)
		if err = d.cacheStripeLocked(request.Context, key); err != nil {
			unlockMap()
			unlockStripe()
			return ListStripeInfosResponse{}, err
		}
		info := d.objects[request.ObjectID][id]
		if info != nil {
			response.Stripes = append(response.Stripes, StripeInfo{StripeID: id, Version: info.version, Layout: info.method.layout(), ChunkSize: info.chunkSize})
		}
		unlockMap()
		unlockStripe()
	}
	sort.Slice(response.Stripes, func(i, j int) bool {
		return response.Stripes[i].StripeID < response.Stripes[j].StripeID
	})
	return response, nil
}
