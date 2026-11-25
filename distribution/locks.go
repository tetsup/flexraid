package distribution

// lockStripe is a compatibility helper while callers are migrated to explicit
// map snapshots. It deliberately provides no per-stripe serialization.
func (d *Distribution) lockStripe(_ stripeIDKey) (unlockStripe func(), unlockMap func()) {
	d.mapMu.Lock()
	return func() {}, d.mapMu.Unlock
}
