package orchestration

import (
	"context"
	"os"
	"time"
)

// touchDirectoryLocked persists mtime and ctime after a successful namespace
// entry mutation. The caller must hold layer.mu.
func (layer *Orchestration) touchDirectoryLocked(ctx context.Context, directory *runtimeObject, now time.Time) error {
	if directory == nil || directory.header.Kind != objectKindDirectory {
		return os.ErrInvalid
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	next := directory.header
	next.Mtime = now
	next.Ctime = now
	if err := layer.persistObjectHeader(ctx, next); err != nil {
		return err
	}
	directory.header = next
	layer.headers[next.ObjectID] = next
	return nil
}
