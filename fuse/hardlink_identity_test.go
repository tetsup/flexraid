package fuse

import (
	"bytes"
	"context"
	"os"
	"syscall"
	"testing"

	gofuse "github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tetsup/flexraid/distribution"
	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/orchestration"
)

// This exercises the adapter callbacks directly, without requiring /dev/fuse.
// In particular, the inode returned by Link must retain the backing File ID,
// not the internal ObjectID of the HardLink namespace entry.
func TestHardLinkCallbacksKeepBackingFileIdentity(t *testing.T) {
	ctx := context.Background()
	ioLayers := make([]*physicalio.IO, 4)
	for index := range ioLayers {
		ioLayers[index] = physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer
	}
	distributed, err := distribution.New(distribution.NewRequest{IOs: ioLayers})
	if err != nil {
		t.Fatalf("initialize Distribution: %v", err)
	}
	createdRoot, err := orchestration.CreateRoot(orchestration.CreateRootRequest{
		Distribution: distributed.Layer,
		Mode:         0755,
		UID:          uint32(os.Getuid()),
		GID:          uint32(os.Getgid()),
	})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	adapter, err := New(createdRoot.Layer, createdRoot.RootID)
	if err != nil {
		t.Fatalf("create FUSE adapter: %v", err)
	}
	root := adapter.root

	created, err := root.layer.Create(orchestration.CreateRequest{
		Context: ctx, ParentID: createdRoot.RootID, Name: "original", Mode: 0640,
		UID: uint32(os.Getuid()), GID: uint32(os.Getgid()),
	})
	if err != nil {
		t.Fatalf("create backing File: %v", err)
	}
	payload := []byte("one backing file, two names")
	opened, err := root.layer.Open(orchestration.OpenRequest{
		Context: ctx, ObjectID: created.ObjectID, Flags: uint32(syscall.O_RDWR),
	})
	if err != nil {
		t.Fatalf("open backing File: %v", err)
	}
	if _, err := root.layer.Write(orchestration.WriteRequest{
		Context: ctx, Handle: opened.Handle, ObjectID: created.ObjectID, Data: payload,
	}); err != nil {
		t.Fatalf("write backing File: %v", err)
	}
	if _, err := root.layer.Flush(orchestration.FlushRequest{Context: ctx, Handle: opened.Handle}); err != nil {
		t.Fatalf("flush backing File: %v", err)
	}
	if _, err := root.layer.Release(orchestration.ReleaseRequest{Context: ctx, Handle: opened.Handle}); err != nil {
		t.Fatalf("release backing File: %v", err)
	}

	var originalEntry gofuse.EntryOut
	originalInode, errno := root.Lookup(ctx, "original", &originalEntry)
	if errno != 0 {
		t.Fatalf("Lookup original: %v", errno)
	}
	originalNode, ok := originalInode.Operations().(*node)
	if !ok {
		t.Fatalf("original inode operations have type %T", originalInode.Operations())
	}
	if originalNode.objectID != created.ObjectID {
		t.Fatalf("original node ObjectID = %s, want backing File %s", originalNode.objectID, created.ObjectID)
	}

	var aliasEntry gofuse.EntryOut
	aliasInode, errno := root.Link(ctx, originalInode, "alias", &aliasEntry)
	if errno != 0 {
		t.Fatalf("Link: %v", errno)
	}
	aliasNode, ok := aliasInode.Operations().(*node)
	if !ok {
		t.Fatalf("alias inode operations have type %T", aliasInode.Operations())
	}
	if aliasNode.objectID != created.ObjectID {
		t.Errorf("node returned by Link retains ObjectID %s; want backing File ObjectID %s (HardLink ObjectID must not escape into FUSE)", aliasNode.objectID, created.ObjectID)
	}
	if aliasEntry.Attr.Ino != inodeNumber(created.ObjectID) {
		t.Errorf("Link EntryOut inode = %d, want backing inode %d", aliasEntry.Attr.Ino, inodeNumber(created.ObjectID))
	}
	if aliasEntry.Attr.Mode&syscall.S_IFMT != syscall.S_IFREG {
		t.Errorf("Link EntryOut mode = %#o, want regular file", aliasEntry.Attr.Mode)
	}

	stream, errno := root.Readdir(ctx)
	if errno != 0 {
		t.Fatalf("Readdir: %v", errno)
	}
	defer stream.Close()
	foundAlias := false
	for stream.HasNext() {
		entry, readErrno := stream.Next()
		if readErrno != 0 {
			t.Fatalf("read Readdir entry: %v", readErrno)
		}
		if entry.Name == "alias" {
			foundAlias = true
			if entry.Ino != inodeNumber(created.ObjectID) {
				t.Errorf("Readdir alias inode = %d, want backing inode %d", entry.Ino, inodeNumber(created.ObjectID))
			}
			if entry.Mode&syscall.S_IFMT != syscall.S_IFREG {
				t.Errorf("Readdir alias mode = %#o, want regular file", entry.Mode)
			}
		}
	}
	if !foundAlias {
		t.Error("Readdir did not return alias")
	}

	var lookedUpEntry gofuse.EntryOut
	lookedUpInode, errno := root.Lookup(ctx, "alias", &lookedUpEntry)
	if errno != 0 {
		t.Fatalf("Lookup alias: %v", errno)
	}
	lookedUpNode, ok := lookedUpInode.Operations().(*node)
	if !ok {
		t.Fatalf("looked-up alias inode operations have type %T", lookedUpInode.Operations())
	}
	if lookedUpNode.objectID != created.ObjectID {
		t.Errorf("node returned by Lookup(alias) retains ObjectID %s, want backing File %s", lookedUpNode.objectID, created.ObjectID)
	}

	var aliasAttr gofuse.AttrOut
	if errno := aliasNode.Getattr(ctx, nil, &aliasAttr); errno != 0 {
		t.Errorf("Getattr alias: %v", errno)
	} else {
		if aliasAttr.Attr.Ino != inodeNumber(created.ObjectID) {
			t.Errorf("Getattr alias inode = %d, want %d", aliasAttr.Attr.Ino, inodeNumber(created.ObjectID))
		}
		if aliasAttr.Attr.Nlink != 2 || aliasAttr.Attr.Size != uint64(len(payload)) {
			t.Errorf("Getattr alias nlink/size = %d/%d, want 2/%d", aliasAttr.Attr.Nlink, aliasAttr.Attr.Size, len(payload))
		}
	}

	handle, _, errno := aliasNode.Open(ctx, uint32(syscall.O_RDONLY))
	if errno != 0 {
		t.Fatalf("Open through alias: %v", errno)
	}
	readResult, errno := aliasNode.Read(ctx, handle, make([]byte, len(payload)), 0)
	if errno != 0 {
		t.Fatalf("Read through alias: %v", errno)
	}
	readData, status := readResult.Bytes(make([]byte, len(payload)))
	if status != 0 {
		t.Fatalf("decode Read result: %v", status)
	}
	if !bytes.Equal(readData, payload) {
		t.Errorf("Read through alias = %q, want %q", readData, payload)
	}
	if errno := aliasNode.Release(ctx, handle); errno != 0 {
		t.Errorf("Release alias handle: %v", errno)
	}

	// Namespace mutations must continue to address a name, not the backing
	// ObjectID: removing the original name must leave the alias usable.
	if errno := root.Unlink(ctx, "original"); errno != 0 {
		t.Fatalf("Unlink original name: %v", errno)
	}
	var survivingAttr gofuse.AttrOut
	if errno := aliasNode.Getattr(ctx, nil, &survivingAttr); errno != 0 {
		t.Errorf("Getattr surviving alias: %v", errno)
	} else if survivingAttr.Attr.Nlink != 1 {
		t.Errorf("surviving alias nlink = %d, want 1", survivingAttr.Attr.Nlink)
	}
	if errno := root.Rename(ctx, "alias", root, "renamed", 0); errno != 0 {
		t.Fatalf("Rename surviving alias: %v", errno)
	}
	var renamedEntry gofuse.EntryOut
	renamedInode, errno := root.Lookup(ctx, "renamed", &renamedEntry)
	if errno != 0 {
		t.Fatalf("Lookup renamed alias: %v", errno)
	}
	renamedNode, ok := renamedInode.Operations().(*node)
	if !ok {
		t.Fatalf("renamed inode operations have type %T", renamedInode.Operations())
	}
	if renamedNode.objectID != created.ObjectID {
		t.Errorf("renamed node ObjectID = %s, want backing File %s", renamedNode.objectID, created.ObjectID)
	}
	if errno := root.Unlink(ctx, "renamed"); errno != 0 {
		t.Errorf("Unlink renamed alias: %v", errno)
	}
	if _, errno := root.Lookup(ctx, "renamed", &gofuse.EntryOut{}); errno != syscall.ENOENT {
		t.Errorf("Lookup after final Unlink errno = %v, want ENOENT", errno)
	}

}
