package io

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestGetMetricsTracksOperationResultsAndUniqueReadErrors(t *testing.T) {
	layer := newTestLayer(t, t.TempDir())
	ctx := context.Background()
	key := testFileKey(0)

	if _, err := layer.Read(ReadRequest{Context: ctx, Key: key}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first Read error = %v", err)
	}
	if _, err := layer.Read(ReadRequest{Context: ctx, Key: key}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second Read error = %v", err)
	}
	if _, err := layer.Write(WriteRequest{Context: ctx, Key: key, Data: []byte("data")}); err != nil {
		t.Fatal(err)
	}
	if _, err := layer.Read(ReadRequest{Context: ctx, Key: key}); err != nil {
		t.Fatal(err)
	}

	metrics, err := layer.GetMetrics(GetMetricsRequest{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	read := metrics.Operations[OperationRead]
	if read.SuccessCount != 1 || read.FailureCount != 2 {
		t.Fatalf("Read counters = %+v", read)
	}
	if read.ReadErrorCount != 2 || read.UniqueReadErrorFileCount != 1 {
		t.Fatalf("Read error counters = %+v", read)
	}
	if read.Recent.ProcessedCount != 3 || read.Recent.SuccessCount != 1 ||
		read.Recent.FailureCount != 2 || read.Recent.ReadErrorCount != 2 {
		t.Fatalf("recent Read counters = %+v", read.Recent)
	}
	write := metrics.Operations[OperationWrite]
	if write.SuccessCount != 1 || write.FailureCount != 0 {
		t.Fatalf("Write counters = %+v", write)
	}
}

func TestGetMetricsKeepsOnlyLatestThousandOperations(t *testing.T) {
	layer := newTestLayer(t, t.TempDir())
	ctx := context.Background()
	key := testFileKey(0)

	if _, err := layer.Read(ReadRequest{Context: ctx, Key: key}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("initial Read error = %v", err)
	}
	if _, err := layer.Write(WriteRequest{Context: ctx, Key: key, Data: []byte("data")}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < recentOperationLimit; i++ {
		if _, err := layer.Read(ReadRequest{Context: ctx, Key: key}); err != nil {
			t.Fatalf("Read %d: %v", i, err)
		}
	}

	metrics, err := layer.GetMetrics(GetMetricsRequest{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	read := metrics.Operations[OperationRead]
	if read.SuccessCount != recentOperationLimit || read.FailureCount != 1 {
		t.Fatalf("cumulative Read counters = %+v", read)
	}
	if read.Recent.ProcessedCount != recentOperationLimit ||
		read.Recent.SuccessCount != recentOperationLimit ||
		read.Recent.FailureCount != 0 || read.Recent.ReadErrorCount != 0 {
		t.Fatalf("recent Read counters = %+v", read.Recent)
	}
	if read.UniqueReadErrorFileCount != 1 {
		t.Fatalf("unique read error files = %d, want 1", read.UniqueReadErrorFileCount)
	}
}

func TestListChunksMetricsCountReadErrorsByFile(t *testing.T) {
	root := t.TempDir()
	layer := newTestLayer(t, root)
	key := testFileKey(1)
	relative, err := key.Path()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing-target"), path); err != nil {
		t.Fatal(err)
	}

	var callbackErrors int
	response, err := layer.ListChunks(ListChunksRequest{
		Context: context.Background(),
		Callback: func(entry ScanEntry) error {
			if entry.Error != nil {
				callbackErrors++
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Entries != 1 || callbackErrors != 1 {
		t.Fatalf("response=%+v callbackErrors=%d", response, callbackErrors)
	}

	metrics, err := layer.GetMetrics(GetMetricsRequest{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	list := metrics.Operations[OperationListChunks]
	if list.SuccessCount != 1 || list.FailureCount != 0 ||
		list.ReadErrorCount != 1 || list.UniqueReadErrorFileCount != 1 {
		t.Fatalf("ListChunks counters = %+v", list)
	}
}

func TestGetMetricsReturnsIndependentSnapshots(t *testing.T) {
	layer := newTestLayer(t, t.TempDir())
	ctx := context.Background()
	key := testFileKey(0)
	if _, err := layer.Write(WriteRequest{Context: ctx, Key: key, Data: []byte("data")}); err != nil {
		t.Fatal(err)
	}

	first, err := layer.GetMetrics(GetMetricsRequest{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	first.Operations[OperationWrite] = OperationMetrics{FailureCount: 99}

	second, err := layer.GetMetrics(GetMetricsRequest{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	if second.Operations[OperationWrite].SuccessCount != 1 ||
		second.Operations[OperationWrite].FailureCount != 0 {
		t.Fatalf("internal metrics changed through snapshot: %+v", second.Operations[OperationWrite])
	}
}

func TestGetMetricsRejectsNilContext(t *testing.T) {
	layer := newTestLayer(t, t.TempDir())
	if _, err := layer.GetMetrics(GetMetricsRequest{}); err == nil {
		t.Fatal("expected nil context error")
	}
}
