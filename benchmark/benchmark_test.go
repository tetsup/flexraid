package benchmark

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tetsup/flexraid/management"
)

func TestRawBenchmarkRequiresOnlyDirectoryArgument(t *testing.T) {
	err := runRaw(nil)
	if err == nil || !strings.Contains(err.Error(), "--directory is required") {
		t.Fatalf("runRaw error = %v, want missing directory error", err)
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

func TestBuildFioJobUsesBoundedWriteWindow(t *testing.T) {
	job := buildFioJob([]string{"/tmp/one"}, 4096, 1<<20, "write", 20*time.Second)
	for _, expected := range []string{"runtime=20\n", "time_based=1\n", "size=1048576\n", "bs=4096\n"} {
		if !strings.Contains(job, expected) {
			t.Fatalf("fio job missing %q:\n%s", expected, job)
		}
	}
}

func TestBuildFioJobDoesNotRepeatReadDataset(t *testing.T) {
	job := buildFioJob([]string{"/tmp/one"}, 4096, 1<<20, "read", 20*time.Second)
	if strings.Contains(job, "time_based=1") {
		t.Fatalf("read job repeats dataset:\n%s", job)
	}
}

func TestMeasureColdReadReadsEachFileOnce(t *testing.T) {
	root := t.TempDir()
	paths := []string{filepath.Join(root, "first"), filepath.Join(root, "second")}
	for index, path := range paths {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "data.bin"), []byte(strings.Repeat("x", 8192)), 0600); err != nil {
			t.Fatal(err)
		}
		_ = index
	}
	bw, iops, err := measureColdRead(paths, 4096, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if bw <= 0 || iops <= 0 {
		t.Fatalf("measureColdRead returned non-positive metrics: bw=%f iops=%f", bw, iops)
	}
}

func TestBuildFioJobUsesConfiguredDuration(t *testing.T) {
	job := buildFioJob([]string{"/tmp/one"}, 4096, 1<<20, "write", 7*time.Second)
	if !strings.Contains(job, "runtime=7\n") {
		t.Fatalf("fio job does not use configured duration:\n%s", job)
	}
}

func TestMeasureOverwriteUpdatesExistingFileOnce(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "data.bin")
	original := []byte(strings.Repeat("a", 8192))
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	bw, iops, err := measureOverwrite([]string{root}, 4096, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if bw <= 0 || iops <= 0 {
		t.Fatalf("measureOverwrite returned non-positive metrics: bw=%f iops=%f", bw, iops)
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != len(original) {
		t.Fatalf("overwrite changed file size: got %d, want %d", len(actual), len(original))
	}
	if strings.Contains(string(actual), "a") {
		t.Fatal("overwrite did not replace existing file contents")
	}
}

func TestDriveCapacityPathFallsBackToPhysicalMountpoint(t *testing.T) {
	mountpoint := t.TempDir()
	disk := management.DriveConfig{
		Root:       filepath.Join(mountpoint, "not-created-yet"),
		Mountpoint: mountpoint,
	}
	path, err := driveCapacityPath(disk)
	if err != nil {
		t.Fatal(err)
	}
	if path != mountpoint {
		t.Fatalf("capacity path = %q, want %q", path, mountpoint)
	}
}

func TestSelectBenchmarkDisksPreservesConfiguredOrderForEqualCapacity(t *testing.T) {
	parent := t.TempDir()
	disks := make([]management.DriveConfig, 5)
	for index := range disks {
		root := filepath.Join(parent, fmt.Sprintf("disk-%d", index))
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		disks[index] = management.DriveConfig{
			Root: root, Mountpoint: parent, WWN: fmt.Sprintf("drive-%d", index),
		}
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

func TestValidateBenchmarkCapacityRejectsOverflow(t *testing.T) {
	disks := []management.DriveConfig{{WWN: "drive-1", Mountpoint: "/unused"}}
	if err := validateBenchmarkCapacity(disks, int64(^uint64(0)>>1)); err == nil {
		t.Fatal("validateBenchmarkCapacity accepted an overflowing size")
	}
}

func TestRenderWorkerConfigUsesIsolatedRootsAndDisablesGRPC(t *testing.T) {
	disks := []management.DriveConfig{
		{Root: "/original/disk1", Mountpoint: "/mnt/disk1", WWN: "drive-1"},
		{Root: "/original/disk2", Mountpoint: "/mnt/disk2", WWN: "drive-2"},
		{Root: "/original/disk3", Mountpoint: "/mnt/disk3", WWN: "drive-3"},
		{Root: "/original/disk4", Mountpoint: "/mnt/disk4", WWN: "drive-4"},
	}
	roots := []string{"/mnt/disk1/temp", "/mnt/disk2/temp", "/mnt/disk3/temp", "/mnt/disk4/temp"}
	config, err := renderWorkerConfig(disks, roots, "/tmp/fuse-mount", "/tmp/worker.sock")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"root: /mnt/disk1/temp",
		"mountpoint: /mnt/disk4",
		"wwn: drive-4",
		"mountpoint: /tmp/fuse-mount",
		"grpc_socket_path: /tmp/worker.sock",
		"grpc_enabled: false",
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("generated config missing %q:\n%s", expected, config)
		}
	}
	if strings.Contains(config, "/original/") {
		t.Fatalf("generated config retained original storage roots:\n%s", config)
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
