package fuse

import (
	"bytes"
	"syscall"
	"testing"
	"time"

	gofuse "github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tetsup/flexraid/orchestration"
)

func TestOpenAppendAndTruncateCallbackSemantics(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root
	inode, initialHandle, _, errno := root.Create(ctx, "open-flags", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create: %v", errno)
	}
	n := inode.Operations().(*node)
	if written, errno := n.Write(ctx, initialHandle, []byte("prefix"), 0); errno != 0 || written != 6 {
		t.Fatalf("initial Write = %d, %v; want 6, success", written, errno)
	}
	if errno := n.Release(ctx, initialHandle); errno != 0 {
		t.Fatalf("Release initial handle: %v", errno)
	}

	appendHandle, _, errno := n.Open(ctx, uint32(syscall.O_WRONLY|syscall.O_APPEND))
	if errno != 0 {
		t.Fatalf("Open O_APPEND: %v", errno)
	}
	if written, errno := n.Write(ctx, appendHandle, []byte("-suffix"), 0); errno != 0 || written != 7 {
		t.Errorf("Write O_APPEND = %d, %v; want 7, success", written, errno)
	}
	if errno := n.Release(ctx, appendHandle); errno != 0 {
		t.Errorf("Release O_APPEND handle: %v", errno)
	}

	readHandle, _, errno := n.Open(ctx, uint32(syscall.O_RDONLY))
	if errno != 0 {
		t.Fatalf("Open read-only after append: %v", errno)
	}
	result, errno := n.Read(ctx, readHandle, make([]byte, 13), 0)
	if errno != 0 {
		t.Fatalf("Read after append: %v", errno)
	}
	got, status := result.Bytes(make([]byte, 13))
	if status != 0 || !bytes.Equal(got, []byte("prefix-suffix")) {
		t.Errorf("content after O_APPEND = %q, %v; want prefix-suffix", got, status)
	}
	if errno := n.Release(ctx, readHandle); errno != 0 {
		t.Errorf("Release read handle: %v", errno)
	}

	truncateHandle, _, errno := n.Open(ctx, uint32(syscall.O_RDWR|syscall.O_TRUNC))
	if errno != 0 {
		t.Fatalf("Open O_TRUNC: %v", errno)
	}
	attr := &gofuse.AttrOut{}
	if errno := n.Getattr(ctx, truncateHandle, attr); errno != 0 {
		t.Fatalf("Getattr after O_TRUNC: %v", errno)
	}
	if attr.Attr.Size != 0 {
		t.Errorf("size after O_TRUNC = %d, want 0", attr.Attr.Size)
	}
	if errno := n.Release(ctx, truncateHandle); errno != 0 {
		t.Errorf("Release O_TRUNC handle: %v", errno)
	}
}

