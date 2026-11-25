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

func TestCleanupStripeDriveProcessesOnlyOneOldVersionFile(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	id := testObjectID()
	request := WriteStripeRequest{
		Context: ctx, ObjectID: id, StripeID: 8, ChunkSize: 32,
		Layout: Layout{Type: LayoutMirror, NumChunks: 2}, IOIndexes: []int{0, 1}, Data: []byte("version one"),
	}
	first, err := created.Layer.writeStripeAtVersion(request, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	request.Data = []byte("version two")
	second, err := created.Layer.writeStripeAtVersion(request, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	request.Data = []byte("version three")
	third, err := created.Layer.writeStripeAtVersion(request, 0, true)
	if err != nil {
		t.Fatal(err)
	}

	oldestKey := storage.FileKey{
		ObjectID: id, StripeVersion: first.Version, StripeID: request.StripeID,
		LayoutID: storage.LayoutID{Type: storage.LayoutType(LayoutMirror), DriveCount: 2, Role: storage.ChunkRoleData, Index: 1},
	}
	secondKey := oldestKey
	secondKey.StripeVersion = second.Version
	currentKey := oldestKey
	currentKey.StripeVersion = third.Version

	result, err := created.Layer.CleanupStripeDrive(CleanupStripeDriveRequest{
		Context: ctx, IOIndex: 0, ObjectID: id, StripeID: request.StripeID, Method: CleanupMethodDeleteOldVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FilesProcessed != 1 || result.Key != oldestKey {
		t.Fatalf("cleanup result = %+v, want exactly oldest file %v", result, oldestKey)
	}
	if _, err := ios[0].Read(physicalio.ReadRequest{Context: ctx, Key: oldestKey}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest version remains on selected drive: %v", err)
	}
	if _, err := ios[0].Read(physicalio.ReadRequest{Context: ctx, Key: secondKey}); err != nil {
		t.Fatalf("second old version was also processed: %v", err)
	}
	if _, err := ios[0].Read(physicalio.ReadRequest{Context: ctx, Key: currentKey}); err != nil {
		t.Fatalf("current version was removed: %v", err)
	}
	oldestKeyOnDrive1 := oldestKey
	oldestKeyOnDrive1.LayoutID.Index = 2
	if _, err := ios[1].Read(physicalio.ReadRequest{Context: ctx, Key: oldestKeyOnDrive1}); err != nil {
		t.Fatalf("old version on unselected drive was changed: %v", err)
	}
	if candidates := created.Layer.cleanupCandidates(); len(candidates) != 2 {
		t.Fatalf("remaining candidates = %+v; want one candidate per drive for remaining old versions", candidates)
	}
}

func TestCleanupStripeDrivePreservesOneAbnormalUnreadableChunk(t *testing.T) {
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
	if _, err := ios[0].Write(physicalio.WriteRequest{Context: ctx, Key: abnormalKey, Data: []byte("bad")}); err != nil {
		t.Fatal(err)
	}

	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	candidates := created.Layer.cleanupCandidates()
	if len(candidates) != 1 || candidates[0].ioIndex != 0 || candidates[0].method != CleanupMethodPreserveAbnormal {
		t.Fatalf("startup cleanup candidates = %+v; orphans = %+v", candidates, created.Layer.orphans[id])
	}
	metrics, err := created.Layer.GetMetrics(GetMetricsRequest{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics.CleanupCandidates) != 1 ||
		metrics.CleanupCandidates[0].Method != CleanupMethodPreserveAbnormal ||
		metrics.CleanupCandidates[0].FileCount != 1 ||
		metrics.CleanupCandidates[0].FileBytes == 0 ||
		metrics.CleanupCandidates[0].ReclaimableBytes != 0 {
		t.Fatalf("abnormal cleanup metrics = %+v", metrics.CleanupCandidates)
	}
	result, err := created.Layer.CleanupStripeDrive(CleanupStripeDriveRequest{
		Context: ctx, IOIndex: 0, ObjectID: id, StripeID: 9, Method: CleanupMethodPreserveAbnormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FilesProcessed != 1 || result.Key != abnormalKey {
		t.Fatalf("cleanup result = %+v, want one preserved file", result)
	}
	relative, err := abnormalKey.Path()
	if err != nil {
		t.Fatal(err)
	}
	preserved := filepath.Join(ios[0].RootPath(), config.PreservationDirectory, filepath.Base(relative))
	if data, err := os.ReadFile(preserved); err != nil || string(data) != "bad" {
		t.Fatalf("preserved data = %q, %v", data, err)
	}
	if _, err := ios[0].Read(physicalio.ReadRequest{Context: ctx, Key: currentKey}); err != nil {
		t.Fatalf("current version was affected: %v", err)
	}
	if candidates := created.Layer.cleanupCandidates(); len(candidates) != 0 {
		t.Fatalf("completed abnormal candidate remains queued: %+v", candidates)
	}
}

func TestCleanupStripeDriveReturnsNoWorkForMissingCandidate(t *testing.T) {
	created, err := New(NewRequest{IOs: makeIOs(t, 2)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := created.Layer.CleanupStripeDrive(CleanupStripeDriveRequest{
		Context: context.Background(), IOIndex: 0, ObjectID: testObjectID(), StripeID: 4,
		Method: CleanupMethodDeleteOldVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FilesProcessed != 0 {
		t.Fatalf("unexpected cleanup result: %+v", result)
	}
}

