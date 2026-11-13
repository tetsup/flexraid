package fuse

import (
	"log"
	"os"
	"runtime"
	"sync/atomic"
	"time"
)

var (
	fuseMemoryTraceEnabled = os.Getenv("FLEXRAID_MEMORY_TRACE") == "1"
	fuseMemoryTraceSequence atomic.Uint64
)

// traceFUSECallback records Go heap statistics around each FUSE callback.
// Process RSS and host memory are sampled independently by the CI workflow.
func traceFUSECallback(operation string, fields ...any) func() {
	if !fuseMemoryTraceEnabled {
		return func() {}
	}

	callID := fuseMemoryTraceSequence.Add(1)
	started := time.Now()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	log.Printf("FUSE_MEMORY phase=start call=%d op=%s args=%v heap_alloc=%d heap_inuse=%d heap_sys=%d total_alloc=%d num_gc=%d goroutines=%d",
		callID, operation, fields, before.HeapAlloc, before.HeapInuse, before.HeapSys,
		before.TotalAlloc, before.NumGC, runtime.NumGoroutine())

	return func() {
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		log.Printf("FUSE_MEMORY phase=end call=%d op=%s duration=%s heap_alloc=%d heap_alloc_delta=%d heap_inuse=%d heap_sys=%d total_alloc=%d total_alloc_delta=%d num_gc=%d num_gc_delta=%d goroutines=%d",
			callID, operation, time.Since(started), after.HeapAlloc,
			int64(after.HeapAlloc)-int64(before.HeapAlloc), after.HeapInuse,
			after.HeapSys, after.TotalAlloc, int64(after.TotalAlloc)-int64(before.TotalAlloc),
			after.NumGC, after.NumGC-before.NumGC, runtime.NumGoroutine())
	}
}
