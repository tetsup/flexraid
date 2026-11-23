package main

import (
	"encoding/json"
	"testing"
)

func TestFioReportDecodesBandwidth(t *testing.T) {
	data := []byte(`{"jobs":[{"read":{"bw_bytes":1234,"iops":2},"write":{"bw_bytes":5678,"iops":3}}]}`)
	var report fioReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Jobs) != 1 || report.Jobs[0].Write.BWBytes != 5678 || report.Jobs[0].Read.IOPS != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestIoSizesIncludeBoundaryCases(t *testing.T) {
	if len(ioSizes) != 6 {
		t.Fatalf("I/O size count = %d, want 6", len(ioSizes))
	}
	if ioSizes[1].bytes != ioSizes[0].bytes+1 || ioSizes[4].bytes != ioSizes[3].bytes+1 {
		t.Fatalf("unaligned boundary cases missing: %+v", ioSizes)
	}
}
