package distribution

import (
	"context"
	"fmt"
	"time"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type WriteStripeRequest struct {
	Context   context.Context
	ObjectID  shared.ObjectID
	StripeID  storage.StripeID
	ChunkSize uint32
	Layout    Layout
	IOIndexes []int
	Data      []byte
}
type WriteStripeResponse struct{ Version storage.StripeVersion }

func (d *Distribution) WriteStripe(request WriteStripeRequest) (response WriteStripeResponse, err error) {
	started := time.Now()
	defer func() {
		logOperation(d.logger, "WriteStripe", started, err, "object_id", request.ObjectID.String(), "stripe_id", request.StripeID, "bytes", len(request.Data))
	}()
	if request.ChunkSize == 0 {
		return response, fmt.Errorf("chunk size must be positive")
	}
	if err = request.Context.Err(); err != nil {
		return response, err
	}
	method, err := request.Layout.method()
	if err != nil {
		return response, err
	}
	required := int(method.numChunks)
	if method.distributionType == distributionParity {
		required++
	}
	if err = validateIOIndexes(request.IOIndexes, len(d.ios), required); err != nil {
		return response, err
	}
	if method.distributionType == distributionMirror && len(request.IOIndexes) < int(method.numChunks) {
		return response, errInsufficientChunks
	}
	if method.distributionType == distributionParity && len(request.IOIndexes) < required {
		return response, errInsufficientChunks
	}
	key := stripeIDKey{objectID: request.ObjectID, stripeID: request.StripeID}
	version := d.nextVersion(key)
	chunks := make(map[chunkNo][]byte, required)
	if method.distributionType == distributionMirror {
		for i := uint8(0); i < method.numChunks; i++ {
			chunks[chunkNo(i)] = append([]byte(nil), request.Data...)
		}
	} else {
		chunks = splitStripeWithSize(request.Data, method.numChunks, int(request.ChunkSize))
		chunks[parityFlag] = generateParity(orderedDataChunks(chunks, method.numChunks))
	}
	written := make(map[chunkNo]chunkLocation, len(chunks))
	savePartial := func() {
		if len(written) > 0 {
			versions := d.versions[key]
			if versions == nil {
				versions = make(map[storage.StripeVersion]*versionInfo)
				d.versions[key] = versions
			}
			versions[version] = &versionInfo{method: method, chunks: cloneChunks(written), recoverable: false}
		}
	}
	for _, no := range orderedChunkNos(chunks) {
		if err = request.Context.Err(); err != nil {
			savePartial()
			return response, err
		}
		ioIndex := request.IOIndexes[int(no.index())]
		if no.isParity() {
			ioIndex = request.IOIndexes[int(method.numChunks)]
		}
		fileKey := storage.FileKey{ObjectID: request.ObjectID, StripeVersion: version, LayoutID: layoutFor(method, no), StripeID: request.StripeID}
		_, writeErr := d.ios[ioIndex].Write(physicalio.WriteRequest{Context: request.Context, Key: fileKey, Data: encodeChunk(uint64(len(request.Data)), chunks[no])})
		if writeErr != nil {
			savePartial()
			err = fmt.Errorf("write chunk %d to I/O %d: %w", no, ioIndex, writeErr)
			d.logger.Error("stripe write failed", "object_id", request.ObjectID.String(), "stripe_id", request.StripeID, "io_index", ioIndex, "chunk", no, "error", err)
			return response, err
		}
		written[no] = chunkLocation{ioIndex: ioIndex, key: fileKey, data: append([]byte(nil), chunks[no]...), totalLength: uint64(len(request.Data))}
	}
	versions := d.versions[key]
	if versions == nil {
		versions = make(map[storage.StripeVersion]*versionInfo)
		d.versions[key] = versions
	}
	versions[version] = &versionInfo{method: method, chunks: cloneChunks(written), recoverable: true}
	stripes := d.objects[request.ObjectID]
	if stripes == nil {
		stripes = make(map[storage.StripeID]*stripeInfo)
		d.objects[request.ObjectID] = stripes
	}
	stripes[request.StripeID] = &stripeInfo{version: version, method: method, chunks: cloneChunks(written)}
	response.Version = version
	return response, nil
}

func validateIOIndexes(indexes []int, total, minimum int) error {
	if len(indexes) < minimum {
		return errInsufficientChunks
	}
	seen := make(map[int]struct{}, len(indexes))
	for _, index := range indexes {
		if index < 0 || index >= total {
			return fmt.Errorf("I/O index %d out of range", index)
		}
		if _, ok := seen[index]; ok {
			return fmt.Errorf("duplicate I/O index %d", index)
		}
		seen[index] = struct{}{}
	}
	return nil
}
func splitStripe(data []byte, count uint8) map[chunkNo][]byte {
	size := (len(data) + int(count) - 1) / int(count)
	return splitStripeWithSize(data, count, size)
}

func splitStripeWithSize(data []byte, count uint8, size int) map[chunkNo][]byte {
	result := make(map[chunkNo][]byte, count)
	if count == 0 || size < 0 || len(data) > size*int(count) {
		return result
	}
	for i := 0; i < int(count); i++ {
		chunk := make([]byte, size)
		start := i * size
		if start < len(data) {
			end := start + size
			if end > len(data) {
				end = len(data)
			}
			copy(chunk, data[start:end])
		}
		result[chunkNo(i)] = chunk
	}
	return result
}
func orderedDataChunks(chunks map[chunkNo][]byte, count uint8) [][]byte {
	result := make([][]byte, count)
	for i := range result {
		result[i] = chunks[chunkNo(i)]
	}
	return result
}
func orderedChunkNos(chunks map[chunkNo][]byte) []chunkNo {
	result := make([]chunkNo, 0, len(chunks))
	for no := range chunks {
		result = append(result, no)
	}
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j] < result[i] {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}
