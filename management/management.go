// Package management provides the serialized entry points for Orchestration operations.
package management

import (
	"sync"

	"github.com/tetsup/flexraid/orchestration"
)

type Management struct {
	mu           sync.Mutex
	orchestration *orchestration.Orchestration
}

func New(layer *orchestration.Orchestration) *Management {
	return &Management{orchestration: layer}
}

func (layer *Management) Cleanup(request orchestration.CleanupRequest) (orchestration.CleanupResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.CleanupLocked(request)
}

func (layer *Management) Create(request orchestration.CreateRequest) (orchestration.CreateResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.CreateLocked(request)
}

func (layer *Management) Flush(request orchestration.FlushRequest) (orchestration.FlushResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.FlushLocked(request)
}

func (layer *Management) Fsync(request orchestration.FsyncRequest) (orchestration.FsyncResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.FsyncLocked(request)
}

func (layer *Management) GetAttr(request orchestration.GetAttrRequest) (orchestration.GetAttrResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.GetAttrLocked(request)
}

func (layer *Management) Link(request orchestration.LinkRequest) (orchestration.LinkResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.LinkLocked(request)
}

func (layer *Management) Lookup(request orchestration.LookupRequest) (orchestration.LookupResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.LookupLocked(request)
}

func (layer *Management) Mkdir(request orchestration.MkdirRequest) (orchestration.MkdirResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.MkdirLocked(request)
}

func (layer *Management) Open(request orchestration.OpenRequest) (orchestration.OpenResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.OpenLocked(request)
}

func (layer *Management) Release(request orchestration.ReleaseRequest) (orchestration.ReleaseResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.ReleaseLocked(request)
}

func (layer *Management) Read(request orchestration.ReadRequest) (orchestration.ReadResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.ReadLocked(request)
}

func (layer *Management) ReadDir(request orchestration.ReadDirRequest) (orchestration.ReadDirResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.ReadDirLocked(request)
}

func (layer *Management) ReadLink(request orchestration.ReadLinkRequest) (orchestration.ReadLinkResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.ReadLinkLocked(request)
}

func (layer *Management) Rename(request orchestration.RenameRequest) (orchestration.RenameResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.RenameLocked(request)
}

func (layer *Management) Rmdir(request orchestration.RmdirRequest) (orchestration.RmdirResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.RmdirLocked(request)
}

func (layer *Management) SetAttr(request orchestration.SetAttrRequest) (orchestration.SetAttrResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.SetAttrLocked(request)
}

func (layer *Management) StatFs(request orchestration.StatFsRequest) (orchestration.StatFsResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.StatFsLocked(request)
}

func (layer *Management) Symlink(request orchestration.SymlinkRequest) (orchestration.SymlinkResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.SymlinkLocked(request)
}

func (layer *Management) Truncate(request orchestration.TruncateRequest) (orchestration.TruncateResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.TruncateLocked(request)
}

func (layer *Management) Unlink(request orchestration.UnlinkRequest) (orchestration.UnlinkResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.UnlinkLocked(request)
}

func (layer *Management) Write(request orchestration.WriteRequest) (orchestration.WriteResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.orchestration.WriteLocked(request)
}
