package fuse

import (
	"context"
	"errors"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
)

// toErrno preserves POSIX interruption semantics for requests canceled by the
// kernel. go-fuse's generic conversion otherwise reports context.Canceled as
// EOPNOTSUPP, which makes an interrupted I/O look like an unsupported feature.
func toErrno(err error) syscall.Errno {
	switch {
	case errors.Is(err, context.Canceled):
		return syscall.EINTR
	case errors.Is(err, context.DeadlineExceeded):
		return syscall.ETIMEDOUT
	default:
		return fs.ToErrno(err)
	}
}
