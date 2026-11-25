package orchestration

import (
	"context"
	"errors"
	"github.com/tetsup/flexraid/distribution"
)

func writeDistributionStripeForTest(layer *distribution.Distribution, request distribution.WriteStripeRequest) (distribution.WriteStripesResponse, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	request.Context = ctx
	stripes := []distribution.WriteStripeRequest{request}
	if request.StripeID != 0 {
		header := distribution.WriteStripeRequest{Context: ctx, ObjectID: request.ObjectID, StripeID: 0, ChunkSize: request.ChunkSize, Layout: request.Layout, IOIndexes: append([]int(nil), request.IOIndexes...)}
		info, err := layer.GetStripeInfo(distribution.GetStripeInfoRequest{Context: ctx, ObjectID: request.ObjectID, StripeID: 0})
		if err == nil {
			read, e := layer.ReadStripe(distribution.ReadStripeRequest{Context: ctx, ObjectID: request.ObjectID, StripeID: 0, ChunkSize: info.ChunkSize})
			if e != nil {
				return distribution.WriteStripesResponse{}, e
			}
			header.ChunkSize, header.Layout, header.Data = info.ChunkSize, info.Layout, read.Data
			if p, e := layer.StripeIOIndexes(distribution.StripeIOIndexesRequest{ObjectID: request.ObjectID, StripeID: 0}); e == nil {
				header.IOIndexes = p.IOIndexes
			}
		} else if !errors.Is(err, distribution.ErrStripeNotFound) {
			return distribution.WriteStripesResponse{}, err
		}
		stripes = append(stripes, header)
	}
	return layer.WriteStripes(distribution.WriteStripesRequest{Context: ctx, ObjectID: request.ObjectID, Stripes: stripes})
}
