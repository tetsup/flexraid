package shared

import "errors"

// ErrCleanupPermissionDriveCount indicates that a replacement snapshot does
// not match the configured drive count.
var ErrCleanupPermissionDriveCount = errors.New("cleanup permission drive count mismatch")