func TestSetattrIndividualFieldsAndPreservesUnspecifiedAttributes(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root
	inode, handle, _, errno := root.Create(ctx, "setattr-fields", uint32(syscall.O_RDWR), 0640, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create: %v", errno)
	}
	n := inode.Operations().(*node)

	mode := uint32(0610)
	modeIn := &gofuse.SetAttrIn{}
	modeIn.Valid, modeIn.Mode = gofuse.FATTR_MODE, mode
	var out gofuse.AttrOut
	if errno := n.Setattr(ctx, nil, modeIn, &out); errno != 0 {
		t.Fatalf("Setattr mode: %v", errno)
	}
	if out.Attr.Mode&07777 != mode {
		t.Errorf("mode after Setattr = %#o, want %#o", out.Attr.Mode&07777, mode)
	}

	uid := uint32(1234)
	uidIn := &gofuse.SetAttrIn{}
	uidIn.Valid, uidIn.Uid = gofuse.FATTR_UID, uid
	if errno := n.Setattr(ctx, nil, uidIn, &out); errno != 0 {
		t.Fatalf("Setattr UID: %v", errno)
	}
	if out.Attr.Uid != uid || out.Attr.Mode&07777 != mode {
		t.Errorf("attributes after UID change = uid %d mode %#o, want uid %d mode %#o", out.Attr.Uid, out.Attr.Mode&07777, uid, mode)
	}

	gid := uint32(2345)
	gidIn := &gofuse.SetAttrIn{}
	gidIn.Valid, gidIn.Gid = gofuse.FATTR_GID, gid
	if errno := n.Setattr(ctx, nil, gidIn, &out); errno != 0 {
		t.Fatalf("Setattr GID: %v", errno)
	}
	if out.Attr.Gid != gid || out.Attr.Uid != uid {
		t.Errorf("attributes after GID change = %d:%d, want %d:%d", out.Attr.Uid, out.Attr.Gid, uid, gid)
	}

	size := uint64(513)
	sizeIn := &gofuse.SetAttrIn{}
	sizeIn.Valid, sizeIn.Size = gofuse.FATTR_SIZE, size
	if errno := n.Setattr(ctx, handle, sizeIn, &out); errno != 0 {
		t.Fatalf("Setattr size: %v", errno)
	}
	if out.Attr.Size != size {
		t.Errorf("size after Setattr = %d, want %d", out.Attr.Size, size)
	}

	atime := time.Unix(1_700_000_000, 999_999_999)
	atimeIn := &gofuse.SetAttrIn{}
	atimeIn.Valid = gofuse.FATTR_ATIME
	atimeIn.Atime, atimeIn.Atimensec = uint64(atime.Unix()), uint32(atime.Nanosecond())
	if errno := n.Setattr(ctx, nil, atimeIn, &out); errno != 0 {
		t.Fatalf("Setattr atime: %v", errno)
	}
	if got := time.Unix(int64(out.Attr.Atime), int64(out.Attr.Atimensec)); !got.Equal(atime) {
		t.Errorf("atime after Setattr = %s, want %s", got, atime)
	}

	mtime := time.Unix(1_700_000_100, 1)
	mtimeIn := &gofuse.SetAttrIn{}
	mtimeIn.Valid = gofuse.FATTR_MTIME
	mtimeIn.Mtime, mtimeIn.Mtimensec = uint64(mtime.Unix()), uint32(mtime.Nanosecond())
	if errno := n.Setattr(ctx, nil, mtimeIn, &out); errno != 0 {
		t.Fatalf("Setattr mtime: %v", errno)
	}
	if got := time.Unix(int64(out.Attr.Mtime), int64(out.Attr.Mtimensec)); !got.Equal(mtime) {
		t.Errorf("mtime after Setattr = %s, want %s", got, mtime)
	}
	if out.Attr.Mode&07777 != mode || out.Attr.Uid != uid || out.Attr.Gid != gid || out.Attr.Size != size {
		t.Errorf("independent Setattr operations lost other fields: %+v", out.Attr)
	}
	if errno := n.Release(ctx, handle); errno != 0 {
		t.Errorf("Release: %v", errno)
	}
}

func TestSetattrNoopDoesNotChangeAttributes(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	inode, handle, _, errno := adapter.root.Create(ctx, "setattr-noop", uint32(syscall.O_RDWR), 0640, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create: %v", errno)
	}
	n := inode.Operations().(*node)
	var before, after gofuse.AttrOut
	if errno := n.Getattr(ctx, nil, &before); errno != 0 {
		t.Fatalf("Getattr before: %v", errno)
	}
	if errno := n.Setattr(ctx, nil, &gofuse.SetAttrIn{}, &after); errno != 0 {
		t.Fatalf("Setattr with no valid fields: %v", errno)
	}
	if before.Attr.Mode != after.Attr.Mode || before.Attr.Uid != after.Attr.Uid ||
		before.Attr.Gid != after.Attr.Gid || before.Attr.Size != after.Attr.Size ||
		before.Attr.Nlink != after.Attr.Nlink || before.Attr.Ino != after.Attr.Ino {
		t.Errorf("no-op Setattr changed attributes: before=%+v after=%+v", before.Attr, after.Attr)
	}
	if errno := n.Release(ctx, handle); errno != 0 {
		t.Errorf("Release: %v", errno)
	}
}

