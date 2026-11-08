package fuse

import (
	"context"
	"os"
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fs"
	gofuse "github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tetsup/flexraid/distribution"
	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/orchestration"
)

type foreignOperationsNode struct {
	fs.Inode
}

func newCallbackTestFilesystem(t *testing.T) (*FileSystem, context.Context) {
	adapter, ctx, _ := newCallbackTestFilesystemWithIORoots(t)
	return adapter, ctx
}

func newCallbackTestFilesystemWithIORoots(t *testing.T) (*FileSystem, context.Context, []string) {
	t.Helper()
	ios := make([]*physicalio.IO, 4)
	roots := make([]string, len(ios))
	for i := range ios {
		roots[i] = t.TempDir()
		ios[i] = physicalio.New(physicalio.NewRequest{Root: roots[i]}).Layer
	}
	distributed, err := distribution.New(distribution.NewRequest{IOs: ios})
	if err != nil {
		t.Fatalf("initialize Distribution: %v", err)
	}
	root, err := orchestration.CreateRoot(orchestration.CreateRootRequest{
		Distribution: distributed.Layer, Mode: 0755,
		UID: uint32(os.Getuid()), GID: uint32(os.Getgid()),
	})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	adapter, err := New(root.Layer, root.RootID)
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	_ = fs.NewNodeFS(adapter.root, nil)
	return adapter, context.Background(), roots
}

