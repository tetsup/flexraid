package io

import (
	"context"
	"errors"
	"time"

	"github.com/tetsup/flexraid/storage"
)

type ScanHeadersRequest struct {
	Context  context.Context
	Callback func(ScanEntry) error
}
type ScanHeadersResponse struct{ Entries int }

// ScanHeaders enumerates stripe-zero physical files and returns unparsed file bytes.
func (layer *IO) ScanHeaders(req ScanHeadersRequest) (res ScanHeadersResponse, err error) {
	started := operationStarted()
	var readErrors uint64
	defer func() {
		layer.recordOperation(OperationScanHeaders, started, err, readErrors)
		logResult(layer.logger, "ScanHeaders", err, "duration", time.Since(started), "entries", res.Entries)
	}()
	if req.Callback == nil {
		return res, errors.New("scan headers callback is nil")
	}
	if err = req.Context.Err(); err != nil {
		return res, err
	}
	layer.recordAccess()
	defer layer.recordAccess()
	res.Entries, err = layer.walkFiles(
		req.Context,
		func(key storage.FileKey) bool { return key.StripeID == 0 },
		req.Callback,
		func(key storage.FileKey) {
			readErrors++
			layer.recordReadErrorFile(OperationScanHeaders, key)
		},
	)
	return res, err
}
