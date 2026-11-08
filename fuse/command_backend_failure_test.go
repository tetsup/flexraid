package fuse

import (
	"bytes"
	"os"
	"syscall"
	"testing"

	gofuse "github.com/hanwen/go-fuse/v2/fuse"
)

func TestNamespaceAndPersistenceFailuresDoNotPublishPartialChanges(t *testing.T) {
	adapter, ctx, roots := newCallbackTestFilesystemWithIORoots(t)
	root := adapter.root

	sourceInode, sourceHandle, _, errno := root.Create(ctx, "source", uint32(syscall.O_RDWR), 0640, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create source: %v", errno)
	}
	source := sourceInode.Operations().(*node)
	if written, errno := source.Write(ctx, sourceHandle, []byte("cached-data"), 0); errno != 0 || written != uint32(len("cached-data")) {
		t.Fatalf("Write before backend failure = %d, %v", written, errno)
	}
	dirInode, errno := root.Mkdir(ctx, "empty-dir", 0755, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Mkdir empty-dir: %v", errno)
	}
	linkInode, errno := root.Symlink(ctx, "target", "existing-link", &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Symlink existing-link: %v", errno)
	}

	// Replace each I/O root directory with a regular file. Every physical
	// chunk/header write now fails with ENOTDIR, while the in-memory Namespace
	// and already-cached file data remain available for consistency checks.
	for _, path := range roots {
		if err := os.Rename(path, path+".saved"); err != nil {
			t.Fatalf("move I/O root %q: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("not a directory"), 0600); err != nil {
			t.Fatalf("replace I/O root %q with file: %v", path, err)
		}
	}

	if _, _, _, errno := root.Create(ctx, "failed-create", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{}); errno == 0 {
		t.Error("Create with unavailable physical storage succeeded")
	}
	if _, errno := root.Lookup(ctx, "failed-create", &gofuse.EntryOut{}); errno != syscall.ENOENT {
		t.Errorf("Lookup failed Create name errno = %v, want ENOENT", errno)
	}
	if _, errno := root.Mkdir(ctx, "failed-mkdir", 0755, &gofuse.EntryOut{}); errno == 0 {
		t.Error("Mkdir with unavailable physical storage succeeded")
	}
	if _, errno := root.Lookup(ctx, "failed-mkdir", &gofuse.EntryOut{}); errno != syscall.ENOENT {
		t.Errorf("Lookup failed Mkdir name errno = %v, want ENOENT", errno)
	}
	if _, errno := root.Symlink(ctx, "target", "failed-symlink", &gofuse.EntryOut{}); errno == 0 {
		t.Error("Symlink with unavailable physical storage succeeded")
	}
	if _, errno := root.Lookup(ctx, "failed-symlink", &gofuse.EntryOut{}); errno != syscall.ENOENT {
		t.Errorf("Lookup failed Symlink name errno = %v, want ENOENT", errno)
	}
	if _, errno := root.Link(ctx, sourceInode, "failed-link", &gofuse.EntryOut{}); errno == 0 {
		t.Error("Link with unavailable physical storage succeeded")
	}
	if _, errno := root.Lookup(ctx, "failed-link", &gofuse.EntryOut{}); errno != syscall.ENOENT {
		t.Errorf("Lookup failed Link name errno = %v, want ENOENT", errno)
	}

	if errno := root.Unlink(ctx, "source"); errno == 0 {
		t.Error("Unlink with unavailable physical storage succeeded")
	}
	if _, errno := root.Lookup(ctx, "source", &gofuse.EntryOut{}); errno != 0 {
		t.Errorf("source disappeared after failed Unlink: %v", errno)
	}
	if errno := root.Rmdir(ctx, "empty-dir"); errno == 0 {
		t.Error("Rmdir with unavailable physical storage succeeded")
	}
	if _, errno := root.Lookup(ctx, "empty-dir", &gofuse.EntryOut{}); errno != 0 {
		t.Errorf("directory disappeared after failed Rmdir: %v", errno)
	}
	if errno := root.Rename(ctx, "source", root, "failed-rename", 0); errno == 0 {
		t.Error("Rename with unavailable physical storage succeeded")
	}
	if _, errno := root.Lookup(ctx, "source", &gofuse.EntryOut{}); errno != 0 {
		t.Errorf("source disappeared after failed Rename: %v", errno)
	}
	if _, errno := root.Lookup(ctx, "failed-rename", &gofuse.EntryOut{}); errno != syscall.ENOENT {
		t.Errorf("failed Rename destination lookup errno = %v, want ENOENT", errno)
	}

	before := &gofuse.AttrOut{}
	if errno := source.Getattr(ctx, nil, before); errno != 0 {
		t.Fatalf("Getattr before failed Setattr: %v", errno)
	}
	setattr := &gofuse.SetAttrIn{}
	setattr.Valid, setattr.Mode = gofuse.FATTR_MODE, 0600
	if errno := source.Setattr(ctx, nil, setattr, &gofuse.AttrOut{}); errno == 0 {
		t.Error("Setattr with unavailable physical storage succeeded")
	}
	after := &gofuse.AttrOut{}
	if errno := source.Getattr(ctx, nil, after); errno != 0 {
		t.Fatalf("Getattr after failed Setattr: %v", errno)
	}
	if after.Attr.Mode != before.Attr.Mode {
		t.Errorf("mode changed after failed Setattr: before=%#o after=%#o", before.Attr.Mode, after.Attr.Mode)
	}

	if errno := source.Flush(ctx, sourceHandle); errno == 0 {
		t.Error("Flush with unavailable physical storage succeeded")
	}
	if errno := source.Fsync(ctx, sourceHandle, 0); errno == 0 {
		t.Error("Fsync with unavailable physical storage succeeded")
	}
	read, errno := source.Read(ctx, sourceHandle, make([]byte, len("cached-data")), 0)
	if errno != 0 {
		t.Fatalf("Read dirty cached data after failed persistence: %v", errno)
	}
	got, status := read.Bytes(make([]byte, len("cached-data")))
	if status != 0 || !bytes.Equal(got, []byte("cached-data")) {
		t.Errorf("cached data after failed persistence = %q, %v; want cached-data", got, status)
	}

	if gotLink, errno := linkInode.Operations().(*node).Readlink(ctx); errno != 0 || string(gotLink) != "target" {
		t.Errorf("existing symlink after unrelated failures = %q, %v", gotLink, errno)
	}
	if errno := source.Release(ctx, sourceHandle); errno != 0 {
		t.Errorf("Release after failed persistence: %v", errno)
	}
	_ = dirInode
}
