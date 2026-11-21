package distribution

import (
	"sort"


)

// CleanupCandidateMetrics summarizes Distribution-owned cleanup inventory.
// FileBytes is the sum of current logical file lengths. ReclaimableBytes is
// non-zero only for old-version files that will be physically deleted;
// preserved abnormal files do not release capacity.
type CleanupCandidateMetrics struct {
	Method           CleanupMethod
	DriveIndex       int
	FileCount        uint64
	FileBytes        uint64
	ReclaimableBytes uint64
}

// DriveCleanupCapacityMetrics estimates per-drive free space after deleting
// all currently eligible old-version candidates. The estimate uses file
// lengths, not filesystem allocated-block accounting.
type DriveCleanupCapacityMetrics struct {
	DriveIndex              int
	CurrentAvailableBytes   uint64
	ReclaimableBytes        uint64
	EstimatedAvailableBytes uint64
}

type cleanupMetricKey struct {
	driveIndex int
	method     CleanupMethod
}

// cleanupCandidateMetricsLocked returns a complete candidate inventory from
// the current Stripe/Chunk classification. Caller must hold mapMu.
func (d *Distribution) cleanupCandidateMetricsLocked() []CleanupCandidateMetrics {
	stripes := make(map[stripeIDKey]struct{}, len(d.versions))
	for key := range d.versions {
		stripes[key] = struct{}{}
	}
	for objectID, locations := range d.orphans {
		for _, location := range locations {
			stripes[stripeIDKey{objectID: objectID, stripeID: location.key.StripeID}] = struct{}{}
		}
	}

	aggregates := make(map[cleanupMetricKey]*CleanupCandidateMetrics)
	add := func(method CleanupMethod, locations []chunkLocation) {
		for _, location := range locations {
			key := cleanupMetricKey{driveIndex: location.ioIndex, method: method}
			value := aggregates[key]
			if value == nil {
				value = &CleanupCandidateMetrics{Method: method, DriveIndex: location.ioIndex}
				aggregates[key] = value
			}
			value.FileCount++
			value.FileBytes = saturatingAdd(value.FileBytes, location.sizeBytes)
			if method == CleanupMethodDeleteOldVersion {
				value.ReclaimableBytes = saturatingAdd(value.ReclaimableBytes, location.sizeBytes)
			}
		}
	}

	for key := range stripes {
		var orphans []chunkLocation
		for _, location := range d.orphans[key.objectID] {
			if location.key.StripeID == key.stripeID {
				orphans = append(orphans, location)
			}
		}
		classified, ok := classifyStripe(
			d.versions[key], orphans, d.objects[key.objectID][key.stripeID],
		)
		if !ok {
			// Without a trusted current version no physical file is safe to
			// classify as reclaimable or preservable cleanup work.
			continue
		}
		add(CleanupMethodDeleteOldVersion, classified.OldVersions)
		add(CleanupMethodPreserveAbnormal, classified.Abnormal)
	}

	result := make([]CleanupCandidateMetrics, 0, len(aggregates))
	for _, value := range aggregates {
		result = append(result, *value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].DriveIndex != result[j].DriveIndex {
			return result[i].DriveIndex < result[j].DriveIndex
		}
		return result[i].Method < result[j].Method
	})
	return result
}

