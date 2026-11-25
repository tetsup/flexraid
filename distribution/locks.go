package distribution

// lockStripe is retained only as a temporary compatibility helper. It does
// not serialize Stripe operations; callers must snapshot shared map values.
func (d *Distribution) lockStripe(_ stripeIDKey) (unlockStripe func(), unlockMap func()) {
	d.mapMu.Lock()
	return func() {}, d.mapMu.Unlock
}
