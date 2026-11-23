package benchmark

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/tetsup/flexraid/management"
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

func TestValidateOutputPathRejectsOverlappingStoragePaths(t *testing.T) {
	root := t.TempDir()
	config := testConfig(filepath.Join(root, "disk"))
	for _, output := range []string{
		config.Disks[0].Root,
		filepath.Join(config.Disks[0].Root, "results"),
		root,
	} {
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
	parent := filepath.Dir(root)
	return management.Config{
		Disks: []management.DriveConfig{{Root: root, Mountpoint: filepath.Join(parent, "physical")}},
		Mountpoint: filepath.Join(parent, "fuse"),
	}
}
