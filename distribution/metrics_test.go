package distribution

import (
	"bytes"
	"context"
	"testing"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/storage"
)

func TestGetMetricsTracksOperationsAndState(t *testing.T) {
	ctx := context.Background()
	created, err := New(NewRequest{IOs: makeIOs(t, 2)})
	if err != nil {
		t.Fatal(err)
	}
	id := testObjectID()
	data := []byte("metrics stripe")
	if _, err := created.Layer.WriteStripe(WriteStripeRequest{Context: ctx, ObjectID: id, StripeID: 3, ChunkSize: 16, Layout: Layout{Type: LayoutMirror, NumChunks: 2}, IOIndexes: []int{0, 1}, Data: data}); err != nil {
		t.Fatal(err)
	}
	if _, err := created.Layer.ReadStripe(ReadStripeRequest{Context: ctx, ObjectID: id, StripeID: 3, ChunkSize: 0}); err == nil {
		t.Fatal("expected invalid read")
	}
	read, err := created.Layer.ReadStripe(ReadStripeRequest{Context: ctx, ObjectID: id, StripeID: 3, ChunkSize: 16})
	if err != nil || !bytes.Equal(read.Data, data) {
		t.Fatalf("read = %q, %v", read.Data, err)
	}
	got, err := created.Layer.GetMetrics(GetMetricsRequest{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	write := got.Operations["WriteStripe"]
	if write.SuccessCount != 1 || write.FailureCount != 0 || write.Recent.ProcessedCount != 1 {
		t.Fatalf("write metrics: %+v", write)
	}
	readMetrics := got.Operations["ReadStripe"]
	if readMetrics.SuccessCount != 1 || readMetrics.FailureCount != 1 || readMetrics.Recent.ProcessedCount != 2 {
		t.Fatalf("read metrics: %+v", readMetrics)
	}
	if got.Stripes.ObjectCount != 1 || got.Stripes.StripeCount != 1 || got.Stripes.RecoverableStripeCount != 1 {
		t.Fatalf("stripe metrics: %+v", got.Stripes)
	}
	if _, ok := got.Operations["GetMetrics"]; ok {
		t.Fatal("GetMetrics must not count itself")
	}
}

func TestGetMetricsCountsRecoverableMissingChunk(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 3)
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	id := testObjectID()
	written, err := created.Layer.WriteStripe(WriteStripeRequest{Context: ctx, ObjectID: id, StripeID: 4, ChunkSize: 4, Layout: Layout{Type: LayoutParity, NumChunks: 2}, IOIndexes: []int{0, 1, 2}, Data: []byte("12345678")})
	if err != nil {
		t.Fatal(err)
	}
	key := storage.FileKey{ObjectID: id, StripeVersion: written.Version, StripeID: 4, LayoutID: storage.LayoutID{Type: storage.LayoutType(LayoutParity), DriveCount: 3, Role: storage.ChunkRoleData, Index: 2}}
	if _, err := ios[1].Delete(physicalio.DeleteRequest{Context: ctx, Key: key}); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Layer.ReadStripe(ReadStripeRequest{Context: ctx, ObjectID: id, StripeID: 4, ChunkSize: 4}); err != nil {
		t.Fatal(err)
	}
	got, err := restarted.Layer.GetMetrics(GetMetricsRequest{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	if got.Stripes.MissingChunkCount != 1 || got.RecoveryCount != 1 {
		t.Fatalf("metrics: %+v", got)
	}
}

func TestGetMetricsSnapshotIsIndependent(t *testing.T) {
	created, err := New(NewRequest{IOs: makeIOs(t, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := created.Layer.WriteStripe(WriteStripeRequest{Context: ctx, ObjectID: testObjectID(), StripeID: 1, ChunkSize: 8, Layout: Layout{Type: LayoutMirror, NumChunks: 1}, IOIndexes: []int{0}, Data: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	first, err := created.Layer.GetMetrics(GetMetricsRequest{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	first.Operations["WriteStripe"] = OperationMetrics{FailureCount: 99}
	first.Stripes.StripeCount = 99
	second, err := created.Layer.GetMetrics(GetMetricsRequest{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	if second.Operations["WriteStripe"].SuccessCount != 1 || second.Stripes.StripeCount != 1 {
		t.Fatalf("snapshot leaked mutation: %+v", second)
	}
}

func TestGetMetricsRequiresContext(t *testing.T) {
	created, err := New(NewRequest{IOs: makeIOs(t, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := created.Layer.GetMetrics(GetMetricsRequest{}); err == nil {
		t.Fatal("expected nil context error")
	}
}
