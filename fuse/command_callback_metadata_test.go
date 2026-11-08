package fuse

import (
	"os"
	"syscall"
	"strings"
	"syscall"
	"testing"

	gofuse "github.com/hanwen/go-fuse/v2/fuse"
)

func TestNamespaceCallbacksReturnConsistentEntryMetadata(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root

	fileInode, fileHandle, _, errno := root.Create(ctx, "file", uint32(syscall.O_RDWR), 0640, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create: %v", errno)
	}
	fileNode := fileInode.Operations().(*node)
	var fileAttr gofuse.AttrOut
	if errno := fileNode.Getattr(ctx, fileHandle, &fileAttr); errno != 0 {
		t.Fatalf("Getattr created file: %v", errno)
	}
	if fileAttr.Attr.Mode != syscall.S_IFREG|0640 || fileAttr.Attr.Nlink != 1 || fileAttr.Attr.Size != 0 {
		t.Errorf("created file attributes = mode %#o nlink %d size %d", fileAttr.Attr.Mode, fileAttr.Attr.Nlink, fileAttr.Attr.Size)
	}
	if fileAttr.Attr.Uid != uint32(os.Getuid()) || fileAttr.Attr.Gid != uint32(os.Getgid()) {
		t.Errorf("created file owner = %d:%d, want %d:%d", fileAttr.Attr.Uid, fileAttr.Attr.Gid, os.Getuid(), os.Getgid())
	}

	dirInode, errno := root.Mkdir(ctx, "directory", 0750, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Mkdir: %v", errno)
	}
	dirNode := dirInode.Operations().(*node)
	var dirAttr gofuse.AttrOut
	if errno := dirNode.Getattr(ctx, nil, &dirAttr); errno != 0 {
		t.Fatalf("Getattr created directory: %v", errno)
	}
	if dirAttr.Attr.Mode != syscall.S_IFDIR|0750 || dirAttr.Attr.Nlink != 2 {
		t.Errorf("created directory attributes = mode %#o nlink %d, want %#o/2", dirAttr.Attr.Mode, dirAttr.Attr.Nlink, syscall.S_IFDIR|0750)
	}

	const target = "relative/target"
	linkInode, errno := root.Symlink(ctx, target, "symlink", &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Symlink: %v", errno)
	}
	linkNode := linkInode.Operations().(*node)
	var linkAttr gofuse.AttrOut
	if errno := linkNode.Getattr(ctx, nil, &linkAttr); errno != 0 {
		t.Fatalf("Getattr symlink: %v", errno)
	}
	if linkAttr.Attr.Mode != syscall.S_IFLNK|0777 || linkAttr.Attr.Nlink != 1 || linkAttr.Attr.Size != uint64(len(target)) {
		t.Errorf("symlink attributes = mode %#o nlink %d size %d", linkAttr.Attr.Mode, linkAttr.Attr.Nlink, linkAttr.Attr.Size)
	}
	readlink, errno := linkNode.Readlink(ctx)
	if errno != 0 || string(readlink) != target {
		t.Errorf("Readlink = %q, %v; want %q", readlink, errno, target)
	}

	stream, errno := root.Readdir(ctx)
	if errno != 0 {
		t.Fatalf("Readdir: %v", errno)
	}
	defer stream.Close()
	got := make(map[string]gofuse.DirEntry)
	for stream.HasNext() {
		entry, readErrno := stream.Next()
		if readErrno != 0 {
			t.Fatalf("Readdir Next: %v", readErrno)
		}
		got[entry.Name] = entry
	}
	wantModes := map[string]uint32{
		"file":      syscall.S_IFREG,
		"directory": syscall.S_IFDIR,
		"symlink":   syscall.S_IFLNK,
	}
	for name, wantMode := range wantModes {
		entry, exists := got[name]
		if !exists {
			t.Errorf("Readdir omitted %q", name)
			continue
		}
		if entry.Mode&syscall.S_IFMT != wantMode {
			t.Errorf("Readdir %q mode = %#o, want type %#o", name, entry.Mode, wantMode)
		}
	}
	if len(got) != len(wantModes) {
		t.Errorf("Readdir returned %d entries, want %d", len(got), len(wantModes))
	}

	if errno := fileNode.Release(ctx, fileHandle); errno != 0 {
		t.Errorf("Release: %v", errno)
	}

	longTarget := strings.Repeat("long-target/", 400)
	longLinkInode, errno := root.Symlink(ctx, longTarget, "long-symlink", &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Symlink long target: %v", errno)
	}
	longLink := longLinkInode.Operations().(*node)
	longReadlink, errno := longLink.Readlink(ctx)
	if errno != 0 || string(longReadlink) != longTarget {
		t.Errorf("Readlink long target length=%d errno=%v, want exact %d-byte target", len(longReadlink), errno, len(longTarget))
	}
	var longAttr gofuse.AttrOut
	if errno := longLink.Getattr(ctx, nil, &longAttr); errno != 0 {
		t.Errorf("Getattr long symlink: %v", errno)
	} else if longAttr.Attr.Size != uint64(len(longTarget)) {
		t.Errorf("long symlink size = %d, want %d", longAttr.Attr.Size, len(longTarget))
	}
}

