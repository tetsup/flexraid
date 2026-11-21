package management

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGetMetricsTracksPublicOperationResults(t *testing.T) {
	layer, err := New(NewRequest{
		Config:         Config{Disks: []DriveConfig{{Root: t.TempDir(), Mountpoint: t.TempDir(), WWN: "wwn-metrics"}}},
		Initialization: InitializationTaskFunc(func(context.Context) (bool, error) { return true, nil }),
	})
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now().Add(-time.Millisecond)
	layer.recordOperation("Read", started, nil)
	layer.recordOperation("Read", started, errors.New("read failed"))

	got, err := layer.GetMetrics(GetMetricsRequest{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	read := got.Operations["Read"]
	if read.SuccessCount != 1 || read.FailureCount != 1 {
		t.Fatalf("Read counters = %+v", read)
	}
	if read.Recent.ProcessedCount != 2 || read.Recent.SuccessCount != 1 || read.Recent.FailureCount != 1 {
		t.Fatalf("recent Read counters = %+v", read.Recent)
	}

	got.Operations["Read"] = OperationMetrics{FailureCount: 100}
	again, err := layer.GetMetrics(GetMetricsRequest{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if again.Operations["Read"].FailureCount != 1 {
		t.Fatalf("snapshot mutation leaked: %+v", again.Operations["Read"])
	}
}

func TestGetMetricsRequiresLiveContext(t *testing.T) {
	layer, err := New(NewRequest{
		Config:         Config{Disks: []DriveConfig{{Root: t.TempDir(), Mountpoint: t.TempDir(), WWN: "wwn-metrics"}}},
		Initialization: InitializationTaskFunc(func(context.Context) (bool, error) { return true, nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := layer.GetMetrics(GetMetricsRequest{}); err == nil {
		t.Fatal("expected nil context error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := layer.GetMetrics(GetMetricsRequest{Context: ctx}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error = %v", err)
	}
}
