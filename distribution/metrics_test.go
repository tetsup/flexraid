package distribution

import (
	"bytes"
	"context"
	"testing"
	"time"

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
	if got.Chunks.Write.SuccessCount != 2 || got.Chunks.Write.RecentProcessedCount != 2 {
		t.Fatalf("chunk write metrics: %+v", got.Chunks.Write)
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

func TestMetricsKeepOnlyLatestThousandSamples(t *testing.T) {
	collector := newMetricsCollector()
	for i := 0; i < recentOperationLimit+2; i++ {
		collector.recordOperation("WriteStripe", time.Duration(i), nil)
	}
	d := &Distribution{metrics: collector}
	for i := 0; i < recentOperationLimit+2; i++ {
		d.recordChunkOperation("Write", nil)
	}
	operations, chunks, _, _, _ := collector.snapshot()
	if operations["WriteStripe"].SuccessCount != recentOperationLimit+2 || operations["WriteStripe"].Recent.ProcessedCount != recentOperationLimit {
		t.Fatalf("operation recent window: %+v", operations["WriteStripe"])
	}
	if chunks.Write.SuccessCount != recentOperationLimit+2 || chunks.Write.RecentProcessedCount != recentOperationLimit {
		t.Fatalf("chunk recent window: %+v", chunks.Write)
	}
}

func TestGetMetricsRetainsRuntimeUnreadableChunk(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	id := testObjectID()
	data := []byte("runtime missing")
	written, err := created.Layer.WriteStripe(WriteStripeRequest{
		Context: ctx, ObjectID: id, StripeID: 9, ChunkSize: 16,
		Layout: Layout{Type: LayoutMirror, NumChunks: 2}, IOIndexes: []int{0, 1}, Data: data,
	})
	if err != nil {
		t.Fatal(err)
	}
	key := storage.FileKey{
		ObjectID: id, StripeVersion: written.Version, StripeID: 9,
		LayoutID: storage.LayoutID{Type: storage.LayoutType(LayoutMirror), DriveCount: 2, Role: storage.ChunkRoleData, Index: 1},
	}
	if _, err := ios[0].Delete(physicalio.DeleteRequest{Context: ctx, Key: key}); err != nil {
		t.Fatal(err)
	}
	created.Layer.mapMu.Lock()
	info := created.Layer.objects[id][9]
	location := info.chunks[0]
	location.data = nil
	info.chunks[0] = location
	created.Layer.versions[stripeIDKey{objectID: id, stripeID: 9}][written.Version].chunks[0] = location
	created.Layer.mapMu.Unlock()

	read, err := created.Layer.ReadStripe(ReadStripeRequest{Context: ctx, ObjectID: id, StripeID: 9, ChunkSize: 16})
	if err != nil || !bytes.Equal(read.Data, data) {
		t.Fatalf("read = %q, %v", read.Data, err)
	}
	got, err := created.Layer.GetMetrics(GetMetricsRequest{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	if got.Stripes.MissingChunkCount != 1 || got.Stripes.CorruptedStripeCount != 1 || got.RecoveryCount != 1 {
		t.Fatalf("runtime missing metrics: %+v", got)
	}
	if got.Chunks.Read.FailureCount == 0 {
		t.Fatalf("read failures not counted: %+v", got.Chunks.Read)
	}
}

func TestGetMetricsReportsCleanupInventoryAndCapacityEstimate(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 4)
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	request := WriteStripeRequest{
		Context: ctx, ObjectID: testObjectID(), StripeID: 17, ChunkSize: 32,
		Layout: Layout{Type: LayoutMirror, NumChunks: 2}, IOIndexes: []int{0, 1},
		Data: []byte("first cleanup version"),
	}
	for _, value := range []string{"first cleanup version", "second cleanup version", "current version"} {
		request.Data = []byte(value)
		if _, err := created.Layer.WriteStripe(request); err != nil {
			t.Fatal(err)
		}
	}

	got, err := created.Layer.GetMetrics(GetMetricsRequest{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.CleanupCandidates) != 2 {
		t.Fatalf("cleanup candidates = %+v, want one old-version summary per drive", got.CleanupCandidates)
	}
	for index, candidate := range got.CleanupCandidates {
		if candidate.Method != CleanupMethodDeleteOldVersion || candidate.DriveIndex != index {
			t.Errorf("candidate[%d] = %+v", index, candidate)
		}
		if candidate.FileCount != 2 || candidate.FileBytes == 0 || candidate.ReclaimableBytes != candidate.FileBytes {
			t.Errorf("candidate[%d] size/count = %+v", index, candidate)
		}
	}
	if !got.CleanupAfterCapacityAvailable {
		t.Fatalf("cleanup capacity estimate unavailable: %s", got.CleanupAfterCapacityError)
	}
	if len(got.CleanupAfterPhysicalAvailableBytes) != len(ios) {
		t.Fatalf("post-cleanup drive capacities = %+v", got.CleanupAfterPhysicalAvailableBytes)
	}
	for index, drive := range got.CleanupAfterPhysicalAvailableBytes {
		wantReclaimable := index < 2
		if drive.DriveIndex != index || (drive.ReclaimableBytes > 0) != wantReclaimable ||
			drive.EstimatedAvailableBytes != drive.CurrentAvailableBytes+drive.ReclaimableBytes {
			t.Errorf("post-cleanup capacity[%d] = %+v", index, drive)
		}
	}
	if got.CleanupAfterMirrorAvailableBlocks == 0 || got.CleanupAfterParityAvailableBlocks == 0 {
		t.Fatalf("logical post-cleanup capacity not estimated: mirror=%d parity=%d",
			got.CleanupAfterMirrorAvailableBlocks, got.CleanupAfterParityAvailableBlocks)
	}
}
