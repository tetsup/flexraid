package io

import "time"

// recordAccess records when this I/O layer last attempted physical storage access.
// Monitoring uses this timestamp to refresh drive state only during active I/O windows.
func (layer *IO) recordAccess() {
	layer.lastAccess.Store(time.Now().UnixNano())
}

func (layer *IO) lastAccessTime() time.Time {
	nanoseconds := layer.lastAccess.Load()
	if nanoseconds == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanoseconds)
}
