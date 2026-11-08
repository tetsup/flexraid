package orchestration

import (
	"context"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

type AccessMode uint8

const (
	AccessModeReadOnly AccessMode = iota
	AccessModeWriteOnly
	AccessModeReadWrite
)

type SyncMode uint8

const (
	SyncModeNone SyncMode = iota
	SyncModeData
	SyncModeFull
)

// FileHandle represents one independent open-file description.
// Its concrete state is owned by Orchestration, never by the FUSE adapter.
type FileHandle struct {
	mu sync.Mutex

	runtimeFile *runtimeFile
	closed      bool
	accessMode  AccessMode
	append      bool
	syncMode    SyncMode

	activeStripeID    uint64
	hasActiveStripeID bool
}

type OpenRequest struct {
	Context  context.Context
	ObjectID shared.ObjectID
	Flags    uint32
}

type OpenResponse struct {
	Handle *FileHandle
}

func (layer *Orchestration) Open(request OpenRequest) (response OpenResponse, err error) {
	started := time.Now()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		if layer.logger != nil {
			layer.logger.Debug("Orchestration Open completed",
				"object_id", request.ObjectID.String(),
				"flags", request.Flags,
				"success", response.Handle != nil,
				"duration", time.Since(started),
				"error", err)
		}
	}()

	if err = ctx.Err(); err != nil {
		return response, err
	}
	object, exists := layer.objects[request.ObjectID]
	if !exists || object.header.Deleted {
		return response, os.ErrNotExist
	}
	if object.header.Kind == objectKindDirectory {
		if request.Flags&uint32(syscall.O_ACCMODE) != uint32(syscall.O_RDONLY) {
			return response, syscall.EISDIR
		}
		return OpenResponse{Handle: &FileHandle{}}, nil
	}
	if object.header.Kind != objectKindFile && object.header.Kind != objectKindHardLink {
		return response, syscall.EINVAL
	}
	if object.file == nil {
		return response, fmt.Errorf("file backing is unavailable: %w", os.ErrNotExist)
	}

	var accessMode AccessMode
	switch request.Flags & uint32(syscall.O_ACCMODE) {
	case syscall.O_RDONLY:
		accessMode = AccessModeReadOnly
	case syscall.O_WRONLY:
		accessMode = AccessModeWriteOnly
	case syscall.O_RDWR:
		accessMode = AccessModeReadWrite
	default:
		return response, fmt.Errorf("invalid open access mode: %w", os.ErrInvalid)
	}

	syncMode := SyncModeNone
	if request.Flags&uint32(syscall.O_SYNC) != 0 {
		syncMode = SyncModeFull
	} else if request.Flags&uint32(syscall.O_DSYNC) == uint32(syscall.O_DSYNC) {
		syncMode = SyncModeData
	}

	handle := &FileHandle{
		runtimeFile: object.file,
		accessMode:  accessMode,
		append:      request.Flags&uint32(syscall.O_APPEND) != 0,
		syncMode:    syncMode,
	}
	object.file.mu.Lock()
	object.file.openCount++
	object.file.mu.Unlock()
	return OpenResponse{Handle: handle}, nil
}

type ReleaseRequest struct {
	Context context.Context
	Handle  *FileHandle
}

type ReleaseResponse struct{}

func (layer *Orchestration) Release(request ReleaseRequest) (response ReleaseResponse, err error) {
	started := time.Now()
	if request.Handle == nil {
		return response, os.ErrInvalid
	}

	handle := request.Handle
	handle.mu.Lock()
	if handle.closed {
		handle.mu.Unlock()
		return response, nil
	}
	handle.closed = true
	handle.hasActiveStripeID = false
	file := handle.runtimeFile
	file.mu.Lock()
	if file.openCount > 0 {
		file.openCount--
	}
	shouldEnqueue := file.openCount == 0 && file.header.Deleted
	file.mu.Unlock()
	handle.mu.Unlock()
	if shouldEnqueue {
		layer.enqueueDeletion(file.objectID)
	}

	if layer.logger != nil {
		layer.logger.Debug("Orchestration Release completed", "duration", time.Since(started))
	}
	return response, nil
}