func TestNamespaceCallbacksReturnExpectedErrors(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root

	// Dot components are resolved by the VFS; they are not ordinary
	// directory entry names for a direct Lookup callback.
	for _, name := range []string{"", "a/b"} {
		if _, errno := root.Lookup(ctx, name, &gofuse.EntryOut{}); errno != syscall.EINVAL {
			t.Errorf("Lookup(%q) errno = %v, want EINVAL", name, errno)
		}
	}
	for _, name := range []string{"", ".", "..", "a/b"} {
		if _, _, _, errno := root.Create(ctx, name, uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{}); errno != syscall.EINVAL {
			t.Errorf("Create(%q) errno = %v, want EINVAL", name, errno)
		}
		if _, errno := root.Mkdir(ctx, name, 0755, &gofuse.EntryOut{}); errno != syscall.EINVAL {
			t.Errorf("Mkdir(%q) errno = %v, want EINVAL", name, errno)
		}
		if _, errno := root.Symlink(ctx, "target", name, &gofuse.EntryOut{}); errno != syscall.EINVAL {
			t.Errorf("Symlink(%q) errno = %v, want EINVAL", name, errno)
		}
	}
	for _, operation := range []struct {
		name string
		call func() syscall.Errno
	}{
		{"Create", func() syscall.Errno {
			_, _, _, errno := root.Create(ctx, "bad\x00name", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
			return errno
		}},
		{"Mkdir", func() syscall.Errno {
			_, errno := root.Mkdir(ctx, "bad\x00name", 0755, &gofuse.EntryOut{})
			return errno
		}},
		{"Symlink", func() syscall.Errno {
			_, errno := root.Symlink(ctx, "target", "bad\x00name", &gofuse.EntryOut{})
			return errno
		}},
	} {
		if errno := operation.call(); errno != syscall.EINVAL {
			t.Errorf("%s with NUL-containing name errno = %v, want EINVAL", operation.name, errno)
		}
	}

	if _, errno := root.Lookup(ctx, "missing", &gofuse.EntryOut{}); errno != syscall.ENOENT {
		t.Errorf("Lookup missing errno = %v, want ENOENT", errno)
	}
	if errno := root.Unlink(ctx, "missing"); errno != syscall.ENOENT {
		t.Errorf("Unlink missing errno = %v, want ENOENT", errno)
	}
	if errno := root.Rmdir(ctx, "missing"); errno != syscall.ENOENT {
		t.Errorf("Rmdir missing errno = %v, want ENOENT", errno)
	}
	if errno := root.Rename(ctx, "missing", root, "renamed", 0); errno != syscall.ENOENT {
		t.Errorf("Rename missing source errno = %v, want ENOENT", errno)
	}

	badInode, badHandle, _, badOpenErrno := root.Create(ctx, "bad-open-flags", uint32(syscall.O_ACCMODE), 0600, &gofuse.EntryOut{})
	if badOpenErrno != syscall.EINVAL {
		t.Errorf("Create with invalid access mode errno = %v, want EINVAL", badOpenErrno)
	}
	if badInode != nil || badHandle != nil {
		t.Errorf("Create with invalid access mode returned inode/handle = %v/%v, want nil/nil", badInode, badHandle)
	}
	if _, errno := root.Lookup(ctx, "bad-open-flags", &gofuse.EntryOut{}); errno != syscall.ENOENT {
		t.Errorf("Lookup after failed Create/Open errno = %v, want ENOENT", errno)
	}

	fileInode, fileHandle, _, errno := root.Create(ctx, "regular", uint32(syscall.O_RDWR), 0640, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create regular file: %v", errno)
	}
	fileNode := fileInode.Operations().(*node)
	if _, errno := fileNode.Lookup(ctx, "child", &gofuse.EntryOut{}); errno != syscall.ENOTDIR {
		t.Errorf("Lookup on regular file errno = %v, want ENOTDIR", errno)
	}
	if _, errno := fileNode.Readdir(ctx); errno != syscall.ENOTDIR {
		t.Errorf("Readdir on regular file errno = %v, want ENOTDIR", errno)
	}
	if _, errno := fileNode.Readlink(ctx); errno != syscall.EINVAL {
		t.Errorf("Readlink regular file errno = %v, want EINVAL", errno)
	}
	if _, errno := root.Link(ctx, fileInode, "bad/name", &gofuse.EntryOut{}); errno != syscall.EINVAL {
		t.Errorf("Link invalid name errno = %v, want EINVAL", errno)
	}
	missingSource := &node{layer: root.layer, objectID: testObjectID(t)}
	missingSourceInode := root.NewInode(ctx, missingSource, fs.StableAttr{Mode: syscall.S_IFREG, Ino: inodeNumber(missingSource.objectID)})
	if _, errno := root.Link(ctx, missingSourceInode, "missing-source", &gofuse.EntryOut{}); errno != syscall.ENOENT {
		t.Errorf("Link missing source errno = %v, want ENOENT", errno)
	}
	if _, errno := root.Link(ctx, fileInode, "regular", &gofuse.EntryOut{}); errno != syscall.EEXIST {
		t.Errorf("Link duplicate name errno = %v, want EEXIST", errno)
	}
	if errno := root.Rename(ctx, "regular", root, "bad/name", 0); errno != syscall.EINVAL {
		t.Errorf("Rename invalid destination name errno = %v, want EINVAL", errno)
	}
	if errno := root.Rmdir(ctx, "regular"); errno != syscall.ENOTDIR {
		t.Errorf("Rmdir regular file errno = %v, want ENOTDIR", errno)
	}
	if errno := fileNode.Release(ctx, fileHandle); errno != 0 {
		t.Fatalf("Release created file handle: %v", errno)
	}
	if errno := root.Unlink(ctx, "regular"); errno != 0 {
		t.Fatalf("Unlink regular file: %v", errno)
	}
	if errno := root.Unlink(ctx, "regular"); errno != syscall.ENOENT {
		t.Errorf("second Unlink errno = %v, want ENOENT", errno)
	}

	dirInode, errno := root.Mkdir(ctx, "directory", 0755, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Mkdir directory: %v", errno)
	}
	if _, errno := root.Link(ctx, dirInode, "directory-link", &gofuse.EntryOut{}); errno != syscall.EPERM {
		t.Errorf("Link directory errno = %v, want EPERM", errno)
	}
	if errno := root.Unlink(ctx, "directory"); errno != syscall.EISDIR {
		t.Errorf("Unlink directory errno = %v, want EISDIR", errno)
	}
	if errno := root.Rmdir(ctx, "directory"); errno != 0 {
		t.Errorf("Rmdir empty directory errno = %v, want success", errno)
	}

	if _, errno := root.Symlink(ctx, "", "empty-target", &gofuse.EntryOut{}); errno != syscall.ENOENT {
		t.Errorf("Symlink empty target errno = %v, want ENOENT", errno)
	}
	if _, errno := root.Symlink(ctx, "bad\x00target", "nul-target", &gofuse.EntryOut{}); errno != syscall.EINVAL {
		t.Errorf("Symlink NUL target errno = %v, want EINVAL", errno)
	}
}

func TestDataCallbacksRejectWrongNodeAndInvalidHandles(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root
	dirInode, errno := root.Mkdir(ctx, "directory", 0755, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Mkdir: %v", errno)
	}
	dir := dirInode.Operations().(*node)

	if _, _, errno := dir.Open(ctx, uint32(syscall.O_WRONLY)); errno != syscall.EISDIR {
		t.Errorf("Open directory for writing errno = %v, want EISDIR", errno)
	}
	if _, errno := dir.Readlink(ctx); errno != syscall.EINVAL {
		t.Errorf("Readlink directory errno = %v, want EINVAL", errno)
	}

	missing := &node{layer: adapter.layer, objectID: testObjectID(t)}
	var attr gofuse.AttrOut
	if errno := missing.Getattr(ctx, nil, &attr); errno != syscall.ENOENT {
		t.Errorf("Getattr missing ObjectID errno = %v, want ENOENT", errno)
	}
	if _, errno := missing.Readdir(ctx); errno != syscall.ENOENT {
		t.Errorf("Readdir missing ObjectID errno = %v, want ENOENT", errno)
	}
	if _, errno := missing.Lookup(ctx, "anything", &gofuse.EntryOut{}); errno != syscall.ENOENT {
		t.Errorf("Lookup missing parent errno = %v, want ENOENT", errno)
	}
	if _, errno := missing.Readlink(ctx); errno != syscall.ENOENT {
		t.Errorf("Readlink missing ObjectID errno = %v, want ENOENT", errno)
	}
	if _, _, errno := missing.Open(ctx, uint32(syscall.O_RDONLY)); errno != syscall.ENOENT {
		t.Errorf("Open missing ObjectID errno = %v, want ENOENT", errno)
	}
	fileInode, fileHandle, _, createErrno := root.Create(ctx, "handle-source", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
	if createErrno != 0 {
		t.Fatalf("Create handle source: %v", createErrno)
	}
	if _, errno := missing.Read(ctx, fileHandle, make([]byte, 1), 0); errno != syscall.ENOENT {
		t.Errorf("Read missing ObjectID errno = %v, want ENOENT", errno)
	}
	if _, errno := missing.Write(ctx, fileHandle, []byte("x"), 0); errno != syscall.ENOENT {
		t.Errorf("Write missing ObjectID errno = %v, want ENOENT", errno)
	}
	if errno := fileInode.Operations().(*node).Release(ctx, fileHandle); errno != 0 {
		t.Errorf("Release handle source: %v", errno)
	}
	if errno := root.Unlink(ctx, "handle-source"); errno != 0 {
		t.Errorf("Unlink handle source: %v", errno)
	}
}

func TestAdapterAndParentTypeErrors(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root
	if _, err := New(adapter.layer, testObjectID(t)); err == nil {
		t.Error("New with missing root ObjectID succeeded")
	}
	file, _, _, errno := root.Create(ctx, "file", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create file: %v", errno)
	}
	fileNode := file.Operations().(*node)
	if _, err := New(adapter.layer, fileNode.objectID); err == nil {
		t.Error("New with regular-file root succeeded")
	}
	if _, _, _, errno := fileNode.Create(ctx, "child", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{}); errno != syscall.ENOTDIR {
		t.Errorf("Create with non-directory parent errno = %v, want ENOTDIR", errno)
	}
	if _, errno := fileNode.Mkdir(ctx, "child-dir", 0755, &gofuse.EntryOut{}); errno != syscall.ENOTDIR {
		t.Errorf("Mkdir with non-directory parent errno = %v, want ENOTDIR", errno)
	}
	if _, errno := fileNode.Symlink(ctx, "target", "child-link", &gofuse.EntryOut{}); errno != syscall.ENOTDIR {
		t.Errorf("Symlink with non-directory parent errno = %v, want ENOTDIR", errno)
	}
	if errno := fileNode.Unlink(ctx, "anything"); errno != syscall.ENOTDIR {
		t.Errorf("Unlink with non-directory parent errno = %v, want ENOTDIR", errno)
	}
	if errno := fileNode.Rmdir(ctx, "anything"); errno != syscall.ENOTDIR {
		t.Errorf("Rmdir with non-directory parent errno = %v, want ENOTDIR", errno)
	}
	if errno := root.Rename(ctx, "file", file, "child", 0); errno != syscall.ENOTDIR {
		t.Errorf("Rename to non-directory parent errno = %v, want ENOTDIR", errno)
	}

	missingParent := &node{layer: adapter.layer, objectID: testObjectID(t)}
	missingParentInode := root.NewInode(ctx, missingParent, fs.StableAttr{Mode: syscall.S_IFDIR, Ino: inodeNumber(missingParent.objectID)})
	if _, errno := missingParent.Link(ctx, file, "missing-parent-link", &gofuse.EntryOut{}); errno != syscall.ENOENT {
		t.Errorf("Link with missing parent errno = %v, want ENOENT", errno)
	}
	if errno := missingParent.Rename(ctx, "missing", root, "new", 0); errno != syscall.ENOENT {
		t.Errorf("Rename with missing source parent errno = %v, want ENOENT", errno)
	}
	if errno := root.Rename(ctx, "file", missingParentInode, "new", 0); errno != syscall.ENOENT {
		t.Errorf("Rename with missing destination parent errno = %v, want ENOENT", errno)
	}

	foreignOperations := &foreignOperationsNode{}
	_ = fs.NewNodeFS(foreignOperations, nil)
	if _, errno := root.Link(ctx, foreignOperations, "foreign-operations-link", &gofuse.EntryOut{}); errno != syscall.EXDEV {
		t.Errorf("Link from incompatible operations type errno = %v, want EXDEV", errno)
	}
	if errno := root.Rename(ctx, "file", foreignOperations, "foreign-operations-move", 0); errno != syscall.EXDEV {
		t.Errorf("Rename to incompatible operations type errno = %v, want EXDEV", errno)
	}

	foreign, foreignCtx := newCallbackTestFilesystem(t)
	if _, errno := root.Link(ctx, foreign.root, "foreign-link", &gofuse.EntryOut{}); errno != syscall.EXDEV {
		t.Errorf("Link from another filesystem errno = %v, want EXDEV", errno)
	}
	if errno := root.Rename(ctx, "file", foreign.root, "foreign-move", 0); errno != syscall.EXDEV {
		t.Errorf("Rename to another filesystem errno = %v, want EXDEV", errno)
	}
	_ = foreignCtx
}
