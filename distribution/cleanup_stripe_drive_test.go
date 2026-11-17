package distribution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared/config"
	"github.com/tetsup/flexraid/storage"
)

func TestCleanupStripeDriveDeletesOnlySelectedDriveOldVersion(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	id := testObjectID()
	request := WriteStripeRequest{
		Context: ctx, ObjectID: id, StripeID: 8, ChunkSize: 32,
		Layout: Layout{Type: LayoutMirror, NumChunks: 2}, IOIndexes: []int{0, 1},
		Data: []byte("version one"),
	}
	first, err := created.Layer.WriteStripe(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Data = []byte("version two")
	second, err := created.Layer.WriteStripe(request)
	if err != nil {
		t.Fatal(err)
	}
	oldKey := storage.FileKey{
		ObjectID: id, StripeVersion: first.Version, StripeID: request.StripeID,
		LayoutID: storage.LayoutID{Type: storage.LayoutType(LayoutMirror), DriveCount: 2, Role: storage.ChunkRoleData, Index: 1},
	}
	newKey := oldKey
	newKey.StripeVersion = second.Version

	result, err := created.Layer.CleanupStripeDrive(CleanupStripeDriveRequest{
		Context: ctx, IOIndex: 0, ObjectID: id, StripeID: request.StripeID, Reason: CleanupOldVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FilesProcessed != 1 {
		t.Fatalf("processed %d files, want 1", result.FilesProcessed)
	}
	if _, err := ios[0].Read(physicalio.ReadRequest{Context: ctx, Key: oldKey}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old version remains on selected drive: %v", err)
	}
	oldKeyOnDrive1 := oldKey
	oldKeyOnDrive1.LayoutID.Index = 2
	if _, err := ios[1].Read(physicalio.ReadRequest{Context: ctx, Key: oldKeyOnDrive1}); err != nil {
		t.Fatalf("old version on unselected drive was changed: %v", err)
	}
	if _, err := ios[0].Read(physicalio.ReadRequest{Context: ctx, Key: newKey}); err != nil {
		t.Fatalf("current version was removed: %v", err)
	}
	candidates := created.Layer.cleanupCandidates()
	if len(candidates) != 1 || candidates[0].ioIndex != 1 {
		t.Fatalf("remaining candidates = %+v; want only drive 1", candidates)
	}
}

func TestCleanupStripeDrivePreservesAbnormalUnreadableChunk(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	id := testObjectID()
	currentKey := storage.FileKey{
		ObjectID: id, StripeVersion: 1, StripeID: 9,
		LayoutID: storage.LayoutID{Type: storage.LayoutType(LayoutMirror), DriveCount: 2, Role: storage.ChunkRoleData, Index: 1},
	}
	if _, err := ios[0].Write(physicalio.WriteRequest{Context: ctx, Key: currentKey, Data: encodeChunk(4, []byte("good"))}); err != nil {
		t.Fatal(err)
	}
	if _, err := ios[1].Write(physicalio.WriteRequest{Context: ctx, Key: storage.FileKey{
		ObjectID: id, StripeVersion: 1, StripeID: 9,
		LayoutID: storage.LayoutID{Type: storage.LayoutType(LayoutMirror), DriveCount: 2, Role: storage.ChunkRoleData, Index: 2},
	}, Data: encodeChunk(4, []byte("good"))}); err != nil {
		t.Fatal(err)
	}
	abnormalKey := currentKey
	abnormalKey.StripeVersion = 99
	if _, err := ios[0].Write(physicalio.WriteRequest{Context: ctx, Key: abnormalKey, Data: []byte("unreadable header and chunk")}); err != nil {
		t.Fatal(err)
	}

	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	if candidates := created.Layer.cleanupCandidates(); len(candidates) != 1 ||
		candidates[0].ioIndex != 0 || candidates[0].reason != CleanupAbnormalVersion {
		t.Fatalf("startup cleanup candidates = %+v; orphans = %+v", candidates, created.Layer.orphans[id])
	}
	result, err := created.Layer.CleanupStripeDrive(CleanupStripeDriveRequest{
		Context: ctx, IOIndex: 0, ObjectID: id, StripeID: 9, Reason: CleanupAbnormalVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FilesProcessed != 1 {
		t.Fatalf("processed %d files, want 1", result.FilesProcessed)
	}
	relative, err := abnormalKey.Path()
	if err != nil {
		t.Fatal(err)
	}
	preserved := filepath.Join(ios[0].RootPath(), config.PreservationDirectory, filepath.Base(relative))
	if data, err := os.ReadFile(preserved); err != nil || string(data) != "unreadable header and chunk" {
		t.Fatalf("preserved data = %q, %v", data, err)
	}
	if _, err := ios[0].Read(physicalio.ReadRequest{Context: ctx, Key: currentKey}); err != nil {
		t.Fatalf("current version was affected: %v", err)
	}
	if candidates := created.Layer.cleanupCandidates(); len(candidates) != 0 {
		t.Fatalf("completed abnormal candidate remains queued: %+v", candidates)
	}
}
