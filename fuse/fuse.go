package fuse

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	gofuse "github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tetsup/flexraid/orchestration"
	"github.com/tetsup/flexraid/pkg/shared"
)

// FileSystem is the go-fuse adapter for Orchestration. It deliberately keeps
// no namespace or file metadata cache of its own.
type FileSystem struct {
	layer  *orchestration.Orchestration
	rootID shared.ObjectID
	root   *node
}

// New constructs the FUSE node tree around an initialized Orchestration layer.
// rootID must identify its root Directory.
func New(layer *orchestration.Orchestration, rootID shared.ObjectID) (*FileSystem, error) {
	if layer == nil {
		return nil, fmt.Errorf("orchestration layer is nil")
	}
	attributes, err := layer.GetAttr(orchestration.GetAttrRequest{ObjectID: rootID})
	if err != nil {
		return nil, fmt.Errorf("get root attributes: %w", err)
	}
	if attributes.Attributes.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return nil, fmt.Errorf("root object is not a directory")
	}
	filesystem := &FileSystem{layer: layer, rootID: rootID}
	filesystem.root = &node{layer: layer, objectID: rootID}
	return filesystem, nil
}

// Root returns the root node for fs.NewNodeFS.
func (filesystem *FileSystem) Root() fs.InodeEmbedder { return filesystem.root }

// Node is a namespace inode whose logical state is owned by Orchestration.
type node struct {
	fs.Inode
	layer    *orchestration.Orchestration
	objectID shared.ObjectID
}

var (
	_ fs.NodeLookuper   = (*node)(nil)
	_ fs.NodeGetattrer  = (*node)(nil)
	_ fs.NodeReaddirer  = (*node)(nil)
	_ fs.NodeOpener     = (*node)(nil)
	_ fs.NodeReader     = (*node)(nil)
	_ fs.NodeWriter     = (*node)(nil)
	_ fs.NodeFlusher    = (*node)(nil)
	_ fs.NodeFsyncer    = (*node)(nil)
	_ fs.NodeReleaser   = (*node)(nil)
	_ fs.NodeCreater    = (*node)(nil)
	_ fs.NodeMkdirer    = (*node)(nil)
	_ fs.NodeUnlinker   = (*node)(nil)
	_ fs.NodeRmdirer    = (*node)(nil)
	_ fs.NodeRenamer    = (*node)(nil)
	_ fs.NodeLinker     = (*node)(nil)
	_ fs.NodeSymlinker  = (*node)(nil)
	_ fs.NodeReadlinker = (*node)(nil)
	_ fs.NodeSetattrer  = (*node)(nil)
	_ fs.NodeStatfser   = (*node)(nil)
)

func (n *node) Lookup(ctx context.Context, name string, out *gofuse.EntryOut) (*fs.Inode, syscall.Errno) {
	result, err := n.layer.Lookup(orchestration.LookupRequest{Context: ctx, ParentID: n.objectID, Name: name})
	if err != nil {
		return nil, gofuse.ToErrno(err)
	}
	attributes, err := n.layer.GetAttr(orchestration.GetAttrRequest{Context: ctx, ObjectID: result.ObjectID})
	if err != nil {
		return nil, gofuse.ToErrno(err)
	}
	fillEntry(out, attributes.Attributes)
	child := &node{layer: n.layer, objectID: result.ObjectID}
	return n.NewInode(ctx, child, stableAttr(attributes.Attributes)), 0
}

func (n *node) Getattr(ctx context.Context, _ fs.FileHandle, out *gofuse.AttrOut) syscall.Errno {
	attributes, err := n.layer.GetAttr(orchestration.GetAttrRequest{Context: ctx, ObjectID: n.objectID})
	if err != nil {
		return gofuse.ToErrno(err)
	}
	fillAttr(&out.Attr, attributes.Attributes)
	return 0
}

func (n *node) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	result, err := n.layer.ReadDir(orchestration.ReadDirRequest{Context: ctx, ObjectID: n.objectID})
	if err != nil {
		return nil, gofuse.ToErrno(err)
	}
	entries := make([]gofuse.DirEntry, 0, len(result.Entries))
	for _, entry := range result.Entries {
		attributes, attrErr := n.layer.GetAttr(orchestration.GetAttrRequest{Context: ctx, ObjectID: entry.ObjectID})
		if attrErr != nil {
			return nil, gofuse.ToErrno(attrErr)
		}
		entries = append(entries, gofuse.DirEntry{
			Name: entry.Name, Ino: inodeNumber(entry.ObjectID),
			Mode: attributes.Attributes.Mode & syscall.S_IFMT,
		})
	}
	return fs.NewListDirStream(entries), 0
}

