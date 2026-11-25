package distribution

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

func testObjectID() shared.ObjectID {
	return shared.ObjectID{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0x4d, 0xef, 0x8a, 0xbc, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab}
}
func makeIOs(t *testing.T, count int) []*physicalio.IO {
	t.Helper()
	result := make([]*physicalio.IO, count)
	for i := range result {
		result[i] = physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer
	}
	return result
}

func TestWriteReadMirrorAndRestart(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("mirror stripe")
	if _, err := created.Layer.WriteStripe(WriteStripeRequest{Context: ctx, ObjectID: testObjectID(), StripeID: 0, ChunkSize: 16, Layout: Layout{Type: LayoutMirror, NumChunks: 2}, IOIndexes: []int{0, 1}, Data: data}); err != nil {
		t.Fatal(err)
	}
	read, err := created.Layer.ReadStripe(ReadStripeRequest{Context: ctx, ObjectID: testObjectID(), StripeID: 0, ChunkSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read.Data, data) {
		t.Fatalf("got %q", read.Data)
	}
	restarted, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	read, err = restarted.Layer.ReadStripe(ReadStripeRequest{Context: ctx, ObjectID: testObjectID(), StripeID: 0, ChunkSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read.Data, data) {
		t.Fatalf("restart got %q", read.Data)
	}
}

func TestWriteReadParityAndRecoverMissingChunk(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 5)
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("parity data!")
	written, err := created.Layer.WriteStripe(WriteStripeRequest{Context: ctx, ObjectID: testObjectID(), StripeID: 4, ChunkSize: 4, Layout: Layout{Type: LayoutParity, NumChunks: 3}, IOIndexes: []int{0, 1, 2, 3}, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	missingKey := storage.FileKey{ObjectID: testObjectID(), StripeVersion: written.Version, LayoutID: storage.LayoutID{Type: storage.LayoutType(LayoutParity), DriveCount: 4, Role: storage.ChunkRoleData, Index: 2}, StripeID: 4}
	if _, err := ios[1].Delete(physicalio.DeleteRequest{Context: ctx, Key: missingKey}); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	read, err := restarted.Layer.ReadStripe(ReadStripeRequest{Context: ctx, ObjectID: testObjectID(), StripeID: 4, ChunkSize: 4, RepairIOIndexes: []int{4, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read.Data, data) {
		t.Fatalf("got %q", read.Data)
	}
	repaired, err := ios[4].Read(physicalio.ReadRequest{Context: ctx, Key: missingKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodeChunk(repaired.Data); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteObjectRemovesHeaderLastAndAllVersions(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	id := testObjectID()
	permissionStore := shared.NewCleanupPermissionStore([]shared.DriveCleanupPermission{{DeleteLogicalObjectsAllowed: true}, {}})
	if err := created.Layer.SetCleanupPermissionStore(permissionStore); err != nil {
		t.Fatal(err)
	}
	for _, stripe := range []storage.StripeID{0, 1} {
		for i := 0; i < 2; i++ {
			_, err = created.Layer.WriteStripe(WriteStripeRequest{Context: ctx, ObjectID: id, StripeID: stripe, ChunkSize: 4, Layout: Layout{Type: LayoutMirror, NumChunks: 2}, IOIndexes: []int{0, 1}, Data: []byte{byte(stripe), byte(i)}})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := created.Layer.DeleteObject(DeleteObjectRequest{Context: ctx, ObjectID: id}); !errors.Is(err, ErrLogicalObjectDeletionNotAllowed) {
		t.Fatalf("DeleteObject error = %v, want drive permission rejection", err)
	}
	objectsBefore, err := created.Layer.ListObjects(ListObjectsRequest{Context: ctx})
	if err != nil || len(objectsBefore.ObjectIDs) != 1 {
		t.Fatalf("Object changed after denied cleanup: %+v, %v", objectsBefore, err)
	}
	if err := permissionStore.Replace([]shared.DriveCleanupPermission{{DeleteLogicalObjectsAllowed: true}, {DeleteLogicalObjectsAllowed: true}}); err != nil {
		t.Fatal(err)
	}
	result, err := created.Layer.DeleteObject(DeleteObjectRequest{Context: ctx, ObjectID: id})
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedFiles != 8 {
		t.Fatalf("deleted %d files", result.DeletedFiles)
	}
	objects, err := created.Layer.ListObjects(ListObjectsRequest{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects.ObjectIDs) != 0 {
		t.Fatalf("objects remain: %v", objects.ObjectIDs)
	}
}

func TestSyncObjectSynchronizesLatestChunksAcrossStripes(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	objectID := testObjectID()
	for stripeID, data := range [][]byte{[]byte("stripe zero"), []byte("stripe one")} {
		if _, err := created.Layer.WriteStripe(WriteStripeRequest{
			Context: ctx, ObjectID: objectID, StripeID: storage.StripeID(stripeID),
			ChunkSize: 32, Layout: Layout{Type: LayoutMirror, NumChunks: 2},
			IOIndexes: []int{0, 1}, Data: data,
		}); err != nil {
			t.Fatalf("WriteStripe %d: %v", stripeID, err)
		}
	}
	if result, err := created.Layer.SyncObject(SyncObjectRequest{Context: ctx, ObjectID: objectID}); err != nil || result.SyncedChunks != 4 {
		t.Fatalf("SyncObject = %+v, %v; want four chunks", result, err)
	}
	restarted, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	for stripeID, want := range [][]byte{[]byte("stripe zero"), []byte("stripe one")} {
		read, err := restarted.Layer.ReadStripe(ReadStripeRequest{
			Context: ctx, ObjectID: objectID, StripeID: storage.StripeID(stripeID), ChunkSize: 32,
		})
		if err != nil || !bytes.Equal(read.Data, want) {
			t.Errorf("ReadStripe %d after SyncObject = %q, %v; want %q", stripeID, read.Data, err, want)
		}
	}
}

func TestConcurrentStripeWritesSerializeVersions(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	const writes = 12
	var wg sync.WaitGroup
	errs := make(chan error, writes)
	for i := 0; i < writes; i++ {
		wg.Add(1)
		go func(value byte) {
			defer wg.Done()
			_, writeErr := created.Layer.WriteStripe(WriteStripeRequest{
				Context: ctx, ObjectID: testObjectID(), StripeID: 9, ChunkSize: 32,
				Layout: Layout{Type: LayoutMirror, NumChunks: 2}, IOIndexes: []int{0, 1},
				Data: []byte{value},
			})
			errs <- writeErr
		}(byte(i))
	}
	wg.Wait()
	close(errs)
	for writeErr := range errs {
		if writeErr != nil {
			t.Errorf("concurrent WriteStripe: %v", writeErr)
		}
	}
	info, err := created.Layer.GetStripeInfo(GetStripeInfoRequest{ObjectID: testObjectID(), StripeID: 9})
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != writes {
		t.Fatalf("latest version = %d, want %d", info.Version, writes)
	}
	candidates := created.Layer.cleanupCandidates()
	if len(candidates) != 2 {
		t.Fatalf("write cleanup candidates = %+v, want one old-version candidate per drive", candidates)
	}
	for _, candidate := range candidates {
		if candidate.method != cleanupDeleteOldVersion {
			t.Fatalf("write candidate method = %v, want old version", candidate.method)
		}
	}
	restarted, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.Layer.cleanupCandidates(); len(got) != 2 {
		t.Fatalf("initial scan cleanup candidates = %+v, want one per drive", got)
	}
	read, err := created.Layer.ReadStripe(ReadStripeRequest{Context: ctx, ObjectID: testObjectID(), StripeID: 9, ChunkSize: 32})
	if err != nil {
		t.Fatal(err)
	}
	if read.Version != writes {
		t.Fatalf("read version = %d, want latest reserved version %d", read.Version, writes)
	}
	if len(read.Data) != 1 {
		t.Fatalf("read data = %v, want one byte", read.Data)
	}
}

func TestCleanupStripeDriveSkipsChunkPinnedByRead(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	request := WriteStripeRequest{
		Context: ctx, ObjectID: testObjectID(), StripeID: 21, ChunkSize: 32,
		Layout: Layout{Type: LayoutMirror, NumChunks: 2}, IOIndexes: []int{0, 1},
		Data: []byte("old version"),
	}
	old, err := created.Layer.WriteStripe(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Data = []byte("new version")
	if _, err := created.Layer.WriteStripe(request); err != nil {
		t.Fatal(err)
	}

	key := stripeIDKey{objectID: testObjectID(), stripeID: 21}
	created.Layer.mapMu.RLock()
	location := created.Layer.versions[key][old.Version].chunks[0]
	created.Layer.mapMu.RUnlock()
	if location.readers == nil {
		t.Fatal("chunk has no active-read counter")
	}
	location.readers.Add(1)
	defer location.readers.Add(-1)

	candidate := cleanupCandidateKey{
		ioIndex: location.ioIndex, objectID: testObjectID(), stripeID: 21,
		method: cleanupDeleteOldVersion,
	}
	result, err := created.Layer.CleanupStripeDrive(CleanupStripeDriveRequest{
		Context: ctx, IOIndex: candidate.ioIndex, ObjectID: candidate.objectID,
		StripeID: candidate.stripeID, Method: CleanupMethodDeleteOldVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FilesProcessed != 0 {
		t.Fatalf("cleanup processed %d files while a read was active", result.FilesProcessed)
	}
	if _, err := ios[location.ioIndex].Read(physicalio.ReadRequest{Context: ctx, Key: location.key}); err != nil {
		t.Fatalf("pinned file was deleted: %v", err)
	}

	location.readers.Add(-1)
	result, err = created.Layer.CleanupStripeDrive(CleanupStripeDriveRequest{
		Context: ctx, IOIndex: candidate.ioIndex, ObjectID: candidate.objectID,
		StripeID: candidate.stripeID, Method: CleanupMethodDeleteOldVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FilesProcessed != 1 {
		t.Fatalf("cleanup processed %d files after read release, want 1", result.FilesProcessed)
	}
}
