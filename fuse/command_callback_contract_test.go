package fuse

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	gofuse "github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tetsup/flexraid/orchestration"
)

func TestOpenAppendCallbackSemantics(t *testing.T) {
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
		before.Attr.Nlink != after.Attr.Nlink || before.Attr.Ino != after.Attr.Ino ||
		before.Attr.Atime != after.Attr.Atime || before.Attr.Atimensec != after.Attr.Atimensec ||
		before.Attr.Mtime != after.Attr.Mtime || before.Attr.Mtimensec != after.Attr.Mtimensec ||
		before.Attr.Ctime != after.Attr.Ctime || before.Attr.Ctimensec != after.Attr.Ctimensec {
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
	if got.Blocks != expected.Blocks || got.Bsize != uint32(expected.Bsize) ||
		got.Frsize != uint32(expected.Frsize) || got.Files != expected.Files || got.Ffree != expected.Ffree {
		t.Errorf("stable FUSE Statfs mapping = %+v, expected orchestration values %+v", got, expected)
	}
	// f_bfree is physical free capacity while f_bavail is logical capacity
	// available for FlexRAID placement. Their relationship is intentionally
	// unconstrained by the filesystem contract, so validate each mapping
	// against its own source rather than comparing the two values.
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

func TestCreateMkdirSymlinkNameCollisionsPreserveExistingEntries(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root

	fileInode, fileHandle, _, errno := root.Create(ctx, "same-file", uint32(syscall.O_RDWR), 0640, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create file: %v", errno)
	}
	file := fileInode.Operations().(*node)
	var beforeFile gofuse.AttrOut
	if errno := file.Getattr(ctx, nil, &beforeFile); errno != 0 {
		t.Fatalf("Getattr original file: %v", errno)
	}
	if _, _, _, errno := root.Create(ctx, "same-file", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{}); errno != syscall.EEXIST {
		t.Errorf("Create duplicate file errno = %v, want EEXIST", errno)
	}
	var afterFile gofuse.AttrOut
	if errno := file.Getattr(ctx, nil, &afterFile); errno != 0 {
		t.Fatalf("Getattr file after duplicate Create: %v", errno)
	}
	if beforeFile.Attr.Ino != afterFile.Attr.Ino || beforeFile.Attr.Mode != afterFile.Attr.Mode ||
		beforeFile.Attr.Uid != afterFile.Attr.Uid || beforeFile.Attr.Gid != afterFile.Attr.Gid {
		t.Errorf("duplicate Create changed original file attributes: before=%+v after=%+v", beforeFile.Attr, afterFile.Attr)
	}
	if errno := file.Release(ctx, fileHandle); errno != 0 {
		t.Errorf("Release file: %v", errno)
	}

	dirInode, errno := root.Mkdir(ctx, "same-directory", 0710, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Mkdir: %v", errno)
	}
	dir := dirInode.Operations().(*node)
	var beforeDir gofuse.AttrOut
	if errno := dir.Getattr(ctx, nil, &beforeDir); errno != 0 {
		t.Fatalf("Getattr original directory: %v", errno)
	}
	if _, errno := root.Mkdir(ctx, "same-directory", 0700, &gofuse.EntryOut{}); errno != syscall.EEXIST {
		t.Errorf("Mkdir duplicate directory errno = %v, want EEXIST", errno)
	}
	var afterDir gofuse.AttrOut
	if errno := dir.Getattr(ctx, nil, &afterDir); errno != 0 {
		t.Fatalf("Getattr directory after duplicate Mkdir: %v", errno)
	}
	if beforeDir.Attr.Ino != afterDir.Attr.Ino || beforeDir.Attr.Mode != afterDir.Attr.Mode {
		t.Errorf("duplicate Mkdir changed original directory: before=%+v after=%+v", beforeDir.Attr, afterDir.Attr)
	}

	linkInode, errno := root.Symlink(ctx, "original-target", "same-symlink", &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Symlink: %v", errno)
	}
	link := linkInode.Operations().(*node)
	if _, errno := root.Symlink(ctx, "replacement-target", "same-symlink", &gofuse.EntryOut{}); errno != syscall.EEXIST {
		t.Errorf("Symlink duplicate name errno = %v, want EEXIST", errno)
	}
	if target, errno := link.Readlink(ctx); errno != 0 || string(target) != "original-target" {
		t.Errorf("existing symlink after duplicate create = %q, %v; want original-target", target, errno)
	}

	if _, errno := root.Link(ctx, linkInode, "hardlink-to-symlink", &gofuse.EntryOut{}); errno == 0 {
		t.Error("Link accepted a symlink source; only regular files may be hard-linked")
	}
	if _, errno := root.Lookup(ctx, "hardlink-to-symlink", &gofuse.EntryOut{}); errno != syscall.ENOENT {
		t.Errorf("Lookup rejected symlink hard-link name errno = %v, want ENOENT", errno)
	}
}

func TestAccessCallbackUsesCallerCredentialsAndRejectsMissingCaller(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	inode, handle, _, errno := adapter.root.Create(ctx, "access", uint32(syscall.O_RDWR), 0640, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create: %v", errno)
	}
	n := inode.Operations().(*node)
	defer func() {
		if errno := n.Release(ctx, handle); errno != 0 {
			t.Errorf("Release: %v", errno)
		}
		if errno := adapter.root.Unlink(ctx, "access"); errno != 0 {
			t.Errorf("Unlink: %v", errno)
		}
	}()

	ownerContext := gofuse.NewContext(ctx, &gofuse.Caller{Owner: gofuse.Owner{Uid: uint32(os.Getuid()), Gid: uint32(os.Getgid())}})
	if errno := n.Access(ownerContext, gofuse.R_OK|gofuse.W_OK); errno != 0 {
		t.Errorf("owner read/write Access = %v, want success", errno)
	}

	otherUID := uint32(os.Getuid()) ^ 1
	if otherUID == uint32(os.Getuid()) {
		otherUID = uint32(os.Getuid()) + 1
	}
	groupContext := gofuse.NewContext(ctx, &gofuse.Caller{Owner: gofuse.Owner{Uid: otherUID, Gid: uint32(os.Getgid())}})
	if errno := n.Access(groupContext, gofuse.R_OK); errno != 0 {
		t.Errorf("group read Access = %v, want success", errno)
	}
	if errno := n.Access(groupContext, gofuse.W_OK); errno != syscall.EACCES {
		t.Errorf("group write Access = %v, want EACCES", errno)
	}

	otherGID := uint32(os.Getgid()) ^ 1
	if otherGID == uint32(os.Getgid()) {
		otherGID = uint32(os.Getgid()) + 1
	}
	otherContext := gofuse.NewContext(ctx, &gofuse.Caller{Owner: gofuse.Owner{Uid: otherUID, Gid: otherGID}})
	if errno := n.Access(otherContext, gofuse.R_OK); errno != syscall.EACCES {
		t.Errorf("other read Access = %v, want EACCES", errno)
	}
	if errno := n.Access(ctx, gofuse.R_OK); errno != syscall.EACCES {
		t.Errorf("Access without caller credentials = %v, want EACCES", errno)
	}
	if errno := n.Access(ownerContext, 0); errno != 0 {
		t.Errorf("Access with empty mask = %v, want success", errno)
	}

	missing := &node{layer: adapter.layer, objectID: testObjectID(t)}
	if errno := missing.Access(ownerContext, gofuse.R_OK); errno == 0 {
		t.Error("Access on missing ObjectID succeeded")
	}
}

func TestRootAccessorAndMountRejectMissingMountpoint(t *testing.T) {
	adapter, _ := newCallbackTestFilesystem(t)
	if got := adapter.Root(); got != adapter.root {
		t.Errorf("Root() = %T %p, want adapter root %p", got, got, adapter.root)
	}
	missingMountpoint := filepath.Join(t.TempDir(), "does-not-exist")
	if server, err := adapter.Mount(missingMountpoint, nil); err == nil {
		if server != nil {
			_ = server.Unmount()
		}
		t.Fatal("Mount on a nonexistent mountpoint succeeded")
	}
}
