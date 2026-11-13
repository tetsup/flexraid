package distribution

import (
	"context"
	"sort"

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
	if request.Context == nil {
		request.Context = context.Background()
	}
	if err = request.Context.Err(); err != nil {
		return response, err
	}
	for id, info := range d.objects[request.ObjectID] {
		if err = request.Context.Err(); err != nil {
			return ListStripeInfosResponse{}, err
		}
		if info == nil {
			continue
		}
		response.Stripes = append(response.Stripes, StripeInfo{
			StripeID: id, Version: info.version, Layout: info.method.layout(), ChunkSize: info.chunkSize,
		})
	}
	sort.Slice(response.Stripes, func(i, j int) bool {
		return response.Stripes[i].StripeID < response.Stripes[j].StripeID
	})
	return response, nil
}
