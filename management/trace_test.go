package management

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTraceRecorderIsDisabledByDefaultAndCanBeToggled(t *testing.T) {
	recorder := newTraceRecorder()
	started := time.Now()
	recorder.record("ignored", started, nil)
	if got := recorder.snapshot(); got.Enabled || len(got.Spans) != 0 {
		t.Fatalf("default trace snapshot = %+v, want disabled and empty", got)
	}

	recorder.start()
	started = time.Now()
	time.Sleep(time.Millisecond)
	recorder.record("Create", started, errors.New("test failure"))
	recorder.stop()
	snapshot := recorder.snapshot()
	if snapshot.Enabled { t.Fatal("trace remained enabled after stop") }
	if len(snapshot.Spans) != 1 { t.Fatalf("got %d spans, want 1", len(snapshot.Spans)) }
	span := snapshot.Spans[0]
	if span.Operation != "Create" || span.StartNS < 0 || span.DurationNS <= 0 || span.Error != "test failure" {
		t.Fatalf("unexpected span: %+v", span)
	}
	recorder.record("after-stop", time.Now(), nil)
	if got := len(recorder.snapshot().Spans); got != 1 { t.Fatalf("recorded after stop: %d spans", got) }
}

func TestManagementTraceControlValidatesContext(t *testing.T) {
	layer := &Management{trace: newTraceRecorder()}
	if _, err := layer.StartTrace(StartTraceRequest{}); err == nil { t.Fatal("StartTrace accepted nil context") }
	if _, err := layer.StartTrace(StartTraceRequest{Context: context.Background()}); err != nil { t.Fatal(err) }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := layer.StopTrace(StopTraceRequest{Context: ctx}); !errors.Is(err, context.Canceled) {
		t.Fatalf("StopTrace error = %v, want context canceled", err)
	}
	if got := layer.trace.snapshot(); !got.Enabled { t.Fatal("canceled StopTrace disabled tracing") }
}
