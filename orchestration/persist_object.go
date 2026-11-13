package orchestration

import (
	"context"
	"fmt"

	cfg "github.com/tetsup/flexraid/pkg/shared/config"
)

// persistNewObject writes every stripe of a new object before the caller exposes
// it in the in-memory namespace. The returned Header contains its final layout.
func (layer *Orchestration) persistNewObject(ctx context.Context, objectHeader header, content []byte) (header, error) {
	plan, err := planPlacement(objectHeader.Size)
	if err != nil {
		return header{}, err
	}
	objectHeader.Layout = plan[0].Layout
	if len(plan) > 1 {
		objectHeader.Layout.ChunkSize = plan[1].ChunkSize
	} else {
		objectHeader.Layout.ChunkSize = cfg.MinimumChunkSize
	}

	headerBytes, err := objectHeader.MarshalBinary()
	if err != nil {
		return header{}, err
	}
	if uint64(len(headerBytes)) > cfg.HeaderReservedSize {
		return header{}, fmt.Errorf("serialized Header exceeds reserved area")
	}
	if uint64(len(content)) != objectHeader.Size {
		return header{}, fmt.Errorf("object content size does not match Header size")
	}

	file := &runtimeFile{
		objectID:    objectHeader.ObjectID,
		header:      objectHeader,
		stripeCache: make(map[uint64]*stripeCacheEntry, len(plan)),
	}
	var contentOffset uint64
	for _, placement := range plan {
		if err := ctx.Err(); err != nil {
			return header{}, err
		}
		prefix := uint64(0)
		if placement.StripeID == 0 {
			prefix = cfg.HeaderReservedSize
		}
		if placement.DataSize > uint64(int(^uint(0)>>1))-prefix {
			return header{}, fmt.Errorf("stripe %d exceeds addressable memory", placement.StripeID)
		}
		data := make([]byte, int(prefix+placement.DataSize))
		if placement.StripeID == 0 {
			copy(data, headerBytes)
		}
		end := contentOffset + placement.DataSize
		if end > uint64(len(content)) {
			end = uint64(len(content))
		}
		if contentOffset < end {
			copy(data[int(prefix):], content[int(contentOffset):int(end)])
		}
		contentOffset = end
		file.stripeCache[placement.StripeID] = &stripeCacheEntry{data: data, state: stripeCacheDirty}
	}
	if contentOffset != objectHeader.Size {
		return header{}, fmt.Errorf("placement plan covers %d bytes, want %d", contentOffset, objectHeader.Size)
	}
	if _, err := layer.persistDirtyStripesLocked(ctx, file); err != nil {
		return header{}, err
	}
	return objectHeader, nil
}

// validateNewObjectName applies the same component rules used by Lookup.
func validateNewObjectName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid child name %q", name)
	}
	for _, character := range name {
		if character == '/' || character == 0 {
			return fmt.Errorf("invalid child name %q", name)
		}
	}
	return nil
}
