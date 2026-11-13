package orchestration

import (
	"context"
	"fmt"
	"math"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
	cfg "github.com/tetsup/flexraid/pkg/shared/config"
	"github.com/tetsup/flexraid/storage"
)

// validateHeaderStripeLayouts checks every stripe that belongs to the logical
// file. Physical stripes beyond EOF are deliberately ignored.
func validateHeaderStripeLayouts(ctx context.Context, layer *distribution.Distribution, objectID shared.ObjectID, h header) error {
	first, err := geometryFor(h, 0)
	if err != nil {
		return err
	}
	count := uint64(1)
	if h.Size > first.Capacity {
		if first.Layout.Type == distribution.LayoutMirror {
			return fmt.Errorf("%w: mirror file exceeds first-stripe capacity", errInvalidHeader)
		}
		regularCapacity := uint64(h.Layout.ChunkSize) * uint64(first.Layout.NumChunks)
		if regularCapacity == 0 {
			return errInvalidHeader
		}
		remaining := h.Size - first.Capacity
		additional := 1 + (remaining-1)/regularCapacity
		if additional >= uint64(math.MaxInt64) {
			return fmt.Errorf("%w: stripe count exceeds supported range", errInvalidHeader)
		}
		count += additional
	}
	if count > uint64(math.MaxInt64) {
		return fmt.Errorf("%w: stripe count exceeds supported range", errInvalidHeader)
	}
	for id := uint64(0); id < count; id++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		geometry, err := geometryFor(h, storage.StripeID(id))
		if err != nil {
			return fmt.Errorf("invalid header geometry for stripe %d: %w", id, err)
		}
		info, err := layer.GetStripeInfo(distribution.GetStripeInfoRequest{ObjectID: objectID, StripeID: storage.StripeID(id)})
		if err != nil {
			return fmt.Errorf("required stripe %d is unavailable: %w", id, err)
		}
		if info.Layout != geometry.Layout {
			return fmt.Errorf("%w: stripe %d layout %v does not match header layout %v",
				distribution.ErrDataCorrupted, id, info.Layout, geometry.Layout)
		}
		// Mirror is only used for stripe zero. Its on-disk payload length is
		// the logical data length, so the physical chunk size cannot be inferred
		// from a scan; stripe zero always uses MinimumChunkSize.
		if id > 0 && info.ChunkSize != geometry.ChunkSize {
			return fmt.Errorf("%w: stripe %d chunk size %d does not match header chunk size %d",
				distribution.ErrDataCorrupted, id, info.ChunkSize, geometry.ChunkSize)
		}
	}
	return nil
}

func (layer *Orchestration) readStripeValidated(ctx context.Context, objectID shared.ObjectID, h header, geometry stripeGeometry) ([]byte, error) {
	info, err := layer.distribution.GetStripeInfo(distribution.GetStripeInfoRequest{
		ObjectID: objectID, StripeID: geometry.StripeID,
	})
	if err != nil {
		return nil, err
	}
	if info.Layout != geometry.Layout {
		return nil, fmt.Errorf("%w: stripe %d layout does not match header", distribution.ErrDataCorrupted, geometry.StripeID)
	}
	if geometry.StripeID > 0 && info.ChunkSize != geometry.ChunkSize {
		return nil, fmt.Errorf("%w: stripe %d chunk size %d does not match header chunk size %d",
			distribution.ErrDataCorrupted, geometry.StripeID, info.ChunkSize, geometry.ChunkSize)
	}
	stripe, err := layer.distribution.ReadStripe(distribution.ReadStripeRequest{
		Context: ctx, ObjectID: objectID, StripeID: geometry.StripeID, ChunkSize: geometry.ChunkSize,
	})
	if err != nil {
		return nil, err
	}
	return stripe.Data, nil
}
