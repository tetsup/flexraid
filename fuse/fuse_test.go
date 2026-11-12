package fuse

import (
	"context"
	"syscall"
	"testing"
	"time"

	gofs "github.com/hanwen/go-fuse/v2/fs"
	gofuse "github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tetsup/flexraid/orchestration"
	"github.com/tetsup/flexraid/pkg/shared"
)

func testObjectID(t *testing.T) shared.ObjectID {
	t.Helper()
	id, err := shared.ParseObjectID("9b2b0d5a-4c7d-4f5b-8f44-4a81a2c4b6d1")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestInodeNumberIsStableAndNonzero(t *testing.T) {
	id := testObjectID(t)
	if first, second := inodeNumber(id), inodeNumber(id); first == 0 || first != second {
		t.Fatalf("inodeNumber is not stable and nonzero: first=%d second=%d", first, second)
	}
}

func TestInodeNumberUsesBigEndianPrefix(t *testing.T) {
	id := testObjectID(t)
	want := uint64(id[0])<<56 | uint64(id[1])<<48 | uint64(id[2])<<40 | uint64(id[3])<<32 |
		uint64(id[4])<<24 | uint64(id[5])<<16 | uint64(id[6])<<8 | uint64(id[7])
	if got := inodeNumber(id); got != want {
		t.Fatalf("inodeNumber=%#x, want %#x", got, want)
	}
}

func TestStableAttrUsesTypeAndObjectID(t *testing.T) {
	id := testObjectID(t)
	got := stableAttr(orchestration.ObjectAttributes{ObjectID: id, Kind: orchestration.ObjectKindDirectory, Mode: 0750})
	if got.Mode != syscall.S_IFDIR || got.Ino != inodeNumber(id) {
		t.Fatalf("stableAttr = %+v, want type=%#o ino=%d", got, syscall.S_IFDIR, inodeNumber(id))
	}
}

func TestFillAttrMapsOrchestrationAttributes(t *testing.T) {
	id := testObjectID(t)
	atime, mtime, ctime := time.Unix(123, 456), time.Unix(234, 567), time.Unix(345, 678)
	attributes := orchestration.ObjectAttributes{ObjectID: id, Kind: orchestration.ObjectKindFile, Mode: 0640, UID: 17, GID: 23, Nlink: 2, Size: 1537, Atime: atime, Mtime: mtime, Ctime: ctime}
	var out gofuse.Attr
	fillAttr(&out, attributes)
	if out.Ino != inodeNumber(id) || out.Mode != (syscall.S_IFREG|attributes.Mode) || out.Nlink != 2 || out.Size != 1537 {
		t.Fatalf("attribute identity/size mapping incorrect: %+v", out)
	}
	if out.Uid != 17 || out.Gid != 23 {
		t.Fatalf("owner mapping incorrect: uid=%d gid=%d", out.Uid, out.Gid)
	}
	if out.Atime != 123 || out.Atimensec != 456 || out.Mtime != 234 || out.Mtimensec != 567 || out.Ctime != 345 || out.Ctimensec != 678 {
		t.Fatalf("timestamp mapping incorrect: %+v", out)
	}
	if out.Blksize != 4096 || out.Blocks != 4 {
		t.Fatalf("block mapping incorrect: blksize=%d blocks=%d", out.Blksize, out.Blocks)
	}
}

func TestObjectModeMapsKindAndPermissions(t *testing.T) {
	tests := []struct {
		kind orchestration.ObjectAttributes
		want uint32
	}{
		{orchestration.ObjectAttributes{Kind: orchestration.ObjectKindDirectory, Mode: 0751}, syscall.S_IFDIR | 0751},
		{orchestration.ObjectAttributes{Kind: orchestration.ObjectKindFile, Mode: 0640}, syscall.S_IFREG | 0640},
		{orchestration.ObjectAttributes{Kind: orchestration.ObjectKindSymlink, Mode: 0777}, syscall.S_IFLNK | 0777},
	}
	for _, tc := range tests {
		if got := objectMode(tc.kind); got != tc.want {
			t.Errorf("objectMode(%+v) = %#o, want %#o", tc.kind, got, tc.want)
		}
	}
}

func TestFillAttrBlockCountRoundsUp(t *testing.T) {
	for _, tc := range []struct{ size, blocks uint64 }{{0, 0}, {1, 1}, {512, 1}, {513, 2}} {
		var out gofuse.Attr
		fillAttr(&out, orchestration.ObjectAttributes{Size: tc.size})
		if out.Blocks != tc.blocks {
			t.Errorf("size %d: Blocks=%d, want %d", tc.size, out.Blocks, tc.blocks)
		}
	}
}

type foreignHandle struct{}

func TestReadWriteRejectInvalidHandleAndOffset(t *testing.T) {
	n := &node{}
	for _, fh := range []gofs.FileHandle{nil, &foreignHandle{}} {
		if _, errno := n.Read(nil, fh, make([]byte, 1), 0); errno != syscall.EBADF {
			t.Errorf("Read invalid handle errno=%v, want EBADF", errno)
		}
		if _, errno := n.Write(nil, fh, []byte("x"), 0); errno != syscall.EBADF {
			t.Errorf("Write invalid handle errno=%v, want EBADF", errno)
		}
	}

	adapter, ctx := newCallbackTestFilesystem(t)
	inode, handle, _, errno := adapter.root.Create(ctx, "negative-offset", uint32(syscall.O_RDWR), 0600, &gofuse.EntryOut{})
	if errno != 0 {
		t.Fatalf("Create for negative-offset checks: %v", errno)
	}
	file := inode.Operations().(*node)
	if _, errno := file.Read(ctx, handle, make([]byte, 1), -1); errno == 0 {
		t.Error("Read with negative offset succeeded")
	}
	if _, errno := file.Write(ctx, handle, []byte("x"), -1); errno == 0 {
		t.Error("Write with negative offset succeeded")
	}
	if errno := file.Release(ctx, handle); errno != 0 {
		t.Errorf("Release negative-offset test handle: %v", errno)
	}
}

func TestHandleOperationsRejectInvalidHandle(t *testing.T) {
	n := &node{}
	if errno := n.Flush(nil, nil); errno != syscall.EBADF {
		t.Errorf("Flush errno=%v, want EBADF", errno)
	}
	if errno := n.Fsync(nil, nil, 0); errno != syscall.EBADF {
		t.Errorf("Fsync errno=%v, want EBADF", errno)
	}
	if errno := n.Release(nil, nil); errno != syscall.EBADF {
		t.Errorf("Release errno=%v, want EBADF", errno)
	}
	fh := &foreignHandle{}
	if errno := n.Flush(nil, fh); errno != syscall.EBADF {
		t.Errorf("Flush foreign handle errno=%v, want EBADF", errno)
	}
	if errno := n.Fsync(nil, fh, 0); errno != syscall.EBADF {
		t.Errorf("Fsync foreign handle errno=%v, want EBADF", errno)
	}
	if errno := n.Release(nil, fh); errno != syscall.EBADF {
		t.Errorf("Release foreign handle errno=%v, want EBADF", errno)
	}
}

func TestNewRejectsNilOrchestration(t *testing.T) {
	if _, err := New(nil, testObjectID(t)); err == nil {
		t.Fatal("New with nil Orchestration succeeded")
	}
}

func TestRenameRejectsUnsupportedFlagsBeforeBackendCall(t *testing.T) {
	if errno := (&node{}).Rename(nil, "old", nil, "new", 1); errno != syscall.EINVAL {
		t.Fatalf("Rename flags errno=%v, want EINVAL", errno)
	}
}

func TestParseObjectID(t *testing.T) {
	if _, err := ParseObjectID("not-a-uuid"); err == nil {
		t.Fatal("invalid UUID accepted")
	}
	got, err := ParseObjectID("9b2b0d5a-4c7d-4f5b-8f44-4a81a2c4b6d1")
	if err != nil || got != testObjectID(t) {
		t.Fatalf("valid ParseObjectID = %v, %v", got, err)
	}
}

func TestFillEntryMapsAllNamespaceAttributes(t *testing.T) {
	id := testObjectID(t)
	attributes := orchestration.ObjectAttributes{
		ObjectID: id, Kind: orchestration.ObjectKindDirectory, Mode: 0751, UID: 41, GID: 42,
		Nlink: 3, Size: 1025,
		Atime: time.Unix(10, 11), Mtime: time.Unix(20, 22), Ctime: time.Unix(30, 33),
	}
	var out gofuse.EntryOut
	fillEntry(&out, attributes)
	if out.Ino != inodeNumber(id) || out.Mode != (syscall.S_IFDIR|attributes.Mode) || out.Nlink != 3 || out.Size != 1025 {
		t.Fatalf("entry identity/metadata mismatch: %+v", out.Attr)
	}
	if out.Uid != 41 || out.Gid != 42 || out.Atime != 10 || out.Atimensec != 11 ||
		out.Mtime != 20 || out.Mtimensec != 22 || out.Ctime != 30 || out.Ctimensec != 33 {
		t.Fatalf("entry ownership/timestamps mismatch: %+v", out.Attr)
	}
}

func TestMountOptionsEnableNullPermissions(t *testing.T) {
	options := MountOptions()
	if options == nil || !options.NullPermissions {
		t.Fatalf("MountOptions()=%+v, want NullPermissions enabled", options)
	}
}

func TestStatfsMapsMaximumNameLength(t *testing.T) {
	adapter, ctx := newCallbackTestFilesystem(t)
	var out gofuse.StatfsOut
	if errno := adapter.root.Statfs(ctx, &out); errno != 0 {
		t.Fatalf("Statfs errno=%v", errno)
	}
	if out.NameLen != 255 {
		t.Fatalf("Statfs NameLen=%d, want 255", out.NameLen)
	}
}

func TestMountOptionsDelegatePermissionChecksToKernel(t *testing.T) {
	options := MountOptions()
	if options == nil {
		t.Fatal("MountOptions() returned nil")
	}
	found := false
	for _, option := range options.MountOptions.Options {
		if option == "default_permissions" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("MountOptions().Options=%v, want default_permissions", options.MountOptions.Options)
	}
	if !options.NullPermissions {
		t.Fatal("NullPermissions must remain enabled to preserve mode 0000")
	}
}

func TestNodeDoesNotImplementCustomAccessCheck(t *testing.T) {
	var candidate any = (*node)(nil)
	if _, implements := candidate.(interface {
		Access(context.Context, uint32) syscall.Errno
	}); implements {
		t.Fatal("node implements custom Access; permission checks must be delegated to the kernel")
	}
}