func (n *node) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	result, err := n.layer.Open(orchestration.OpenRequest{Context: ctx, ObjectID: n.objectID, Flags: flags})
	if err != nil {
		return nil, 0, gofuse.ToErrno(err)
	}
	return &fileHandle{layer: n.layer, objectID: n.objectID, handle: result.Handle}, 0, 0
}

func (n *node) Read(ctx context.Context, fh fs.FileHandle, dest []byte, offset int64) (gofuse.ReadResult, syscall.Errno) {
	handle, ok := fh.(*fileHandle)
	if !ok || handle == nil || offset < 0 {
		return nil, syscall.EBADF
	}
	result, err := n.layer.Read(orchestration.ReadRequest{
		Context: ctx, Handle: handle.handle, ObjectID: n.objectID,
		Offset: uint64(offset), Size: uint32(len(dest)),
	})
	if err != nil {
		return nil, gofuse.ToErrno(err)
	}
	return gofuse.ReadResultData(result.Data), 0
}

func (n *node) Write(ctx context.Context, fh fs.FileHandle, data []byte, offset int64) (uint32, syscall.Errno) {
	handle, ok := fh.(*fileHandle)
	if !ok || handle == nil || offset < 0 {
		return 0, syscall.EBADF
	}
	result, err := n.layer.Write(orchestration.WriteRequest{
		Context: ctx, Handle: handle.handle, ObjectID: n.objectID,
		Offset: uint64(offset), Data: data,
	})
	if err != nil {
		return 0, gofuse.ToErrno(err)
	}
	return result.Written, 0
}

func (n *node) Flush(ctx context.Context, fh fs.FileHandle) syscall.Errno {
	handle, ok := fh.(*fileHandle)
	if !ok || handle == nil {
		return syscall.EBADF
	}
	_, err := n.layer.Flush(orchestration.FlushRequest{Context: ctx, Handle: handle.handle})
	return gofuse.ToErrno(err)
}

func (n *node) Fsync(ctx context.Context, fh fs.FileHandle, _ uint32) syscall.Errno {
	handle, ok := fh.(*fileHandle)
	if !ok || handle == nil {
		return syscall.EBADF
	}
	_, err := n.layer.Fsync(orchestration.FsyncRequest{Context: ctx, Handle: handle.handle})
	return gofuse.ToErrno(err)
}

func (n *node) Release(ctx context.Context, fh fs.FileHandle) syscall.Errno {
	handle, ok := fh.(*fileHandle)
	if !ok || handle == nil {
		return syscall.EBADF
	}
	_, err := n.layer.Release(orchestration.ReleaseRequest{Context: ctx, Handle: handle.handle})
	return gofuse.ToErrno(err)
}

func (n *node) Create(ctx context.Context, name string, flags, mode uint32, out *gofuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	credentials := caller(ctx)
	result, err := n.layer.Create(orchestration.CreateRequest{
		Context: ctx, ParentID: n.objectID, Name: name,
		Mode: mode, UID: credentials.Uid, GID: credentials.Gid,
	})
	if err != nil {
		return nil, nil, 0, gofuse.ToErrno(err)
	}
	attributes, err := n.layer.GetAttr(orchestration.GetAttrRequest{Context: ctx, ObjectID: result.ObjectID})
	if err != nil {
		return nil, nil, 0, gofuse.ToErrno(err)
	}
	node := &node{layer: n.layer, objectID: result.ObjectID}
	inode := n.NewInode(ctx, node, stableAttr(attributes.Attributes))
	fillEntry(out, attributes.Attributes)
	opened, err := n.layer.Open(orchestration.OpenRequest{Context: ctx, ObjectID: result.ObjectID, Flags: flags})
	if err != nil {
		return inode, nil, 0, gofuse.ToErrno(err)
	}
	return inode, &fileHandle{layer: n.layer, objectID: result.ObjectID, handle: opened.Handle}, 0, 0
}

