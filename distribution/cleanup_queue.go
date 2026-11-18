package distribution

import (
	"sort"
	"sync"

	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type CleanupMethod uint8

const (
	CleanupMethodDeleteOldVersion CleanupMethod = iota + 1
	CleanupMethodPreserveAbnormal
)

type cleanupMethod = CleanupMethod

const (
	cleanupDeleteOldVersion cleanupMethod = CleanupMethodDeleteOldVersion
	cleanupPreserveAbnormal cleanupMethod = CleanupMethodPreserveAbnormal
)

type cleanupCandidateKey struct {
	ioIndex  int
	objectID shared.ObjectID
	stripeID storage.StripeID
	method   cleanupMethod
}

// cleanupQueue is only a deduplicated set of work hints. Physical files and
// their current classification are revalidated from Distribution state.
type cleanupQueue struct {
	mu       sync.Mutex
	pending  map[cleanupCandidateKey]struct{}
	inFlight map[cleanupCandidateKey]struct{}
}

func newCleanupQueue() *cleanupQueue {
	return &cleanupQueue{pending: make(map[cleanupCandidateKey]struct{}), inFlight: make(map[cleanupCandidateKey]struct{})}
}

// refreshStripeCleanupQueueLocked replaces all queued methods for one Stripe.
// The caller must hold mapMu. Lock order is mapMu -> cleanupQueue.mu.
func (d *Distribution) refreshStripeCleanupQueueLocked(key stripeIDKey) {
	if d.cleanupQueue == nil {
		d.cleanupQueue = newCleanupQueue()
	}

	d.cleanupQueue.mu.Lock()
	for candidate := range d.cleanupQueue.pending {
		if candidate.objectID == key.objectID && candidate.stripeID == key.stripeID {
			delete(d.cleanupQueue.pending, candidate)
		}
	}
	d.cleanupQueue.mu.Unlock()

	var orphans []chunkLocation
	for _, location := range d.orphans[key.objectID] {
		if location.key.StripeID == key.stripeID {
			orphans = append(orphans, location)
		}
	}
	classified, ok := classifyStripe(d.versions[key], orphans, d.objects[key.objectID][key.stripeID])
	if !ok {
		return
	}

	byMethod := map[cleanupMethod][]chunkLocation{
		cleanupDeleteOldVersion: classified.OldVersions,
		cleanupPreserveAbnormal: classified.Abnormal,
	}
	d.cleanupQueue.mu.Lock()
	defer d.cleanupQueue.mu.Unlock()
	for method, locations := range byMethod {
		for _, location := range locations {
			d.cleanupQueue.pending[cleanupCandidateKey{
				ioIndex: location.ioIndex, objectID: key.objectID, stripeID: key.stripeID, method: method,
			}] = struct{}{}
		}
	}	d.wakeCleanupWorker()
}

func (d *Distribution) cleanupCandidates() []cleanupCandidateKey {
	if d.cleanupQueue == nil {
		return nil
	}
	d.cleanupQueue.mu.Lock()
	defer d.cleanupQueue.mu.Unlock()
	result := make([]cleanupCandidateKey, 0, len(d.cleanupQueue.pending))
	for candidate := range d.cleanupQueue.pending {
		result = append(result, candidate)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ioIndex != result[j].ioIndex {
			return result[i].ioIndex < result[j].ioIndex
		}
		if result[i].objectID != result[j].objectID {
			return result[i].objectID.String() < result[j].objectID.String()
		}
		if result[i].stripeID != result[j].stripeID {
			return result[i].stripeID < result[j].stripeID
		}
		return result[i].method < result[j].method
	})
	return result
}

// claimCleanupCandidate reserves a hint without retaining the queue lock
// during Stripe inspection or physical I/O.
func (d *Distribution) claimCleanupCandidate(key cleanupCandidateKey) bool {
	if d.cleanupQueue == nil {
		return false
	}
	d.cleanupQueue.mu.Lock()
	defer d.cleanupQueue.mu.Unlock()
	if _, exists := d.cleanupQueue.pending[key]; !exists {
		return false
	}
	if _, exists := d.cleanupQueue.inFlight[key]; exists {
		return false
	}
	d.cleanupQueue.inFlight[key] = struct{}{}
	return true
}

func (d *Distribution) releaseCleanupCandidate(key cleanupCandidateKey) {
	if d.cleanupQueue == nil {
		return
	}
	d.cleanupQueue.mu.Lock()
	delete(d.cleanupQueue.inFlight, key)
	d.cleanupQueue.mu.Unlock()
}
