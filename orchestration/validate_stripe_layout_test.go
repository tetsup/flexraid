package orchestration

import (
	"context"
	"errors"
	"testing"

	"github.com/tetsup/flexraid/distribution"
	cfg "github.com/tetsup/flexraid/pkg/shared/config"
	"github.com/tetsup/flexraid/storage"
)

func TestValidateHeaderStripeLayoutsIgnoresStripesBeyondEOF(t *testing.T) {
	fixture := newWriteFixture(t)
	// A stale physical stripe from an earlier, larger file is outside the
	// current Header's logical range and must not invalidate the object.
	_, err := fixture.layer.distribution.WriteStripe(distribution.WriteStripeRequest{
		Context: context.Background(), ObjectID: fixture.fileID, StripeID: 1,
		ChunkSize: 4096, Layout: distribution.Layout{Type: distribution.LayoutParity, NumChunks: 3},
		IOIndexes: []int{0, 1, 2, 3}, Data: []byte("stale"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateHeaderStripeLayouts(context.Background(), fixture.layer.distribution, fixture.fileID, fixture.layer.objects[fixture.fileID].file.header); err != nil {
		t.Fatalf("stale stripe beyond EOF rejected: %v", err)
	}
	if _, err := readInitialObjects(context.Background(), fixture.layer.distribution); err != nil {
		t.Fatalf("initialization rejected stale stripe beyond EOF: %v", err)
	}
}

func TestValidateHeaderStripeLayoutsRejectsLatestMismatchedChunkSize(t *testing.T) {
	fixture := newWriteFixture(t)
	h := fixture.layer.objects[fixture.fileID].file.header
	h.Layout = layout{Mode: layoutModeStripe, ChunkSize: 4096, DataChunks: 3, ParityChunks: 1}
	firstCapacity := cfg.HeaderDataCapacity + 2*uint64(cfg.MinimumChunkSize)
	h.Size = firstCapacity + 1
	headerBytes, err := h.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	firstData := make([]byte, int(cfg.HeaderReservedSize)+int(firstCapacity))
	copy(firstData, headerBytes)
	if _, err := fixture.layer.distribution.WriteStripe(distribution.WriteStripeRequest{
		Context: context.Background(), ObjectID: fixture.fileID, StripeID: 0,
		ChunkSize: cfg.MinimumChunkSize, Layout: distribution.Layout{Type: distribution.LayoutParity, NumChunks: 3},
		IOIndexes: []int{0, 1, 2, 3}, Data: firstData,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.distribution.WriteStripe(distribution.WriteStripeRequest{
		Context: context.Background(), ObjectID: fixture.fileID, StripeID: 1,
		ChunkSize: 4096, Layout: distribution.Layout{Type: distribution.LayoutParity, NumChunks: 3},
		IOIndexes: []int{0, 1, 2, 3}, Data: []byte("x"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := validateHeaderStripeLayouts(context.Background(), fixture.layer.distribution, fixture.fileID, h); err != nil {
		t.Fatalf("matching layout rejected: %v", err)
	}

	// The next recoverable version is selected by Distribution. Its physical
	// chunk size conflicts with the Header and must not fall back to v1.
	if _, err := fixture.layer.distribution.WriteStripe(distribution.WriteStripeRequest{
		Context: context.Background(), ObjectID: fixture.fileID, StripeID: 1,
		ChunkSize: 8192, Layout: distribution.Layout{Type: distribution.LayoutParity, NumChunks: 3},
		IOIndexes: []int{0, 1, 2, 3}, Data: []byte("new"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := validateHeaderStripeLayouts(context.Background(), fixture.layer.distribution, fixture.fileID, h); !errors.Is(err, distribution.ErrDataCorrupted) {
		t.Fatalf("mismatched latest layout = %v, want ErrDataCorrupted", err)
	}
	restarted, err := distribution.New(distribution.NewRequest{IOs: fixture.ios})
	if err != nil {
		t.Fatalf("reinitialize Distribution: %v", err)
	}
	info, err := restarted.Layer.GetStripeInfo(distribution.GetStripeInfoRequest{ObjectID: fixture.fileID, StripeID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if info.ChunkSize != 8192 {
		t.Fatalf("scanned latest chunk size = %d, want 8192", info.ChunkSize)
	}
	fixture.layer.objects[fixture.fileID].file.header = h
	geometry, err := geometryFor(h, storage.StripeID(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.readStripeValidated(context.Background(), fixture.fileID, h, geometry); !errors.Is(err, distribution.ErrDataCorrupted) {
		t.Fatalf("runtime stripe read mismatch = %v, want ErrDataCorrupted", err)
	}
	if _, err := readInitialObjects(context.Background(), fixture.layer.distribution); !errors.Is(err, distribution.ErrDataCorrupted) {
		t.Fatalf("initialization with mismatched latest stripe = %v, want ErrDataCorrupted", err)
	}
}

func TestGetStripeInfoReturnsSelectedVersionLayout(t *testing.T) {
	fixture := newWriteFixture(t)
	info, err := fixture.layer.distribution.GetStripeInfo(distribution.GetStripeInfoRequest{
		ObjectID: fixture.fileID, StripeID: storage.StripeID(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.Layout != (distribution.Layout{Type: distribution.LayoutMirror, NumChunks: 2}) {
		t.Fatalf("layout = %+v", info.Layout)
	}
	if info.Version == 0 {
		t.Fatal("selected version is zero")
	}
}
