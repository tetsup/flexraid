package orchestration

// Compatibility entry points for the package's legacy tests. Production callers
// must enter through Management; these wrappers keep focused Orchestration tests
// exercising the lock-free operation implementations directly.
func (l *Orchestration) Cleanup(r CleanupRequest) (CleanupResponse, error) { return l.CleanupLocked(r) }
func (l *Orchestration) Create(r CreateRequest) (CreateResponse, error)    { return l.CreateLocked(r) }
func (l *Orchestration) Flush(r FlushRequest) (FlushResponse, error)       { return l.FlushLocked(r) }
func (l *Orchestration) Fsync(r FsyncRequest) (FsyncResponse, error)       { return l.FsyncLocked(r) }
func (l *Orchestration) GetAttr(r GetAttrRequest) (GetAttrResponse, error) { return l.GetAttrLocked(r) }
func (l *Orchestration) Link(r LinkRequest) (LinkResponse, error)          { return l.LinkLocked(r) }
func (l *Orchestration) Lookup(r LookupRequest) (LookupResponse, error)    { return l.LookupLocked(r) }
func (l *Orchestration) Mkdir(r MkdirRequest) (MkdirResponse, error)       { return l.MkdirLocked(r) }
func (l *Orchestration) Open(r OpenRequest) (OpenResponse, error)          { return l.OpenLocked(r) }
func (l *Orchestration) Release(r ReleaseRequest) (ReleaseResponse, error) { return l.ReleaseLocked(r) }
func (l *Orchestration) Read(r ReadRequest) (ReadResponse, error)          { return l.ReadLocked(r) }
func (l *Orchestration) ReadDir(r ReadDirRequest) (ReadDirResponse, error) { return l.ReadDirLocked(r) }
func (l *Orchestration) ReadLink(r ReadLinkRequest) (ReadLinkResponse, error) {
	return l.ReadLinkLocked(r)
}
func (l *Orchestration) Rename(r RenameRequest) (RenameResponse, error)    { return l.RenameLocked(r) }
func (l *Orchestration) Rmdir(r RmdirRequest) (RmdirResponse, error)       { return l.RmdirLocked(r) }
func (l *Orchestration) SetAttr(r SetAttrRequest) (SetAttrResponse, error) { return l.SetAttrLocked(r) }
func (l *Orchestration) StatFs(r StatFsRequest) (StatFsResponse, error)    { return l.StatFsLocked(r) }
func (l *Orchestration) Symlink(r SymlinkRequest) (SymlinkResponse, error) { return l.SymlinkLocked(r) }
func (l *Orchestration) Truncate(r TruncateRequest) (TruncateResponse, error) {
	return l.TruncateLocked(r)
}
func (l *Orchestration) Unlink(r UnlinkRequest) (UnlinkResponse, error) { return l.UnlinkLocked(r) }
func (l *Orchestration) Write(r WriteRequest) (WriteResponse, error)    { return l.WriteLocked(r) }
