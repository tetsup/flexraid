package distribution

import (
	"context"
	"os"
	"testing"
	"time"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/storage"
)

func TestCleanupWorkerRespectsDrivePermissionsAndStops(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	request := WriteStripeRequest{
		Context: ctx, ObjectID: testObjectID(), StripeID: storage.StripeID(31), ChunkSize: 32,
		Layout: Layout{Type: LayoutMirror, NumChunks: 2}, IOIndexes: []int{0, 1}, Data: []byte("old"),
	}
	first, err := created.Layer.WriteStripe(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Data = []byte("new")
	second, err := created.Layer.WriteStripe(request)
	if err != nil {
		t.Fatal(err)
	}
	oldKey := storage.FileKey{ObjectID: request.ObjectID, StripeVersion: first.Version, StripeID: request.StripeID,
		LayoutID: storage.LayoutID{Type: storage.LayoutType(LayoutMirror), DriveCount: 2, Role: storage.ChunkRoleData, Index: 1}}

	if err := created.Layer.SetCleanupPermissions([]CleanupPermissions{{}, {DeleteAllowed: true}}); err != nil {
		t.Fatal(err)
	}
	if err := created.Layer.StartCleanupWorker(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if err := created.Layer.StopCleanupWorker(stopCtx); err != nil {
			t.Errorf("stop cleanup worker: %v", err)
		}
	}()

	deadline := time.After(3 * time.Second)
	for {
		_, err := ios[1].Read(physicalio.ReadRequest{Context: ctx, Key: storage.FileKey{
			ObjectID: request.ObjectID, StripeVersion: first.Version, StripeID: request.StripeID,
			LayoutID: storage.LayoutID{Type: storage.LayoutType(LayoutMirror), DriveCount: 2, Role: storage.ChunkRoleData, Index: 2},
		}})
		if os.IsNotExist(err) {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("permitted old version was not cleaned; read error = %v (current version %d)", err, second.Version)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := ios[0].Read(physicalio.ReadRequest{Context: ctx, Key: oldKey}); err != nil {
		t.Fatalf("cleanup touched a drive without permission: %v", err)
	}
}
