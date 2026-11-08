package fuse

import (
	"os"
	"syscall"
	"testing"
	"time"

	gofuse "github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tetsup/flexraid/orchestration"
)

func TestFileHandleCallbackLifecycleAndReleasedHandle(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root
	inode, handle, _, errno := root.Create(ctx, "lifecycle", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create: %v", errno)
	}
	n := inode.Operations().(*node)

	if errno := n.Flush(ctx, handle); errno != 0 {
		t.Errorf("Flush open handle: %v", errno)
	}
	if errno := n.Fsync(ctx, handle, 0); errno != 0 {
		t.Errorf("Fsync open handle: %v", errno)
	}
	if written, errno := n.Write(ctx, handle, []byte("data"), 0); errno != 0 || written != 4 {
		t.Errorf("Write open handle = %d, %v; want 4, success", written, errno)
	}
	if errno := n.Release(ctx, handle); errno != 0 {
		t.Fatalf("Release: %v", errno)
	}
	if errno := n.Release(ctx, handle); errno != 0 {
		t.Errorf("second Release should be idempotent: %v", errno)
	}

	if _, errno := n.Read(ctx, handle, make([]byte, 4), 0); errno == 0 {
		t.Error("Read after Release succeeded, want an error")
	}
	if _, errno := n.Write(ctx, handle, []byte("x"), 0); errno == 0 {
		t.Error("Write after Release succeeded, want an error")
	}
	if errno := n.Flush(ctx, handle); errno == 0 {
		t.Error("Flush after Release succeeded, want an error")
	}
	if errno := n.Fsync(ctx, handle, 0); errno == 0 {
		t.Error("Fsync after Release succeeded, want an error")
	}
	if errno := root.Unlink(ctx, "lifecycle"); errno != 0 {
		t.Errorf("Unlink after Release: %v", errno)
	}
}

func TestSetattrCallbackMapsAllSupportedFields(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root
	inode, handle, _, errno := root.Create(ctx, "metadata", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create: %v", errno)
	}
	n := inode.Operations().(*node)

	mode, uid, gid := uint32(0640), uint32(os.Getuid()+1), uint32(os.Getgid()+1)
	atime := time.Unix(1_710_000_000, 123_000_000)
	mtime := time.Unix(1_710_000_100, 456_000_000)
	in := &gofuse.SetAttrIn{}
	in.Valid = gofuse.FATTR_MODE | gofuse.FATTR_UID | gofuse.FATTR_GID | gofuse.FATTR_ATIME | gofuse.FATTR_MTIME
	in.Mode, in.Uid, in.Gid = mode, uid, gid
	in.Atime, in.Atimensec = uint64(atime.Unix()), uint32(atime.Nanosecond())
	in.Mtime, in.Mtimensec = uint64(mtime.Unix()), uint32(mtime.Nanosecond())
	var out gofuse.AttrOut
	if errno := n.Setattr(ctx, handle, in, &out); errno != 0 {
		t.Fatalf("Setattr multiple fields: %v", errno)
	}
	if out.Attr.Mode&07777 != mode || out.Attr.Uid != uid || out.Attr.Gid != gid {
		t.Errorf("Setattr mode/owner = %#o %d:%d, want %#o %d:%d", out.Attr.Mode&07777, out.Attr.Uid, out.Attr.Gid, mode, uid, gid)
	}
	if out.Attr.Atime != uint64(atime.Unix()) || out.Attr.Atimensec != uint32(atime.Nanosecond()) ||
		out.Attr.Mtime != uint64(mtime.Unix()) || out.Attr.Mtimensec != uint32(mtime.Nanosecond()) {
		t.Errorf("Setattr timestamps = atime %d.%09d mtime %d.%09d", out.Attr.Atime, out.Attr.Atimensec, out.Attr.Mtime, out.Attr.Mtimensec)
	}

	nowIn := &gofuse.SetAttrIn{}
	nowIn.Valid = gofuse.FATTR_ATIME_NOW | gofuse.FATTR_MTIME_NOW
	before := time.Now()
	if errno := n.Setattr(ctx, nil, nowIn, &out); errno != 0 {
		t.Fatalf("Setattr ATIME_NOW/MTIME_NOW: %v", errno)
	}
	if time.Unix(int64(out.Attr.Atime), int64(out.Attr.Atimensec)).Before(before) ||
		time.Unix(int64(out.Attr.Mtime), int64(out.Attr.Mtimensec)).Before(before) {
		t.Errorf("Setattr *_NOW timestamps precede request: atime=%d.%09d mtime=%d.%09d", out.Attr.Atime, out.Attr.Atimensec, out.Attr.Mtime, out.Attr.Mtimensec)
	}
	if errno := n.Release(ctx, handle); errno != 0 {
		t.Errorf("Release: %v", errno)
	}
}

