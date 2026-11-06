package orchestration

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/tetsup/flexraid/distribution"
)

func TestSetAttrUpdatesMetadataThroughSharedFileHandle(t *testing.T) {
	fixture := newWriteFixture(t)
	handle := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDWR))
	mode, uid := uint32(0600), uint32(1234)
	atime := time.Unix(1700000000, 123)
	if _, err := fixture.layer.SetAttr(SetAttrRequest{
		ObjectID: fixture.linkID, Handle: handle, Mode: &mode, UID: &uid, Atime: &atime,
	}); err != nil {
		t.Fatal(err)
	}
	attrs, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: fixture.fileID})
	if err != nil {
		t.Fatal(err)
	}
	got := attrs.Attributes
	if got.Mode != mode || got.UID != uid || !got.Atime.Equal(atime) {
		t.Fatalf("attributes after SetAttr = %+v", got)
	}
	file := fixture.layer.objects[fixture.fileID].file
	file.mu.RLock()
	entry := file.stripeCache[0]
	file.mu.RUnlock()
	if entry == nil || entry.state != stripeCacheDirty {
		t.Fatal("SetAttr did not dirty Header stripe")
	}
	if _, err := fixture.layer.Flush(FlushRequest{Handle: handle}); err != nil {
		t.Fatal(err)
	}
}

func TestSetAttrWithoutHandlePersistsMetadataImmediately(t *testing.T) {
	fixture := newWriteFixture(t)
	gid := uint32(5678)
	if _, err := fixture.layer.SetAttr(SetAttrRequest{ObjectID: fixture.fileID, GID: &gid}); err != nil {
		t.Fatal(err)
	}
	read, err := fixture.layer.distribution.ReadStripe(distribution.ReadStripeRequest{
		Context: context.Background(), ObjectID: fixture.fileID, StripeID: 0, ChunkSize: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	var persisted header
	if err := persisted.UnmarshalBinary(read.Data[:headerSize]); err != nil {
		t.Fatal(err)
	}
	if persisted.GID != gid {
		t.Fatalf("persisted GID = %d, want %d", persisted.GID, gid)
	}
}

func TestSetAttrSizeUsesTruncateRebuildAndPersistsWithoutHandle(t *testing.T) {
	fixture := newWriteFixture(t)
	size := uint64(100)
	if _, err := fixture.layer.SetAttr(SetAttrRequest{ObjectID: fixture.fileID, Size: &size}); err != nil {
		t.Fatal(err)
	}
	attrs, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: fixture.fileID})
	if err != nil || attrs.Attributes.Size != size {
		t.Fatalf("GetAttr = %+v, %v", attrs, err)
	}
	handle := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDONLY))
	read, err := fixture.layer.Read(ReadRequest{Handle: handle, ObjectID: fixture.fileID, Offset: 90, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Data) != 10 {
		t.Fatalf("read after SetAttr size = %d bytes, want 10", len(read.Data))
	}
}

func TestSetAttrValidation(t *testing.T) {
	fixture := newWriteFixture(t)
	now := time.Now()
	if _, err := fixture.layer.SetAttr(SetAttrRequest{ObjectID: fixture.fileID, Atime: &now, AtimeNow: true}); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("conflicting atime = %v", err)
	}
	size := uint64(10)
	reader := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDONLY))
	if _, err := fixture.layer.SetAttr(SetAttrRequest{ObjectID: fixture.fileID, Handle: reader, Size: &size}); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("truncate through read-only handle = %v", err)
	}
	directoryID := testID("52345678-9abc-4def-8abc-1234567890ab")
	fixture.layer.objects[directoryID] = &runtimeObject{header: testHeader(objectKindDirectory, directoryID, nil, nil, "")}
	if _, err := fixture.layer.SetAttr(SetAttrRequest{ObjectID: directoryID, Size: &size}); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("directory size change = %v", err)
	}
}

func TestSetAttrRejectsCanceledContext(t *testing.T) {
	fixture := newWriteFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	uid := uint32(42)
	if _, err := fixture.layer.SetAttr(SetAttrRequest{Context: ctx, ObjectID: fixture.fileID, UID: &uid}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled SetAttr = %v", err)
	}
}

