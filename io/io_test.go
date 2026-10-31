package io

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetsup/flexraid/storage"
)

func testFileKey(stripeID storage.StripeID) storage.FileKey {
	return storage.FileKey{
		ObjectID:      storage.ObjectID{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0x4d, 0xef, 0x8a, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde},
		StripeVersion: 1,
		LayoutID:      storage.LayoutID{Type: 'X', DriveCount: 5, Role: storage.ChunkRoleData, Index: 1},
		StripeID:      stripeID,
	}
}

func TestReadWriteDelete(t *testing.T) {
	layer := New(t.TempDir())
	ctx := context.Background()
	key := testFileKey(0)
	data := []byte("stripe data")

	if err := layer.Write(ctx, key, data); err != nil {
		t.Fatal(err)
	}
	if err := layer.Write(ctx, key, []byte("replacement")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected os.ErrExist, got %v", err)
	}
	got, err := layer.Read(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("got %q, want %q", got, data)
	}
	if err := layer.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := layer.Read(ctx, key); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

func TestScanOnlyStripeZero(t *testing.T) {
	layer := New(t.TempDir())
	ctx := context.Background()
	if err := layer.Write(ctx, testFileKey(0), []byte("header")); err != nil {
		t.Fatal(err)
	}
	if err := layer.Write(ctx, testFileKey(1), []byte("other stripe")); err != nil {
		t.Fatal(err)
	}

	var found []ScanEntry
	if err := layer.Scan(ctx, func(entry ScanEntry) error {
		found = append(found, entry)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Key.StripeID != 0 || string(found[0].Data) != "header" {
		t.Fatalf("unexpected scan result: %#v", found)
	}
}

func TestScanCallbackError(t *testing.T) {
	layer := New(t.TempDir())
	if err := layer.Write(context.Background(), testFileKey(0), []byte("header")); err != nil {
		t.Fatal(err)
	}
	want := errors.New("stop")
	err := layer.Scan(context.Background(), func(ScanEntry) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want callback error", err)
	}
}

func TestPathUsesObjectIDShards(t *testing.T) {
	layer := New(t.TempDir())
	key := testFileKey(0)
	if err := layer.Write(context.Background(), key, []byte("data")); err != nil {
		t.Fatal(err)
	}
	relative, err := key.Path()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(layer.root, filepath.FromSlash(relative))); err != nil {
		t.Fatal(err)
	}
}
