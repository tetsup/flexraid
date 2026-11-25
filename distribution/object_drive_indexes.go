package distribution

import (
	"context"
	"sort"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

// ObjectDriveIndexesRequest identifies the logical Object whose physical
// placement is being inspected.
type ObjectDriveIndexesRequest struct {
	Context  context.Context
	ObjectID shared.ObjectID
}

// ObjectDriveIndexesResponse contains the drive indexes holding any physical
// file belonging to the Object, including obsolete versions and orphans.
type ObjectDriveIndexesResponse struct {
	IOIndexes []int
}

func (d *Distribution) ObjectDriveIndexes(request ObjectDriveIndexesRequest) (response ObjectDriveIndexesResponse, err error) {
	started := time.Now()
	defer func() {
		logOperation(d, "ObjectDriveIndexes", started, err, "object_id", request.ObjectID.String(), "drives", len(response.IOIndexes))
	}()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err = ctx.Err(); err != nil {
		return response, err
	}
	d.mapMu.RLock()
	indexes := make(map[int]struct{})
	for _, stripe := range d.objects[request.ObjectID] {
		for _, location := range stripe.chunks {
			indexes[location.ioIndex] = struct{}{}
		}
	}
	for key, versions := range d.versions {
		if key.objectID != request.ObjectID {
			continue
		}
		for _, version := range versions {
			for _, location := range version.chunks {
				indexes[location.ioIndex] = struct{}{}
			}
		}
	}
	for _, location := range d.orphans[request.ObjectID] {
		indexes[location.ioIndex] = struct{}{}
	}
	d.mapMu.RUnlock()
	for index := range indexes {
		response.IOIndexes = append(response.IOIndexes, index)
	}
	sort.Ints(response.IOIndexes)
	return response, nil
}
