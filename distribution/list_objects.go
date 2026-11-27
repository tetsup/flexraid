package distribution

import (
	"context"
	"sort"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

type ListObjectsRequest struct{ Context context.Context }
type ListObjectsResponse struct{ ObjectIDs []shared.ObjectID }

func (d *Distribution) ListObjects(request ListObjectsRequest) (response ListObjectsResponse, err error) {
	started := time.Now()
	defer func() { logOperation(d, "ListObjects", started, err, "objects", len(response.ObjectIDs)) }()
	if err = request.Context.Err(); err != nil {
		return response, err
	}
	d.mapMu.RLock()
	defer d.mapMu.RUnlock()
	for id, stripes := range d.objects {
		// An Object is visible only after its Stripe 0 watermark has been
		// committed and published. In-flight versions and orphan-only Objects
		// must not leak through the namespace listing.
		if stripes[0] != nil {
			response.ObjectIDs = append(response.ObjectIDs, id)
		}
	}
	sort.Slice(response.ObjectIDs, func(i, j int) bool { return response.ObjectIDs[i].String() < response.ObjectIDs[j].String() })
	return response, nil
}