func (n *node) Mkdir(ctx context.Context, name string, mode uint32, out *gofuse.EntryOut) (*fs.Inode, syscall.Errno) {
	credentials := caller(ctx)
	result, err := n.layer.Mkdir(orchestration.MkdirRequest{
		Context: ctx, ParentID: n.objectID, Name: name,
		Mode: mode, UID: credentials.Uid, GID: credentials.Gid,
	})
	if err != nil {
		return nil, gofuse.ToErrno(err)
	}
	attributes, err := n.layer.GetAttr(orchestration.GetAttrRequest{Context: ctx, ObjectID: result.ObjectID})
	if err != nil {
		return nil, gofuse.ToErrno(err)
	}
	child := &node{layer: n.layer, objectID: result.ObjectID}
	inode := n.NewInode(ctx, child, stableAttr(attributes.Attributes))
	fillEntry(out, attributes.Attributes)
	return inode, 0
}

func (n *node) Unlink(ctx context.Context, name string) syscall.Errno {
	_, err := n.layer.Unlink(orchestration.UnlinkRequest{Context: ctx, ParentID: n.objectID, Name: name})
	return gofuse.ToErrno(err)
}

func (n *node) Rmdir(ctx context.Context, name string) syscall.Errno {
	_, err := n.layer.Rmdir(orchestration.RmdirRequest{Context: ctx, ParentID: n.objectID, Name: name})
	return gofuse.ToErrno(err)
}

func (n *node) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	if flags != 0 {
		return syscall.EINVAL
	}
	destination, ok := newParent.EmbeddedInode().Operations().(*node)
	if !ok {
		return syscall.EXDEV
	}
	_, err := n.layer.Rename(orchestration.RenameRequest{
		Context: ctx, SourceParentID: n.objectID, SourceName: name,
		DestinationParentID: destination.objectID, DestinationName: newName,
	})
	return gofuse.ToErrno(err)
}

func (n *node) Link(ctx context.Context, existing fs.InodeEmbedder, name string, out *gofuse.EntryOut) (*fs.Inode, syscall.Errno) {
	source, ok := existing.EmbeddedInode().Operations().(*node)
	if !ok {
		return nil, syscall.EXDEV
	}
	result, err := n.layer.Link(orchestration.LinkRequest{Context: ctx, SourceID: source.objectID, ParentID: n.objectID, Name: name})
	if err != nil {
		return nil, gofuse.ToErrno(err)
	}
	attributes, err := n.layer.GetAttr(orchestration.GetAttrRequest{Context: ctx, ObjectID: result.ObjectID})
	if err != nil {
		return nil, gofuse.ToErrno(err)
	}
	child := &node{layer: n.layer, objectID: result.ObjectID}
	inode := n.NewInode(ctx, child, stableAttr(attributes.Attributes))
	fillEntry(out, attributes.Attributes)
	return inode, 0
}

func (n *node) Symlink(ctx context.Context, target, name string, out *gofuse.EntryOut) (*fs.Inode, syscall.Errno) {
	credentials := caller(ctx)
	result, err := n.layer.Symlink(orchestration.SymlinkRequest{
		Context: ctx, ParentID: n.objectID, Name: name, Target: target,
		UID: credentials.Uid, GID: credentials.Gid,
	})
	if err != nil {
		return nil, gofuse.ToErrno(err)
	}
	attributes, err := n.layer.GetAttr(orchestration.GetAttrRequest{Context: ctx, ObjectID: result.ObjectID})
	if err != nil {
		return nil, gofuse.ToErrno(err)
	}
	child := &node{layer: n.layer, objectID: result.ObjectID}
	inode := n.NewInode(ctx, child, stableAttr(attributes.Attributes))
	fillEntry(out, attributes.Attributes)
	return inode, 0
}

func (n *node) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	result, err := n.layer.ReadLink(orchestration.ReadLinkRequest{Context: ctx, ObjectID: n.objectID})
	if err != nil {
		return nil, gofuse.ToErrno(err)
	}
	return []byte(result.Target), 0
}

