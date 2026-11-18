package distribution

import "github.com/tetsup/flexraid/storage"

// stripeChunks partitions the physical chunks belonging to one Stripe.
type stripeChunks struct {
	Current     []chunkLocation
	OldVersions []chunkLocation
	Abnormal    []chunkLocation
}

// classifyStripe classifies a complete in-memory Stripe snapshot. The caller
// must provide the selected latest recoverable version and its version map.
// A false result means that no trustworthy current version can be established;
// callers must hold every cleanup candidate for this Stripe.
func classifyStripe(
	versions map[storage.StripeVersion]*versionInfo,
	orphans []chunkLocation,
	current *stripeInfo,
) (stripeChunks, bool) {
	var result stripeChunks
	if current == nil {
		return result, false
	}
	selected := versions[current.version]
	if selected == nil || selected.invalid || !selected.recoverable {
		return result, false
	}

	for versionNo, version := range versions {
		for _, location := range version.chunks {
			switch {
			case versionNo < current.version:
				result.OldVersions = append(result.OldVersions, location)
			case versionNo > current.version:
				result.Abnormal = append(result.Abnormal, location)
			default:
				if version.invalid || !version.recoverable {
					result.Abnormal = append(result.Abnormal, location)
				} else {
					result.Current = append(result.Current, location)
				}
			}
		}
	}

	// Orphan entries have a usable FileKey but their contents or layout could
	// not be admitted into version.chunks. They are never current data.
	for _, location := range orphans {
		switch {
		case location.key.StripeVersion < current.version:
			result.OldVersions = append(result.OldVersions, location)
		default:
			result.Abnormal = append(result.Abnormal, location)
		}
	}
	return result, true
}