func TestSetattrCallbackRejectsInvalidTargetsAndSymlinkMode(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root
	missing := &node{layer: adapter.layer, objectID: testObjectID(t)}
	mode := uint32(0600)
	in := &gofuse.SetAttrIn{}
	in.Valid, in.Mode = gofuse.FATTR_MODE, mode
	if errno := missing.Setattr(ctx, nil, in, &gofuse.AttrOut{}); errno != syscall.ENOENT {
		t.Errorf("Setattr missing ObjectID errno = %v, want ENOENT", errno)
	}

	linkInode, errno := root.Symlink(ctx, "target", "link", &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Symlink: %v", errno)
	}
	link := linkInode.Operations().(*node)
	if errno := link.Setattr(ctx, nil, in, &gofuse.AttrOut{}); errno != syscall.EPERM {
		t.Errorf("Setattr symlink mode errno = %v, want EPERM", errno)
	}
	sizeIn := &gofuse.SetAttrIn{}
	sizeIn.Valid, sizeIn.Size = gofuse.FATTR_SIZE, 1
	if errno := link.Setattr(ctx, nil, sizeIn, &gofuse.AttrOut{}); errno != syscall.EINVAL {
		t.Errorf("Setattr symlink size errno = %v, want EINVAL", errno)
	}

	dirInode, errno := root.Mkdir(ctx, "directory", 0755, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Mkdir: %v", errno)
	}
	dir := dirInode.Operations().(*node)
	if errno := dir.Setattr(ctx, nil, sizeIn, &gofuse.AttrOut{}); errno != syscall.EINVAL {
		t.Errorf("Setattr directory size errno = %v, want EINVAL", errno)
	}
}

func TestStatfsCallbackMatchesOrchestrationResponse(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	expected, err := adapter.layer.StatFs(orchestration.StatFsRequest{Context: ctx})
	if err != nil {
		t.Fatalf("Orchestration StatFs: %v", err)
	}
	var out gofuse.StatfsOut
	if errno := adapter.root.Statfs(ctx, &out); errno != 0 {
		t.Fatalf("FUSE Statfs: %v", errno)
	}
	if out.Blocks != expected.Blocks || out.Bfree != expected.Bfree || out.Bavail != expected.Bavail ||
		out.Bsize != uint32(expected.Bsize) || out.Frsize != uint32(expected.Frsize) ||
		out.Files != expected.Files || out.Ffree != expected.Ffree {
		t.Errorf("FUSE Statfs = %+v, Orchestration = %+v", out, expected)
	}
}


func TestOpenReadWriteCallbackRejectInvalidModesAndForeignHandles(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root
	firstInode, firstHandle, _, errno := root.Create(ctx, "first", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create first: %v", errno)
	}
	first := firstInode.Operations().(*node)
	secondInode, secondHandle, _, errno := root.Create(ctx, "second", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create second: %v", errno)
	}
	second := secondInode.Operations().(*node)

	if _, _, errno := first.Open(ctx, uint32(syscall.O_ACCMODE)); errno != syscall.EINVAL {
		t.Errorf("Open invalid access mode errno = %v, want EINVAL", errno)
	}
	if _, errno := second.Read(ctx, firstHandle, make([]byte, 1), 0); errno == 0 {
		t.Error("Read with another inode's handle succeeded")
	}
	if _, errno := second.Write(ctx, firstHandle, []byte("x"), 0); errno == 0 {
		t.Error("Write with another inode's handle succeeded")
	}
	if errno := first.Release(ctx, firstHandle); errno != 0 {
		t.Errorf("Release first: %v", errno)
	}
	if errno := second.Release(ctx, secondHandle); errno != 0 {
		t.Errorf("Release second: %v", errno)
	}
	if errno := root.Unlink(ctx, "first"); errno != 0 {
		t.Errorf("Unlink first: %v", errno)
	}
	if errno := root.Unlink(ctx, "second"); errno != 0 {
		t.Errorf("Unlink second: %v", errno)
	}
}

func TestNamespaceCallbacksHonorCanceledContext(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	if _, errno := root.Lookup(cancelled, "missing", &gofuse.EntryOut{}); errno == 0 {
		t.Error("Lookup with canceled context succeeded")
	}
	if _, _, _, errno := root.Create(cancelled, "cancelled-create", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{}); errno == 0 {
		t.Error("Create with canceled context succeeded")
	}
	if _, errno := root.Mkdir(cancelled, "cancelled-mkdir", 0755, &gofuse.EntryOut{}); errno == 0 {
		t.Error("Mkdir with canceled context succeeded")
	}
	if _, errno := root.Symlink(cancelled, "target", "cancelled-link", &gofuse.EntryOut{}); errno == 0 {
		t.Error("Symlink with canceled context succeeded")
	}
	if errno := root.Rename(cancelled, "missing", root, "new", 0); errno == 0 {
		t.Error("Rename with canceled context succeeded")
	}
	if errno := root.Unlink(cancelled, "missing"); errno == 0 {
		t.Error("Unlink with canceled context succeeded")
	}
	if errno := root.Rmdir(cancelled, "missing"); errno == 0 {
		t.Error("Rmdir with canceled context succeeded")
	}
}
