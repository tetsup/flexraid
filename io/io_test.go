package io

import (
	"bytes"
	"context"
	"errors"
	stdio "io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

func newTestLayer(root string) *IO {
	logger := slog.New(slog.NewTextHandler(stdio.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return New(NewRequest{Root: root, Logger: logger}).Layer
}

func newTestLayerWithOutput(root string, output *bytes.Buffer, level slog.Level) *IO {
	logger := slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{Level: level}))
	return New(NewRequest{Root: root, Logger: logger}).Layer
}

func TestReadWriteDelete(t *testing.T) {
	layer := newTestLayer(t.TempDir())
	ctx := context.Background()
	key := testFileKey(0)
	data := []byte("stripe data")

	written, err := layer.Write(WriteRequest{Context: ctx, Key: key, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if written.BytesWritten != len(data) {
		t.Fatalf("wrote %d bytes, want %d", written.BytesWritten, len(data))
	}
	if _, err := layer.Write(WriteRequest{Context: ctx, Key: key, Data: []byte("replacement")}); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected os.ErrExist, got %v", err)
	}
	read, err := layer.Read(ReadRequest{Context: ctx, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	if string(read.Data) != string(data) {
		t.Fatalf("got %q, want %q", read.Data, data)
	}
	deleted, err := layer.Delete(DeleteRequest{Context: ctx, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	if !deleted.Deleted {
		t.Fatal("expected Deleted to be true")
	}
	if _, err := layer.Read(ReadRequest{Context: ctx, Key: key}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

func TestScanOnlyStripeZero(t *testing.T) {
	layer := newTestLayer(t.TempDir())
	ctx := context.Background()
	for _, key := range []storage.FileKey{testFileKey(0), testFileKey(1)} {
		if _, err := layer.Write(WriteRequest{Context: ctx, Key: key, Data: []byte("stripe data")}); err != nil {
			t.Fatal(err)
		}
	}

	var found []ScanEntry
	response, err := layer.Scan(ScanRequest{Context: ctx, Callback: func(entry ScanEntry) error {
		found = append(found, entry)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Entries != 1 || len(found) != 1 || found[0].Key.StripeID != 0 || string(found[0].Data) != "stripe data" {
		t.Fatalf("unexpected scan result: response=%#v entries=%#v", response, found)
	}
}

func TestScanSkipsInvalidFileNameWithWarning(t *testing.T) {
	root := t.TempDir()
	var logOutput bytes.Buffer
	layer := newTestLayerWithOutput(root, &logOutput, slog.LevelDebug)
	if _, err := layer.Write(WriteRequest{Context: context.Background(), Key: testFileKey(0), Data: []byte("valid header")}); err != nil {
		t.Fatal(err)
	}
	invalidPath := filepath.Join(root, "invalid-file-name")
	if err := os.WriteFile(invalidPath, []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	response, err := layer.Scan(ScanRequest{Context: context.Background(), Callback: func(ScanEntry) error { return nil }})
	if err != nil {
		t.Fatalf("Scan returned an error: %v", err)
	}
	if response.Entries != 1 {
		t.Fatalf("got %d entries, want 1", response.Entries)
	}
	logs := logOutput.String()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "invalid-file-name") {
		t.Fatalf("expected invalid filename warning, got %q", logs)
	}
}

func TestPublicOperationResultsAreDebugAndLevelIsConfigurable(t *testing.T) {
	root := t.TempDir()
	var debugOutput bytes.Buffer
	debugLayer := newTestLayerWithOutput(root, &debugOutput, slog.LevelDebug)
	if _, err := debugLayer.Write(WriteRequest{Context: context.Background(), Key: testFileKey(0), Data: []byte("private payload")}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(debugOutput.String(), "level=DEBUG") {
		t.Fatalf("expected DEBUG result log, got %q", debugOutput.String())
	}
	if strings.Contains(debugOutput.String(), "private payload") {
		t.Fatalf("log contains file payload: %q", debugOutput.String())
	}

	var infoOutput bytes.Buffer
	infoLayer := newTestLayerWithOutput(t.TempDir(), &infoOutput, slog.LevelInfo)
	if _, err := infoLayer.Write(WriteRequest{Context: context.Background(), Key: testFileKey(0), Data: []byte("data")}); err != nil {
		t.Fatal(err)
	}
	if infoOutput.Len() != 0 {
		t.Fatalf("DEBUG logs should be filtered at INFO, got %q", infoOutput.String())
	}
}

func TestExpectedOperationErrorIsNotLoggedAsWarningOrError(t *testing.T) {
	var output bytes.Buffer
	layer := newTestLayerWithOutput(t.TempDir(), &output, slog.LevelDebug)
	_, err := layer.Read(ReadRequest{Context: context.Background(), Key: testFileKey(0)})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
	logs := output.String()
	if strings.Contains(logs, "level=WARN") || strings.Contains(logs, "level=ERROR") {
		t.Fatalf("expected errors must not be elevated to WARN/ERROR: %q", logs)
	}
	if !strings.Contains(logs, "level=DEBUG") {
		t.Fatalf("expected operation result at DEBUG, got %q", logs)
	}
}

func TestScanCallbackError(t *testing.T) {
	layer := newTestLayer(t.TempDir())
	if _, err := layer.Write(WriteRequest{Context: context.Background(), Key: testFileKey(0), Data: []byte("header")}); err != nil {
		t.Fatal(err)
	}
	want := errors.New("stop")
	_, err := layer.Scan(ScanRequest{Context: context.Background(), Callback: func(ScanEntry) error { return want }})
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want callback error", err)
	}
}

func TestPathUsesObjectIDShards(t *testing.T) {
	layer := newTestLayer(t.TempDir())
	key := testFileKey(0)
	if _, err := layer.Write(WriteRequest{Context: context.Background(), Key: key, Data: []byte("data")}); err != nil {
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
