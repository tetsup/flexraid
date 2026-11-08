package fuse

import (
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fuse"
)

func TestRenameCallbackDirectorySemantics(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root

	parentInode, errno := root.Mkdir(ctx, "parent", 0755, &fuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Mkdir parent: %v", errno)
	}
	parent := parentInode.Operations().(*node)
	childInode, errno := parent.Mkdir(ctx, "child", 0755, &fuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Mkdir child: %v", errno)
	}
	if errno := root.Rename(ctx, "parent", childInode, "moved-parent", 0); errno != syscall.EINVAL {
		t.Errorf("Rename directory into descendant errno = %v, want EINVAL", errno)
	}
	if _, errno := root.Lookup(ctx, "parent", &fuse.EntryOut{}); errno != 0 {
		t.Errorf("parent after rejected descendant move: %v", errno)
	}

	sourceInode, errno := root.Mkdir(ctx, "source-empty", 0710, &fuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Mkdir source-empty: %v", errno)
	}
	_, errno = root.Mkdir(ctx, "destination-empty", 0700, &fuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Mkdir destination-empty: %v", errno)
	}
	sourceID := sourceInode.Operations().(*node).objectID
	if errno := root.Rename(ctx, "source-empty", root, "destination-empty", 0); errno != 0 {
		t.Fatalf("Rename over empty directory: %v", errno)
	}
	if _, errno := root.Lookup(ctx, "source-empty", &fuse.EntryOut{}); errno != syscall.ENOENT {
		t.Errorf("source after empty-directory replacement errno = %v, want ENOENT", errno)
	}
	var replaced fuse.EntryOut
	if _, errno := root.Lookup(ctx, "destination-empty", &replaced); errno != 0 {
		t.Fatalf("Lookup replaced directory: %v", errno)
	}
	if replaced.Attr.Ino != inodeNumber(sourceID) || replaced.Attr.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		t.Errorf("replacement directory attributes = ino %d mode %#o, want ino %d directory", replaced.Attr.Ino, replaced.Attr.Mode, inodeNumber(sourceID))
	}
	fileInode, fileHandle, _, errno := root.Create(ctx, "regular", uint32(syscall.O_RDWR), 0600, &fuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create regular: %v", errno)
	}
	if errno := root.Rename(ctx, "regular", root, "destination-empty", 0); errno != syscall.EISDIR {
		t.Errorf("Rename file over directory errno = %v, want EISDIR", errno)
	}
	if errno := root.Rename(ctx, "destination-empty", root, "regular", 0); errno != syscall.ENOTDIR {
		t.Errorf("Rename directory over file errno = %v, want ENOTDIR", errno)
	}
	if _, errno := root.Lookup(ctx, "regular", &fuse.EntryOut{}); errno != 0 {
		t.Errorf("regular file disappeared after failed type-mismatch Rename: %v", errno)
	}
	if _, errno := root.Lookup(ctx, "destination-empty", &fuse.EntryOut{}); errno != 0 {
		t.Errorf("directory disappeared after failed type-mismatch Rename: %v", errno)
	}
	if errno := fileInode.Operations().(*node).Release(ctx, fileHandle); errno != 0 {
		t.Errorf("Release regular: %v", errno)
	}
}


func TestRenameCallbackReplacesNonDirectoryKinds(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root

	fileInode, fileHandle, _, errno := root.Create(ctx, "file-source", uint32(syscall.O_RDWR), 0600, &fuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create file source: %v", errno)
	}
	if _, errno := root.Symlink(ctx, "old-target", "symlink-destination", &fuse.EntryOut{}); errno != 0 {
		t.Fatalf("Symlink destination: %v", errno)
	}
	if errno := root.Rename(ctx, "file-source", root, "symlink-destination", 0); errno != 0 {
		t.Fatalf("Rename file over symlink: %v", errno)
	}
	var replacedFile fuse.EntryOut
	if _, errno := root.Lookup(ctx, "symlink-destination", &replacedFile); errno != 0 {
		t.Fatalf("Lookup file-over-symlink destination: %v", errno)
	}
	if replacedFile.Attr.Mode&syscall.S_IFMT != syscall.S_IFREG {
		t.Errorf("file-over-symlink destination mode = %#o, want regular file", replacedFile.Attr.Mode)
	}
	if _, errno := root.Lookup(ctx, "file-source", &fuse.EntryOut{}); errno != syscall.ENOENT {
		t.Errorf("file source after Rename errno = %v, want ENOENT", errno)
	}
	if errno := fileInode.Operations().(*node).Release(ctx, fileHandle); errno != 0 {
		t.Errorf("Release file source: %v", errno)
	}

	if _, _, _, errno := root.Create(ctx, "file-destination", uint32(syscall.O_RDWR), 0600, &fuse.EntryOut{}); errno != 0 {
		t.Fatalf("Create file destination: %v", errno)
	}
	if _, errno := root.Symlink(ctx, "new-target", "symlink-source", &fuse.EntryOut{}); errno != 0 {
		t.Fatalf("Symlink source: %v", errno)
	}
	if errno := root.Rename(ctx, "symlink-source", root, "file-destination", 0); errno != 0 {
		t.Fatalf("Rename symlink over file: %v", errno)
	}
	var replacedLink fuse.EntryOut
	linkInode, errno := root.Lookup(ctx, "file-destination", &replacedLink)
	if errno != 0 {
		t.Fatalf("Lookup symlink-over-file destination: %v", errno)
	}
	if replacedLink.Attr.Mode&syscall.S_IFMT != syscall.S_IFLNK {
		t.Errorf("symlink-over-file destination mode = %#o, want symlink", replacedLink.Attr.Mode)
	}
	if target, errno := linkInode.Operations().(*node).Readlink(ctx); errno != 0 || string(target) != "new-target" {
		t.Errorf("Readlink replaced destination = %q, %v; want new-target", target, errno)
	}
	if _, errno := root.Lookup(ctx, "symlink-source", &fuse.EntryOut{}); errno != syscall.ENOENT {
		t.Errorf("symlink source after Rename errno = %v, want ENOENT", errno)
	}
}