func TestStatfsCallbackCopiesEveryDefinedField(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	expected, err := adapter.layer.StatFs(orchestration.StatFsRequest{Context: ctx})
	if err != nil {
		t.Fatalf("Orchestration StatFs: %v", err)
	}
	var got gofuse.StatfsOut
	if errno := adapter.root.Statfs(ctx, &got); errno != 0 {
		t.Fatalf("FUSE Statfs: %v", errno)
	}
	if got.Blocks != expected.Blocks || got.Bfree != expected.Bfree || got.Bavail != expected.Bavail ||
		got.Bsize != uint32(expected.Bsize) || got.Frsize != uint32(expected.Frsize) ||
		got.Files != expected.Files || got.Ffree != expected.Ffree {
		t.Errorf("FUSE Statfs mapping = %+v, expected orchestration values %+v", got, expected)
	}
}

func TestReadWriteRejectHandleFromDifferentInode(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root
	firstInode, firstHandle, _, errno := root.Create(ctx, "first", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create first: %v", errno)
	}
	secondInode, secondHandle, _, errno := root.Create(ctx, "second", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create second: %v", errno)
	}
	first := firstInode.Operations().(*node)
	second := secondInode.Operations().(*node)
	if _, errno := first.Write(ctx, firstHandle, []byte("first-data"), 0); errno != 0 {
		t.Fatalf("Write first: %v", errno)
	}
	if _, errno := second.Write(ctx, secondHandle, []byte("second-data"), 0); errno != 0 {
		t.Fatalf("Write second: %v", errno)
	}

	if _, errno := second.Read(ctx, firstHandle, make([]byte, 10), 0); errno == 0 {
		t.Error("Read through second inode with first inode's handle succeeded")
	}
	if _, errno := second.Write(ctx, firstHandle, []byte("corrupt"), 0); errno == 0 {
		t.Error("Write through second inode with first inode's handle succeeded")
	}
	if _, errno := first.Read(ctx, secondHandle, make([]byte, 11), 0); errno == 0 {
		t.Error("Read through first inode with second inode's handle succeeded")
	}
	if _, errno := first.Write(ctx, secondHandle, []byte("corrupt"), 0); errno == 0 {
		t.Error("Write through first inode with second inode's handle succeeded")
	}

	firstRead, _, errno := first.Open(ctx, uint32(syscall.O_RDONLY))
	if errno != 0 {
		t.Fatalf("Open first for verification: %v", errno)
	}
	firstResult, errno := first.Read(ctx, firstRead, make([]byte, 10), 0)
	if errno != 0 {
		t.Fatalf("Read first for verification: %v", errno)
	}
	firstData, status := firstResult.Bytes(make([]byte, 10))
	if status != 0 || !bytes.Equal(firstData, []byte("first-data")) {
		t.Errorf("first file after cross-inode handle attempts = %q, %v; want first-data", firstData, status)
	}
	if errno := first.Release(ctx, firstRead); errno != 0 {
		t.Errorf("Release first verification handle: %v", errno)
	}
	secondRead, _, errno := second.Open(ctx, uint32(syscall.O_RDONLY))
	if errno != 0 {
		t.Fatalf("Open second for verification: %v", errno)
	}
	secondResult, errno := second.Read(ctx, secondRead, make([]byte, 11), 0)
	if errno != 0 {
		t.Fatalf("Read second for verification: %v", errno)
	}
	secondData, status := secondResult.Bytes(make([]byte, 11))
	if status != 0 || !bytes.Equal(secondData, []byte("second-data")) {
		t.Errorf("second file after cross-inode handle attempts = %q, %v; want second-data", secondData, status)
	}
	if errno := first.Release(ctx, firstHandle); errno != 0 {
		t.Errorf("Release first: %v", errno)
	}
	if errno := second.Release(ctx, secondHandle); errno != 0 {
		t.Errorf("Release second: %v", errno)
	}
	if errno := second.Release(ctx, secondRead); errno != 0 {
		t.Errorf("Release second verification handle: %v", errno)
	}
}
