package benchmark

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tetsup/flexraid/management"
)

func TestRawBenchmarkRequiresOnlyDirectoryArgument(t *testing.T) {
	err := runRaw(nil)
	if err == nil || !strings.Contains(err.Error(), "--directory is required") {
		t.Fatalf("runRaw error = %v, want missing directory error", err)
	}
}

func TestCreateBenchmarkFilesCreatesDistinctRandomFiles(t *testing.T) {
	root := t.TempDir()
	files, bytesWritten, operations, elapsed, err := createBenchmarkFiles([]string{root}, 4096, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 8 || bytesWritten != 8*4096 || operations != 8 || elapsed <= 0 {
		t.Fatalf("files=%d bytes=%d operations=%d elapsed=%s", len(files), bytesWritten, operations, elapsed)
	}
	seen := make(map[string]struct{})
	for _, path := range files {
		if _, ok := seen[path]; ok {
			t.Fatalf("duplicate generated path %q", path)
		}
		seen[path] = struct{}{}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() != 4096 {
			t.Fatalf("file %s size=%d, want 4096", path, info.Size())
		}
	}
}

func TestOverwriteAndColdReadProcessEachGeneratedFileOnce(t *testing.T) {
	root := t.TempDir()
	files, _, _, _, err := createBenchmarkFiles([]string{root}, 4096, 4)
	if err != nil {
		t.Fatal(err)
	}
	bytesWritten, writes, elapsed, err := measureOverwrite(files, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if writes != int64(len(files)) || bytesWritten != writes*4096 || elapsed <= 0 {
		t.Fatalf("overwrite bytes=%d operations=%d elapsed=%s", bytesWritten, writes, elapsed)
	}
	bytesRead, reads, readElapsed, err := measureColdRead(files, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if reads != int64(len(files)) || bytesRead != int64(len(files))*4096 || readElapsed <= 0 {
		t.Fatalf("read bytes=%d operations=%d elapsed=%s; want one pass over %d files", bytesRead, reads, readElapsed, len(files))
	}
}

func TestRemoveBenchmarkFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "random-name")
	if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeBenchmarkFiles([]string{path}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
}

func TestDriveCapacityPathFallsBackToPhysicalMountpoint(t *testing.T) {
	mountpoint := t.TempDir()
	disk := management.DriveConfig{Root: filepath.Join(mountpoint, "not-created-yet"), Mountpoint: mountpoint}
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
		disks[index] = management.DriveConfig{Root: root, Mountpoint: parent, WWN: fmt.Sprintf("drive-%d", index)}
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
		t.Fatal("accepted overflowing size")
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
	for _, expected := range []string{"root: /mnt/disk1/temp", "mountpoint: /mnt/disk4", "wwn: drive-4", "mountpoint: /tmp/fuse-mount", "grpc_socket_path: /tmp/worker.sock", "grpc_enabled: false"} {
		if !strings.Contains(config, expected) {
			t.Fatalf("missing %q:\n%s", expected, config)
		}
	}
	if strings.Contains(config, "/original/") {
		t.Fatalf("retained original roots:\n%s", config)
	}
}

func TestValidateOutputPathRejectsOverlappingStoragePaths(t *testing.T) {
	root := t.TempDir()
	config := testConfig(filepath.Join(root, "disk"))
	for _, output := range []string{config.Disks[0].Root, filepath.Join(config.Disks[0].Root, "results"), root} {
		if err := validateOutputPath(output, config); err == nil {
			t.Fatalf("validateOutputPath(%q) succeeded", output)
		}
	}
}

func TestValidateOutputPathAllowsSibling(t *testing.T) {
	root := t.TempDir()
	config := testConfig(filepath.Join(root, "disk"))
	if err := validateOutputPath(filepath.Join(root, "results"), config); err != nil {
		t.Fatal(err)
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
		t.Fatal("accepted symlink into storage root")
	}
}

func testConfig(root string) management.Config {
	parent := filepath.Dir(root)
	return management.Config{
		Disks:      []management.DriveConfig{{Root: root, Mountpoint: filepath.Join(parent, "physical")}},
		Mountpoint: filepath.Join(parent, "fuse"),
	}
}

func TestBenchmarkFileCountUsesReferenceAndMaximum(t *testing.T) {
	tests := []struct {
		label     string
		blockSize int64
		want      int
	}{
		{"4KiB capped", 4 << 10, 256},
		{"64KiB capped", 64 << 10, 256},
		{"6MiB+11KiB", (6 << 20) + (11 << 10), 10},
		{"stripe boundary +1B", (6 << 20) + (11 << 10) + 1, 10},
		{"64MiB reference", 64 << 20, 1},
	}
	for _, test := range tests {
		t.Run(test.label, func(t *testing.T) {
			if got := benchmarkFileCount(test.blockSize, 64<<20, 256); got != test.want {
				t.Fatalf("benchmarkFileCount() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestCreateBenchmarkFilesCreatesExactlyRequestedCount(t *testing.T) {
	root := t.TempDir()
	files, bytesWritten, operations, elapsed, err := createBenchmarkFiles([]string{root}, 4096, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || bytesWritten != 3*4096 || operations != 3 || elapsed <= 0 {
		t.Fatalf("files=%d bytes=%d operations=%d elapsed=%s", len(files), bytesWritten, operations, elapsed)
	}
}
