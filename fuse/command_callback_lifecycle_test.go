package fuse

import (
	"bytes"
	"context"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	gofuse "github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tetsup/flexraid/distribution"
	physicalio "github.com/tetsup/flexraid/io"
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
	if errno := n.Fsync(ctx, handle, 1); errno != 0 {
		t.Errorf("Fsync datasync open handle: %v", errno)
	}
	if written, errno := n.Write(ctx, handle, []byte("data"), 0); errno != 0 || written != 4 {
		t.Errorf("Write open handle = %d, %v; want 4, success", written, errno)
	}
	if result, errno := n.Read(ctx, handle, make([]byte, 4), 100); errno != 0 {
		t.Errorf("Read beyond EOF errno = %v, want success with empty data", errno)
	} else if data, status := result.Bytes(make([]byte, 4)); status != 0 || len(data) != 0 {
		t.Errorf("Read beyond EOF data/status = %q/%v, want empty/success", data, status)
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

	uid := uint32(os.Getuid()) ^ 1
	if uid == uint32(os.Getuid()) {
		uid = uint32(os.Getuid()) + 1
	}
	gid := uint32(os.Getgid()) ^ 1
	if gid == uint32(os.Getgid()) {
		gid = uint32(os.Getgid()) + 1
	}
	mode := uint32(0640)
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
	nowIn.Valid = gofuse.FATTR_ATIME | gofuse.FATTR_ATIME_NOW | gofuse.FATTR_MTIME | gofuse.FATTR_MTIME_NOW
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
	if errno := link.Setattr(ctx, nil, in, &gofuse.AttrOut{}); errno == 0 {
		t.Error("Setattr symlink mode succeeded; symlink permissions are not mutable")
	}
	sizeIn := &gofuse.SetAttrIn{}
	sizeIn.Valid, sizeIn.Size = gofuse.FATTR_SIZE, 1
	if errno := link.Setattr(ctx, nil, sizeIn, &gofuse.AttrOut{}); errno == 0 {
		t.Error("Setattr symlink size succeeded; symlink size is not mutable")
	}

	dirInode, errno := root.Mkdir(ctx, "directory", 0755, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Mkdir: %v", errno)
	}
	dir := dirInode.Operations().(*node)
	if errno := dir.Setattr(ctx, nil, sizeIn, &gofuse.AttrOut{}); errno == 0 {
		t.Error("Setattr directory size succeeded; directory size changes must be rejected")
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
	if out.Blocks != expected.Blocks || out.Bsize != uint32(expected.Bsize) ||
		out.Frsize != uint32(expected.Frsize) || out.Files != expected.Files || out.Ffree != expected.Ffree {
		t.Errorf("stable FUSE Statfs fields = %+v, Orchestration = %+v", out, expected)
	}
	// Free-space counters are sampled from the live backing filesystems on
	// each call and may change between these two independent observations.
	if out.Bfree == 0 || out.Bavail == 0 || out.Bfree > out.Blocks {
		t.Errorf("FUSE Statfs physical free/logical available capacity is out of range: %+v", out)
	}
	// Bavail represents logical placement capacity and is not bounded by
	// Blocks, which reports the sum of physical capacity across all I/O roots.
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

	writeOnlyHandle, _, errno := first.Open(ctx, uint32(syscall.O_WRONLY))
	if errno != 0 {
		t.Fatalf("Open O_WRONLY: %v", errno)
	}
	if _, errno := first.Read(ctx, writeOnlyHandle, make([]byte, 1), 0); errno == 0 {
		t.Error("Read through O_WRONLY handle succeeded")
	}
	readOnlyHandle, _, errno := first.Open(ctx, uint32(syscall.O_RDONLY))
	if errno != 0 {
		t.Fatalf("Open O_RDONLY: %v", errno)
	}
	if _, errno := first.Write(ctx, readOnlyHandle, []byte("x"), 0); errno == 0 {
		t.Error("Write through O_RDONLY handle succeeded")
	}
	if errno := first.Release(ctx, writeOnlyHandle); errno != 0 {
		t.Errorf("Release O_WRONLY handle: %v", errno)
	}
	if errno := first.Release(ctx, readOnlyHandle); errno != 0 {
		t.Errorf("Release O_RDONLY handle: %v", errno)
	}

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
		t.Errorf("Create with canceled context errno = %v, want a nonzero errno", errno)
	}
	if _, errno := root.Mkdir(cancelled, "cancelled-mkdir", 0755, &gofuse.EntryOut{}); errno == 0 {
		t.Errorf("Mkdir with canceled context errno = %v, want a nonzero errno", errno)
	}
	if _, errno := root.Symlink(cancelled, "target", "cancelled-link", &gofuse.EntryOut{}); errno == 0 {
		t.Errorf("Symlink with canceled context errno = %v, want a nonzero errno", errno)
	}
	if errno := root.Rename(cancelled, "missing", root, "new", 0); errno == 0 {
		t.Errorf("Rename with canceled context errno = %v, want a nonzero errno", errno)
	}
	if errno := root.Unlink(cancelled, "missing"); errno == 0 {
		t.Errorf("Unlink with canceled context errno = %v, want a nonzero errno", errno)
	}
	if errno := root.Rmdir(cancelled, "missing"); errno == 0 {
		t.Errorf("Rmdir with canceled context errno = %v, want a nonzero errno", errno)
	}

	var rootAttr gofuse.AttrOut
	if errno := root.Getattr(cancelled, nil, &rootAttr); errno == 0 {
		t.Errorf("Getattr with canceled context errno = %v, want a nonzero errno", errno)
	}
	if _, errno := root.Readdir(cancelled); errno == 0 {
		t.Errorf("Readdir with canceled context errno = %v, want a nonzero errno", errno)
	}
	if errno := root.Statfs(cancelled, &gofuse.StatfsOut{}); errno == 0 {
		t.Errorf("Statfs with canceled context errno = %v, want a nonzero errno", errno)
	}
	fileInode, fileHandle, _, createErrno := root.Create(ctx, "link-source", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
	if createErrno != 0 {
		t.Fatalf("Create link source: %v", createErrno)
	}
	if _, errno := root.Link(cancelled, fileInode, "cancelled-hardlink", &gofuse.EntryOut{}); errno == 0 {
		t.Errorf("Link with canceled context errno = %v, want a nonzero errno", errno)
	}
	fileNode := fileInode.Operations().(*node)
	if errno := fileNode.Release(ctx, fileHandle); errno != 0 {
		t.Errorf("Release link source: %v", errno)
	}
	if errno := root.Unlink(ctx, "link-source"); errno != 0 {
		t.Errorf("Unlink link source: %v", errno)
	}
	linkInode, linkErrno := root.Symlink(ctx, "target", "cancelled-readlink", &gofuse.EntryOut{})
	if linkErrno != 0 {
		t.Fatalf("create Readlink target: %v", linkErrno)
	}
	if _, errno := linkInode.Operations().(*node).Readlink(cancelled); errno == 0 {
		t.Errorf("Readlink with canceled context errno = %v, want a nonzero errno", errno)
	}
	if errno := root.Unlink(ctx, "cancelled-readlink"); errno != 0 {
		t.Errorf("Unlink symlink: %v", errno)
	}
}

func TestSetattrRejectsReadonlyAndMismatchedHandles(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root
	readonlyInode, readonlyHandle, _, errno := root.Create(ctx, "readonly", uint32(syscall.O_RDONLY), 0600, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create readonly: %v", errno)
	}
	otherInode, otherHandle, _, errno := root.Create(ctx, "other", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create other: %v", errno)
	}
	readonly := readonlyInode.Operations().(*node)
	other := otherInode.Operations().(*node)
	sizeIn := &gofuse.SetAttrIn{}
	sizeIn.Valid, sizeIn.Size = gofuse.FATTR_SIZE, 4
	if errno := readonly.Setattr(ctx, readonlyHandle, sizeIn, &gofuse.AttrOut{}); errno != syscall.EBADF {
		t.Errorf("Setattr size through read-only handle errno = %v, want EBADF", errno)
	}
	if errno := readonly.Setattr(ctx, otherHandle, sizeIn, &gofuse.AttrOut{}); errno == 0 {
		t.Error("Setattr with another file's handle succeeded")
	}
	if errno := readonly.Release(ctx, readonlyHandle); errno != 0 {
		t.Errorf("Release readonly: %v", errno)
	}
	if errno := other.Release(ctx, otherHandle); errno != 0 {
		t.Errorf("Release other: %v", errno)
	}
	if errno := readonly.Setattr(ctx, readonlyHandle, sizeIn, &gofuse.AttrOut{}); errno != syscall.EBADF {
		t.Errorf("Setattr with released handle errno = %v, want EBADF", errno)
	}
	if errno := root.Unlink(ctx, "readonly"); errno != 0 {
		t.Errorf("Unlink readonly: %v", errno)
	}
	if errno := root.Unlink(ctx, "other"); errno != 0 {
		t.Errorf("Unlink other: %v", errno)
	}
}

func TestDataCallbacksHonorCancellationAndStillRelease(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root
	inode, handle, _, errno := root.Create(ctx, "cancel-data", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create: %v", errno)
	}
	n := inode.Operations().(*node)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	if _, _, errno := n.Open(cancelled, uint32(syscall.O_RDONLY)); errno == 0 {
		t.Error("Open with canceled context succeeded")
	}
	if _, errno := n.Read(cancelled, handle, make([]byte, 1), 0); errno == 0 {
		t.Errorf("Read with canceled context errno = %v, want a nonzero errno", errno)
	}
	if _, errno := n.Write(cancelled, handle, []byte("x"), 0); errno == 0 {
		t.Errorf("Write with canceled context errno = %v, want a nonzero errno", errno)
	}
	if errno := n.Flush(cancelled, handle); errno == 0 {
		t.Errorf("Flush with canceled context errno = %v, want a nonzero errno", errno)
	}
	if errno := n.Fsync(cancelled, handle, 0); errno == 0 {
		t.Errorf("Fsync with canceled context errno = %v, want a nonzero errno", errno)
	}
	setattr := &gofuse.SetAttrIn{}
	setattr.Valid, setattr.Mode = gofuse.FATTR_MODE, 0640
	if errno := n.Setattr(cancelled, nil, setattr, &gofuse.AttrOut{}); errno == 0 {
		t.Errorf("Setattr with canceled context errno = %v, want a nonzero errno", errno)
	}
	// Release must clean up an open handle even after request cancellation.
	if errno := n.Release(cancelled, handle); errno != 0 {
		t.Errorf("Release with canceled context = %v, want success", errno)
	}
	if errno := root.Unlink(ctx, "cancel-data"); errno != 0 {
		t.Errorf("Unlink after canceled operations: %v", errno)
	}
}

func TestOpenSyncModesPersistWrites(t *testing.T) {
	adapter, ctx, roots := newCallbackTestFilesystemWithIORoots(t)
	root := adapter.root
	inode, createdHandle, _, errno := root.Create(ctx, "sync-modes", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create: %v", errno)
	}
	n := inode.Operations().(*node)

	for index, flags := range []uint32{uint32(syscall.O_WRONLY | syscall.O_SYNC), uint32(syscall.O_WRONLY | syscall.O_DSYNC)} {
		handle, _, openErrno := n.Open(ctx, flags)
		if openErrno != 0 {
			t.Fatalf("Open synchronous mode %#x: %v", flags, openErrno)
		}
		payload := []byte{byte('A' + index)}
		written, writeErrno := n.Write(ctx, handle, payload, int64(index))
		if writeErrno != 0 || written != 1 {
			t.Errorf("Write with synchronous mode %#x = %d, %v; want 1 byte, success", flags, written, writeErrno)
		}
		// Do not Flush here: O_SYNC/O_DSYNC require persistence before
		// Write returns, not only when the handle is later closed.
		if releaseErrno := n.Release(ctx, handle); releaseErrno != 0 {
			t.Errorf("Release synchronous mode %#x: %v", flags, releaseErrno)
		}
	}
	if errno := n.Release(ctx, createdHandle); errno != 0 {
		t.Errorf("Release Create handle: %v", errno)
	}

	// Reinitialize from physical storage so the check cannot pass merely
	// because the original Orchestration instance still has dirty cache.
	ioLayers := make([]*physicalio.IO, len(roots))
	for index, path := range roots {
		ioLayers[index] = physicalio.New(physicalio.NewRequest{Root: path}).Layer
	}
	distributed, err := distribution.New(distribution.NewRequest{IOs: ioLayers})
	if err != nil {
		t.Fatalf("reinitialize Distribution: %v", err)
	}
	reopened, err := orchestration.New(orchestration.NewRequest{Context: ctx, Distribution: distributed.Layer})
	if err != nil {
		t.Fatalf("reinitialize Orchestration: %v", err)
	}
	opened, err := reopened.Layer.Open(orchestration.OpenRequest{Context: ctx, ObjectID: n.objectID, Flags: uint32(syscall.O_RDONLY)})
	if err != nil {
		t.Fatalf("Open persisted synchronous writes: %v", err)
	}
	result, err := reopened.Layer.Read(orchestration.ReadRequest{Context: ctx, Handle: opened.Handle, ObjectID: n.objectID, Offset: 0, Size: 2})
	if err != nil {
		t.Fatalf("Read persisted synchronous writes: %v", err)
	}
	if !bytes.Equal(result.Data, []byte("AB")) {
		t.Errorf("persisted content after O_SYNC/O_DSYNC writes = %q; want AB", result.Data)
	}
	if _, err := reopened.Layer.Release(orchestration.ReleaseRequest{Context: ctx, Handle: opened.Handle}); err != nil {
		t.Errorf("Release reinitialized handle: %v", err)
	}
}

func TestConcurrentHandlesShareObjectStripeState(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	root := adapter.root
	inode, firstHandle, _, errno := root.Create(ctx, "concurrent", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create: %v", errno)
	}
	n := inode.Operations().(*node)
	secondHandle, _, errno := n.Open(ctx, uint32(syscall.O_RDWR))
	if errno != 0 {
		t.Fatalf("Open second handle: %v", errno)
	}

	firstData := bytes.Repeat([]byte("A"), 16384)
	secondData := bytes.Repeat([]byte("B"), 16384)
	type writeResult struct {
		name string
		n    uint32
		err  syscall.Errno
	}
	results := make(chan writeResult, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		written, writeErrno := n.Write(ctx, firstHandle, firstData, 0)
		results <- writeResult{name: "first", n: written, err: writeErrno}
	}()
	go func() {
		defer wg.Done()
		written, writeErrno := n.Write(ctx, secondHandle, secondData, 16384)
		results <- writeResult{name: "second", n: written, err: writeErrno}
	}()
	wg.Wait()
	close(results)
	for result := range results {
		if result.err != 0 || result.n != 16384 {
			t.Errorf("%s concurrent Write = %d, %v; want 16384, success", result.name, result.n, result.err)
		}
	}

	if errno := n.Flush(ctx, firstHandle); errno != 0 {
		t.Fatalf("Flush first handle: %v", errno)
	}
	if errno := n.Fsync(ctx, secondHandle, 0); errno != 0 {
		t.Fatalf("Fsync second handle: %v", errno)
	}
	readResult, errno := n.Read(ctx, secondHandle, make([]byte, 32768), 0)
	if errno != 0 {
		t.Fatalf("Read through second handle: %v", errno)
	}
	got, status := readResult.Bytes(make([]byte, 32768))
	if status != 0 {
		t.Fatalf("ReadResult.Bytes: %v", status)
	}
	if !bytes.Equal(got[:16384], firstData) || !bytes.Equal(got[16384:], secondData) {
		t.Error("concurrent writes lost or mixed data across shared handles")
	}

	if errno := n.Release(ctx, firstHandle); errno != 0 {
		t.Errorf("Release first handle: %v", errno)
	}
	if errno := n.Release(ctx, secondHandle); errno != 0 {
		t.Errorf("Release second handle: %v", errno)
	}
	if errno := root.Unlink(ctx, "concurrent"); errno != 0 {
		t.Errorf("Unlink concurrent file: %v", errno)
	}
}
