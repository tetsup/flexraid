package orchestration

import (
	"fmt"

	"github.com/tetsup/flexraid/distribution"
	cfg "github.com/tetsup/flexraid/pkg/shared/config"
	"github.com/tetsup/flexraid/storage"
)

type stripeGeometry struct {
	StripeID  storage.StripeID
	FileOffset uint64
	Capacity  uint64
	DataOffset uint64
	ChunkSize uint32
	Layout distribution.Layout
}

func geometryFor(header header, stripeID storage.StripeID) (stripeGeometry, error) {
	layout, err := distributionLayout(header.Layout)
	if err != nil {
		return stripeGeometry{}, err
	}
	if layout.NumChunks == 0 {
		return stripeGeometry{}, errInvalidHeader
	}
	chunkSize := header.Layout.ChunkSize
	if stripeID == 0 {
		chunkSize = cfg.MinimumChunkSize
	} else if chunkSize == 0 {
		return stripeGeometry{}, fmt.Errorf("%w: zero regular chunk size", errInvalidHeader)
	}
	capacity := uint64(chunkSize) * uint64(layout.NumChunks)
	if layout.Type == distribution.LayoutMirror {
		capacity = uint64(chunkSize)
		if stripeID != 0 {
			return stripeGeometry{}, fmt.Errorf("%w: mirror layout cannot have regular stripes", errInvalidHeader)
		}
	}
	fileOffset := uint64(0)
	dataOffset := uint64(0)
	if stripeID == 0 {
		dataOffset = cfg.HeaderReservedSize
		capacity -= cfg.HeaderReservedSize
	} else {
		firstCapacity := uint64(cfg.MinimumChunkSize)*uint64(layout.NumChunks) - cfg.HeaderReservedSize
		regularCapacity := uint64(header.Layout.ChunkSize) * uint64(layout.NumChunks)
		fileOffset = firstCapacity + (uint64(stripeID)-1)*regularCapacity
	}
	return stripeGeometry{StripeID: stripeID, FileOffset: fileOffset, Capacity: capacity, DataOffset: dataOffset, ChunkSize: chunkSize, Layout: layout}, nil
}

func locateFileOffset(header header, offset uint64) (stripeGeometry, uint64, error) {
	first, err := geometryFor(header, 0)
	if err != nil {
		return stripeGeometry{}, 0, err
	}
	if offset < first.Capacity {
		return first, first.DataOffset + offset, nil
	}
	if first.Layout.Type == distribution.LayoutMirror || header.Layout.ChunkSize == 0 {
		return stripeGeometry{}, 0, fmt.Errorf("%w: offset exceeds mirror capacity", errInvalidHeader)
	}
	regularCapacity := uint64(header.Layout.ChunkSize) * uint64(first.Layout.NumChunks)
	relative := offset - first.Capacity
	stripeID := storage.StripeID(1 + relative/regularCapacity)
	geometry, err := geometryFor(header, stripeID)
	if err != nil {
		return stripeGeometry{}, 0, err
	}
	return geometry, relative % regularCapacity, nil
}
