package io

import (
 "context"
 "errors"
 "time"
 "github.com/tetsup/flexraid/storage"
)

type ListChunksRequest struct { Context context.Context; Callback func(ScanEntry) error }
type ListChunksResponse struct { Entries int }

// ListChunks enumerates all physical chunk files without interpreting their contents.
func (layer *IO) ListChunks(request ListChunksRequest) (response ListChunksResponse, err error) {
 started := operationStarted()
 defer func() { logResult(layer.logger, "ListChunks", err, "duration", time.Since(started), "entries", response.Entries) }()
 if request.Callback == nil { return response, errors.New("list chunks callback is nil") }
 if err = request.Context.Err(); err != nil { return response, err }
 response.Entries, err = layer.walkFiles(request.Context, func(storage.FileKey) bool { return true }, request.Callback)
 return response, err
}