func TestStatfsCountsDirectoriesSymlinksAndDistinctHardLinkedFiles(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root
	files := func() uint64 {
		t.Helper()
		var out gofuse.StatfsOut
		if errno := root.Statfs(ctx, &out); errno != 0 {
			t.Fatalf("Statfs: %v", errno)
		}
		return out.Files
	}
	if got := files(); got != 1 {
		t.Fatalf("initial Files = %d, want root only (1)", got)
	}
	if _, errno := root.Mkdir(ctx, "directory", 0755, &gofuse.EntryOut{}); errno != 0 {
		t.Fatalf("Mkdir: %v", errno)
	}
	if got := files(); got != 2 {
		t.Errorf("Files after Mkdir = %d, want 2", got)
	}
	if _, errno := root.Symlink(ctx, "target", "symlink", &gofuse.EntryOut{}); errno != 0 {
		t.Fatalf("Symlink: %v", errno)
	}
	if got := files(); got != 3 {
		t.Errorf("Files after Symlink = %d, want 3", got)
	}
	fileInode, fileHandle, _, errno := root.Create(ctx, "file", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create: %v", errno)
	}
	if errno := fileInode.Operations().(*node).Release(ctx, fileHandle); errno != 0 {
		t.Errorf("Release: %v", errno)
	}
	if got := files(); got != 4 {
		t.Errorf("Files after Create = %d, want 4", got)
	}
	if _, errno := root.Link(ctx, fileInode, "alias", &gofuse.EntryOut{}); errno != 0 {
		t.Fatalf("Link: %v", errno)
	}
	if got := files(); got != 4 {
		t.Errorf("Files after hard link = %d, want 4 distinct objects", got)
	}
	if errno := root.Unlink(ctx, "file"); errno != 0 {
		t.Fatalf("Unlink original name: %v", errno)
	}
	if got := files(); got != 4 {
		t.Errorf("Files after unlinking one hard link = %d, want 4", got)
	}
	if errno := root.Unlink(ctx, "alias"); errno != 0 {
		t.Fatalf("Unlink final hard link: %v", errno)
	}
	if got := files(); got != 3 {
		t.Errorf("Files after final hard-link removal = %d, want 3", got)
	}
	if errno := root.Unlink(ctx, "symlink"); errno != 0 {
		t.Fatalf("Unlink symlink: %v", errno)
	}
	if got := files(); got != 2 {
		t.Errorf("Files after symlink removal = %d, want 2", got)
	}
	if errno := root.Rmdir(ctx, "directory"); errno != 0 {
		t.Fatalf("Rmdir: %v", errno)
	}
	if got := files(); got != 1 {
		t.Errorf("Files after directory removal = %d, want root only (1)", got)
	}
}