func (n *node) Setattr(ctx context.Context, fh fs.FileHandle, in *gofuse.SetAttrIn, out *gofuse.AttrOut) syscall.Errno {
	request := orchestration.SetAttrRequest{Context: ctx, ObjectID: n.objectID}
	if handle, ok := fh.(*fileHandle); ok && handle != nil {
		request.Handle = handle.handle
	}
	if mode, ok := in.GetMode(); ok {
		request.Mode = &mode
	}
	if uid, ok := in.GetUID(); ok {
		request.UID = &uid
	}
	if gid, ok := in.GetGID(); ok {
		request.GID = &gid
	}
	if size, ok := in.GetSize(); ok {
		request.Size = &size
	}
	if atime, ok := in.GetATime(); ok {
		if in.Valid&gofuse.FATTR_ATIME_NOW != 0 {
			request.AtimeNow = true
		} else {
			request.Atime = &atime
		}
	}
	if mtime, ok := in.GetMTime(); ok {
		if in.Valid&gofuse.FATTR_MTIME_NOW != 0 {
			request.MtimeNow = true
		} else {
			request.Mtime = &mtime
		}
	}
	if _, err := n.layer.SetAttr(request); err != nil {
		return gofuse.ToErrno(err)
	}
	return n.Getattr(ctx, fh, out)
}

func (n *node) Statfs(ctx context.Context, out *gofuse.StatfsOut) syscall.Errno {
	result, err := n.layer.StatFs(orchestration.StatFsRequest{Context: ctx})
	if err != nil {
		return gofuse.ToErrno(err)
	}
	out.Blocks, out.Bfree, out.Bavail = result.Blocks, result.Bfree, result.Bavail
	out.Bsize, out.Frsize = uint32(result.Bsize), uint32(result.Frsize)
	out.Files, out.Ffree = result.Files, result.Ffree
	return 0
}

type fileHandle struct {
	layer    *orchestration.Orchestration
	objectID shared.ObjectID
	handle   *orchestration.FileHandle
}

func stableAttr(attributes orchestration.ObjectAttributes) fs.StableAttr {
	return fs.StableAttr{Mode: attributes.Mode & syscall.S_IFMT, Ino: inodeNumber(attributes.ObjectID)}
}

func inodeNumber(id shared.ObjectID) uint64 {
	value := uint64(id[0])<<56 | uint64(id[1])<<48 | uint64(id[2])<<40 | uint64(id[3])<<32 |
		uint64(id[4])<<24 | uint64(id[5])<<16 | uint64(id[6])<<8 | uint64(id[7])
	if value == 0 {
		return 1
	}
	return value
}

func fillEntry(out *gofuse.EntryOut, attributes orchestration.ObjectAttributes) {
	fillAttr(&out.Attr, attributes)
}

func fillAttr(out *gofuse.Attr, attributes orchestration.ObjectAttributes) {
	out.Ino = inodeNumber(attributes.ObjectID)
	out.Mode = attributes.Mode
	out.Nlink = attributes.Nlink
	out.Size = attributes.Size
	out.Owner = gofuse.Owner{Uid: attributes.UID, Gid: attributes.GID}
	out.Atime = uint64(attributes.Atime.Unix())
	out.Atimensec = uint32(attributes.Atime.Nanosecond())
	out.Mtime = uint64(attributes.Mtime.Unix())
	out.Mtimensec = uint32(attributes.Mtime.Nanosecond())
	out.Ctime = uint64(attributes.Ctime.Unix())
	out.Ctimensec = uint32(attributes.Ctime.Nanosecond())
	out.Blksize = 4096
	out.Blocks = (attributes.Size + 511) / 512
}

func caller(ctx context.Context) gofuse.Owner {
	if c, ok := gofuse.FromContext(ctx); ok && c != nil {
		return *c
	}
	return gofuse.Owner{Uid: uint32(os.Getuid()), Gid: uint32(os.Getgid())}
}

// MountOptions returns conservative go-fuse defaults for FlexRAID. Advisory
// locks are intentionally not enabled until their callback contract is ready.
func MountOptions() *fs.Options {
	return &fs.Options{NullPermissions: true}
}

// Mount mounts the adapter and returns the go-fuse server.
func (filesystem *FileSystem) Mount(mountpoint string, options *fs.Options) (*fs.Server, error) {
	if options == nil {
		options = MountOptions()
	}
	server, err := fs.Mount(mountpoint, filesystem.root, options)
	if err != nil {
		return nil, err
	}
	return server, nil
}

// ParseObjectID is a small convenience for command-line mount setup.
func ParseObjectID(value string) (shared.ObjectID, error) { return shared.ParseObjectID(value) }

