package distribution

import (
	"sort"
	"sync"

	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type CleanupReason uint8

const (
	CleanupOldVersion CleanupReason = iota + 1
	CleanupAbnormalVersion
)

type cleanupReason = CleanupReason

const (
	cleanupOldVersion = CleanupOldVersion
	cleanupAbnormalVersion = CleanupAbnormalVersion
)

type cleanupCandidateKey struct {
	ioIndex  int
	objectID shared.ObjectID
	stripeID storage.StripeID
	reason   cleanupReason
}

// cleanupQueue is only a deduplicated set of work hints. The physical files
// and their current classification are always re-read from Distribution state
// when a worker processes a candidate.
type cleanupQueue struct {
	mu      sync.Mutex
	pending  map[cleanupCandidateKey]struct{}
	inFlight map[cleanupCandidateKey]struct{}
}

func newCleanupQueue() *cleanupQueue {
	return &cleanupQueue{pending: make(map[cleanupCandidateKey]struct{}), inFlight: make(map[cleanupCandidateKey]struct{})}
}

// refreshStripeCleanupQueueLocked replaces all queued reasons for one Stripe.
// The caller must hold mapMu. Lock order is mapMu -> cleanupQueue.mu; consumers
// must release cleanupQueue.mu before acquiring mapMu or a Stripe lock.
func (d *Distribution) refreshStripeCleanupQueueLocked(key stripeIDKey) {
	if d.cleanupQueue == nil {
		d.cleanupQueue = newCleanupQueue()
	}

	current := d.objects[key.objectID][key.stripeID]
	d.cleanupQueue.mu.Lock()
	defer d.cleanupQueue.mu.Unlock()

	for candidate := range d.cleanupQueue.pending {
		if candidate.objectID == key.objectID && candidate.stripeID == key.stripeID {
			delete(d.cleanupQueue.pending, candidate)
		}
	}
	if current == nil {
		return
	}

	for versionNo, version := range d.versions[key] {
		var reason cleanupReason
		switch {
		case versionNo < current.version:
			reason = cleanupOldVersion
		case versionNo > current.version:
			reason = cleanupAbnormalVersion
		default:
			continue
		}
		for ioIndex := range versionIOIndexes(version.chunks) {
			d.cleanupQueue.pending[cleanupCandidateKey{
				ioIndex: ioIndex, objectID: key.objectID, stripeID: key.stripeID, reason: reason,
			}] = struct{}{}
		}
	}

	// Orphans retain their physical FileKey and can therefore be classified
	// against the selected version even when their contents were not usable.
	for _, location := range d.orphans[key.objectID] {
		if location.key.StripeID != key.stripeID {
			continue
		}
		var reason cleanupReason
		switch {
		case location.key.StripeVersion < current.version:
			reason = cleanupOldVersion
		case location.key.StripeVersion > current.version:
			reason = cleanupAbnormalVersion
		default:
			continue
		}
		d.cleanupQueue.pending[cleanupCandidateKey{
			ioIndex: location.ioIndex, objectID: key.objectID, stripeID: key.stripeID, reason: reason,
		}] = struct{}{}
	}
}

func versionIOIndexes(chunks map[chunkNo]chunkLocation) map[int]struct{} {
	indexes := make(map[int]struct{}, len(chunks))
	for _, location := range chunks {
		indexes[location.ioIndex] = struct{}{}
	}
	return indexes
}

// popCleanupCandidate removes one hint. It never holds the queue mutex while
// the caller performs Stripe-state inspection or physical I/O.
func (d *Distribution) popCleanupCandidate() (cleanupCandidateKey, bool) {
	if d.cleanupQueue == nil {
		return cleanupCandidateKey{}, false
	}
	d.cleanupQueue.mu.Lock()
	defer d.cleanupQueue.mu.Unlock()
	for candidate := range d.cleanupQueue.pending {
		delete(d.cleanupQueue.pending, candidate)
		return candidate, true
	}
	return cleanupCandidateKey{}, false
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
		return result[i].reason < result[j].reason
	})
	return result
}

// claimCleanupCandidate serializes workers for the same Drive/Stripe/reason.
func (d *Distribution) claimCleanupCandidate(key cleanupCandidateKey) bool {
	if d.cleanupQueue == nil { return false }
	d.cleanupQueue.mu.Lock()
	defer d.cleanupQueue.mu.Unlock()
	if _, exists := d.cleanupQueue.pending[key]; !exists { return false }
	if _, exists := d.cleanupQueue.inFlight[key]; exists { return false }
	d.cleanupQueue.inFlight[key] = struct{}{}
	return true
}

func (d *Distribution) releaseCleanupCandidate(key cleanupCandidateKey) {
	if d.cleanupQueue == nil { return }
	d.cleanupQueue.mu.Lock()
	delete(d.cleanupQueue.inFlight, key)
	d.cleanupQueue.mu.Unlock()
}
