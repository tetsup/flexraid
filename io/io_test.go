package io

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

func testFileKey(id storage.StripeID) storage.FileKey {
	return storage.FileKey{
		ObjectID:      shared.ObjectID{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0x4d, 0xef, 0x8a, 0xbc, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab},
		StripeVersion: 1,
		LayoutID:      storage.LayoutID{Type: 'X', DriveCount: 5, Role: storage.ChunkRoleData, Index: 1},
		StripeID:      id,
	}
}

func newTestLayer(t testing.TB, root string) *IO {
	t.Helper()
	var b bytes.Buffer
	return newTestLayerWithOutput(t, root, &b, slog.LevelDebug)
}

func newTestLayerWithOutput(t testing.TB, root string, out *bytes.Buffer, level slog.Level) *IO {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: level}))
	t.Cleanup(func() {
		if t.Failed() && out.Len() > 0 {
			t.Logf("captured logs:\n%s", out.String())
		}
	})
	return New(NewRequest{Root: root, Logger: logger}).Layer
}

func TestReadWriteDelete(t *testing.T) {
	layer := newTestLayer(t, t.TempDir())
	ctx := context.Background()
	key := testFileKey(0)
	data := []byte("stripe data")

	written, err := layer.Write(WriteRequest{Context: ctx, Key: key, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if written.BytesWritten != len(data) {
		t.Fatalf("wrote %d", written.BytesWritten)
	}
	if _, err := layer.Write(WriteRequest{Context: ctx, Key: key, Data: []byte("replacement")}); !errors.Is(err, os.ErrExist) {
		t.Fatalf("got %v", err)
	}

	read, err := layer.Read(ReadRequest{Context: ctx, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read.Data, data) {
		t.Fatalf("got %q", read.Data)
	}

	deleted, err := layer.Delete(DeleteRequest{Context: ctx, Key: key})
	if err != nil || !deleted.Deleted {
		t.Fatalf("delete: %#v %v", deleted, err)
	}
	if _, err := layer.Read(ReadRequest{Context: ctx, Key: key}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v", err)
	}
}

func TestScanHeadersOnlyStripeZero(t *testing.T) {
	layer := newTestLayer(t, t.TempDir())
	ctx := context.Background()
	for _, key := range []storage.FileKey{testFileKey(0), testFileKey(1)} {
		if _, err := layer.Write(WriteRequest{Context: ctx, Key: key, Data: []byte("data")}); err != nil {
			t.Fatal(err)
		}
	}

	var found []ScanEntry
	response, err := layer.ScanHeaders(ScanHeadersRequest{
		Context: ctx,
		Callback: func(entry ScanEntry) error {
			found = append(found, entry)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Entries != 1 || len(found) != 1 || found[0].Key.StripeID != 0 {
		t.Fatalf("%#v %#v", response, found)
	}
}

func TestListChunksIncludesEveryStripe(t *testing.T) {
	layer := newTestLayer(t, t.TempDir())
	ctx := context.Background()
	for _, id := range []storage.StripeID{0, 1, 2} {
		if _, err := layer.Write(WriteRequest{Context: ctx, Key: testFileKey(id), Data: []byte{byte(id)}}); err != nil {
			t.Fatal(err)
		}
	}

	var count int
	response, err := layer.ListChunks(ListChunksRequest{
		Context: ctx,
		Callback: func(ScanEntry) error {
			count++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Entries != 3 || count != 3 {
		t.Fatalf("entries=%d callbacks=%d", response.Entries, count)
	}
}

func TestScanSkipsInvalidFilenameWithWarning(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	layer := newTestLayerWithOutput(t, root, &output, slog.LevelDebug)
	if _, err := layer.Write(WriteRequest{Context: context.Background(), Key: testFileKey(0), Data: []byte("data")}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "invalid-file-name"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	response, err := layer.ScanHeaders(ScanHeadersRequest{
		Context: context.Background(),
		Callback: func(ScanEntry) error {
			return nil
		},
	})
	if err != nil || response.Entries != 1 {
		t.Fatalf("%#v %v", response, err)
	}
	if !strings.Contains(output.String(), "level=WARN") {
		t.Fatalf("logs %q", output.String())
	}
}

func TestExpectedReadErrorIsDebugOnly(t *testing.T) {
	var output bytes.Buffer
	layer := newTestLayerWithOutput(t, t.TempDir(), &output, slog.LevelDebug)
	_, err := layer.Read(ReadRequest{Context: context.Background(), Key: testFileKey(0)})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(output.String(), "level=WARN") || strings.Contains(output.String(), "level=ERROR") {
		t.Fatalf("logs %q", output.String())
	}
}

func TestScanCallbackError(t *testing.T) {
	layer := newTestLayer(t, t.TempDir())
	if _, err := layer.Write(WriteRequest{Context: context.Background(), Key: testFileKey(0), Data: []byte("x")}); err != nil {
		t.Fatal(err)
	}

	want := errors.New("stop")
	_, err := layer.ScanHeaders(ScanHeadersRequest{
		Context: context.Background(),
		Callback: func(ScanEntry) error {
			return want
		},
	})
	if !errors.Is(err, want) {
		t.Fatalf("got %v", err)
	}
}
