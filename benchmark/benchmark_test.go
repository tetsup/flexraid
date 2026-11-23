package benchmark

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"os"
	"testing"

	"github.com/tetsup/flexraid/management"
)

func TestSelectBenchmarkDisksPreservesConfiguredOrderForEqualCapacity(t *testing.T) {
	parent := t.TempDir()
	disks := make([]management.DriveConfig, 5)
	for index := range disks {
		root := filepath.Join(parent, fmt.Sprintf("disk-%d", index))
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		disks[index] = management.DriveConfig{Root: root, WWN: fmt.Sprintf("drive-%d", index)}
	}
	selected, err := selectBenchmarkDisks(disks)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 4 {
		t.Fatalf("selected %d drives, want 4", len(selected))
	}
	for index := range selected {
		if selected[index].WWN != disks[index].WWN {
			t.Fatalf("selected drive %d = %s, want %s", index, selected[index].WWN, disks[index].WWN)
		}
	}
}

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

func TestValidateOutputPathResolvesSymlinks(t *testing.T) {
	root := t.TempDir()
	config := testConfig(filepath.Join(root, "disk"))
	link := filepath.Join(root, "output-link")
	if err := os.Symlink(config.Disks[0].Root, link); err != nil {
		t.Fatal(err)
	}
	if err := validateOutputPath(link, config); err == nil {
		t.Fatal("validateOutputPath accepted a symlink into a storage root")
	}
}

func testConfig(root string) management.Config {
	parent := filepath.Dir(root)
	return management.Config{
		Disks:      []management.DriveConfig{{Root: root, Mountpoint: filepath.Join(parent, "physical")}},
		Mountpoint: filepath.Join(parent, "fuse"),
	}
}
