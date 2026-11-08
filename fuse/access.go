package fuse

import (
	"context"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	gofuse "github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tetsup/flexraid/orchestration"
)

var _ fs.NodeAccesser = (*node)(nil)

// Access checks the requested POSIX permission bits against the inode's
// Orchestration attributes and the credentials attached to the FUSE request.
func (n *node) Access(ctx context.Context, mask uint32) syscall.Errno {
	result, err := n.layer.GetAttr(orchestration.GetAttrRequest{Context: ctx, ObjectID: n.objectID})
	if err != nil {
		return gofuse.ToErrno(err)
	}
	attributes := result.Attributes
	credentials, ok := gofuse.FromContext(ctx)
	if !ok || credentials == nil {
		return syscall.EACCES
	}
	uid, gid := credentials.Uid, credentials.Gid
	if uid == 0 {
		if mask&gofuse.X_OK != 0 && attributes.Mode&0111 == 0 {
			return syscall.EACCES
		}
		return 0
	}

	var bits uint32
	switch {
	case uid == attributes.UID:
		bits = (attributes.Mode >> 6) & 7
	case gid == attributes.GID:
		bits = (attributes.Mode >> 3) & 7
	default:
		bits = attributes.Mode & 7
	}
	required := uint32(0)
	if mask&gofuse.R_OK != 0 {
		required |= 4
	}
	if mask&gofuse.W_OK != 0 {
		required |= 2
	}
	if mask&gofuse.X_OK != 0 {
		required |= 1
	}
	if bits&required != required {
		return syscall.EACCES
	}
	return 0
}
