package distribution

import (
	"context"
	"errors"
)

func writeStripeForTest(layer *Distribution, request WriteStripeRequest) (WriteStripesResponse, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	request.Context = ctx
	stripes := []WriteStripeRequest{request}
	if request.StripeID != 0 {
		header := WriteStripeRequest{Context: ctx, ObjectID: request.ObjectID, StripeID: 0, ChunkSize: request.ChunkSize, Layout: request.Layout, IOIndexes: append([]int(nil), request.IOIndexes...)}
		info, err := layer.GetStripeInfo(GetStripeInfoRequest{Context: ctx, ObjectID: request.ObjectID, StripeID: 0})
		if err == nil {
			read, e := layer.ReadStripe(ReadStripeRequest{Context: ctx, ObjectID: request.ObjectID, StripeID: 0, ChunkSize: request.ChunkSize})
			if e != nil {
				return WriteStripesResponse{}, e
			}
			header.ChunkSize, header.Layout, header.Data = request.ChunkSize, info.Layout, read.Data
			if p, e := layer.StripeIOIndexes(StripeIOIndexesRequest{ObjectID: request.ObjectID, StripeID: 0}); e == nil {
				header.IOIndexes = p.IOIndexes
			}
		} else if !errors.Is(err, ErrStripeNotFound) {
			return WriteStripesResponse{}, err
		}
		stripes = append(stripes, header)
	}
	return layer.WriteStripes(WriteStripesRequest{Context: ctx, ObjectID: request.ObjectID, Stripes: stripes})
}
