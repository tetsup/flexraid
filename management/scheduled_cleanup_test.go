package management

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetsup/flexraid/distribution"
	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

func TestExecuteScheduledCleanupUsesConfiguredDriveAndPermission(t *testing.T) {
	ctx := context.Background()
	rootA, rootB := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	for _, root := range []string{rootA, rootB} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ioA := physicalio.New(physicalio.NewRequest{Root: rootA}).Layer
	ioB := physicalio.New(physicalio.NewRequest{Root: rootB}).Layer
	created, err := distribution.New(distribution.NewRequest{Context: ctx, IOs: []*physicalio.IO{ioA, ioB}})
	if err != nil {
		t.Fatal(err)
	}
	objectID := shared.ObjectID{1}
	write := distribution.WriteStripeRequest{
		Context: ctx, ObjectID: objectID, StripeID: 4, ChunkSize: 32,
		Layout:    distribution.Layout{Type: distribution.LayoutMirror, NumChunks: 2},
		IOIndexes: []int{0, 1}, Data: []byte("old version"),
	}
	if _, err := created.Layer.WriteStripe(write); err != nil {
		t.Fatal(err)
	}
	write.Data = []byte("new version")
	if _, err := created.Layer.WriteStripe(write); err != nil {
		t.Fatal(err)
	}

	layer, err := New(NewRequest{
		Config: Config{Disks: []DriveConfig{
			{Root: rootA, Mountpoint: filepath.Dir(rootA), WWN: "wwn-a", CleanupDeleteAllowed: false},
			{Root: rootB, Mountpoint: filepath.Dir(rootB), WWN: "wwn-b", CleanupDeleteAllowed: true},
		}},
		Distribution:   created.Layer,
		Initialization: InitializationTaskFunc(func(context.Context) (bool, error) { return true, nil }),
	})
	if err != nil {
		t.Fatal(err)
	}

	skipped, err := layer.ExecuteScheduledCleanup(ExecuteScheduledCleanupRequest{
		Context: ctx, WWN: "wwn-a", ObjectID: objectID, StripeID: 4, Method: distribution.CleanupMethodDeleteOldVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !skipped.Skipped || skipped.IOIndex != 0 || skipped.Distribution.FilesProcessed != 0 {
		t.Fatalf("disabled-drive result = %+v", skipped)
	}

	processed, err := layer.ExecuteScheduledCleanup(ExecuteScheduledCleanupRequest{
		Context: ctx, WWN: "wwn-b", ObjectID: objectID, StripeID: 4, Method: distribution.CleanupMethodDeleteOldVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if processed.Skipped || processed.IOIndex != 1 || processed.Distribution.FilesProcessed != 1 {
		t.Fatalf("enabled-drive result = %+v", processed)
	}
}

func TestExecuteScheduledCleanupRejectsUnknownDriveAndMethod(t *testing.T) {
	layer := &Management{
		drives:             map[string]DriveConfig{"wwn-a": {WWN: "wwn-a"}},
		driveOrder:         []string{"wwn-a"},
		cleanupPermissions: map[string]DriveCleanupPermissions{"wwn-a": {DeleteAllowed: true}},
		distribution:       &distribution.Distribution{},
	}
	base := ExecuteScheduledCleanupRequest{Context: context.Background(), WWN: "missing", StripeID: storage.StripeID(1), Method: distribution.CleanupMethodDeleteOldVersion}
	if _, err := layer.ExecuteScheduledCleanup(base); err == nil {
		t.Fatal("unknown drive was accepted")
	}
	base.WWN = "wwn-a"
	base.Method = 99
	if _, err := layer.ExecuteScheduledCleanup(base); err == nil {
		t.Fatal("unknown cleanup method was accepted")
	}
}
