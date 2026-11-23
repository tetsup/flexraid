package benchmark

import (
	"encoding/json"
	"path/filepath"
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

func TestValidateOutputPathRejectsStorageRoots(t *testing.T) {
	root := t.TempDir()
	config := testConfig(root)
	for _, output := range []string{root, filepath.Join(root, "results")} {
		if err := validateOutputPath(output, config); err == nil {
			t.Fatalf("validateOutputPath(%q) succeeded; want error", output)
		}
	}
}

func TestValidateOutputPathAllowsSibling(t *testing.T) {
	root := t.TempDir()
	config := testConfig(filepath.Join(root, "disk"))
	if err := validateOutputPath(filepath.Join(root, "results"), config); err != nil {
		t.Fatalf("validateOutputPath returned error: %v", err)
	}
}

func testConfig(root string) management.Config {
	return management.Config{
		Disks:      []management.DriveConfig{{Root: root}},
		Mountpoint: filepath.Join(root, "fuse"),
	}
}
