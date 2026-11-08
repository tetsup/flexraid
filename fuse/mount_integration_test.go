package fuse_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/fuse"
	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/orchestration"
)

func TestMountedEmptyFilesystem(t *testing.T) {
	if _, err := os.Stat("/dev/fuse"); err != nil {
		t.Skipf("FUSE device is unavailable: %v", err)
	}

	ioLayers := make([]*physicalio.IO, 4)
	for index := range ioLayers {
		ioLayers[index] = physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer
	}
	distributed, err := distribution.New(distribution.NewRequest{IOs: ioLayers})
	if err != nil {
		t.Fatalf("initialize Distribution: %v", err)
	}
	created, err := orchestration.CreateRoot(orchestration.CreateRootRequest{
		Distribution: distributed.Layer,
		Mode:         0751,
		UID:          uint32(os.Getuid()),
		GID:          uint32(os.Getgid()),
	})
	if err != nil {
		t.Fatalf("create empty filesystem root: %v", err)
	}
	adapter, err := fuse.New(created.Layer, created.RootID)
	if err != nil {
		t.Fatalf("create FUSE adapter: %v", err)
	}

	mountpoint := filepath.Join(t.TempDir(), "mount")
	if err := os.Mkdir(mountpoint, 0700); err != nil {
		t.Fatalf("create mountpoint: %v", err)
	}
	server, err := adapter.Mount(mountpoint, nil)
	if err != nil {
		if err == syscall.EPERM || err == syscall.EACCES {
			t.Skipf("FUSE mounting is not permitted in this environment: %v", err)
		}
		t.Fatalf("mount empty filesystem: %v", err)
	}
	defer func() {
		if err := server.Unmount(); err != nil {
			t.Errorf("unmount filesystem: %v", err)
		}
	}()
	if err := server.WaitMount(); err != nil {
		t.Fatalf("wait for FUSE mount: %v", err)
	}

	rootInfo, err := os.Stat(mountpoint)
	if err != nil {
		t.Fatalf("stat mounted root: %v", err)
	}
	if !rootInfo.IsDir() {
		t.Fatalf("mounted root mode = %v, want directory", rootInfo.Mode())
	}
	if got := rootInfo.Mode().Perm(); got != 0751 {
		t.Errorf("mounted root permissions = %#o, want %#o", got, uint32(0751))
	}
	stat, ok := rootInfo.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("mounted root stat has unexpected type %T", rootInfo.Sys())
	}
	if stat.Nlink != 2 {
		t.Errorf("mounted root nlink = %d, want 2", stat.Nlink)
	}
	if stat.Uid != uint32(os.Getuid()) || stat.Gid != uint32(os.Getgid()) {
		t.Errorf("mounted root owner = %d:%d, want %d:%d", stat.Uid, stat.Gid, os.Getuid(), os.Getgid())
	}

	entries, err := os.ReadDir(mountpoint)
	if err != nil {
		t.Fatalf("read mounted root directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("newly mounted root contains entries: %v", entries)
	}

	var statfs syscall.Statfs_t
	if err := syscall.Statfs(mountpoint, &statfs); err != nil {
		t.Fatalf("statfs mounted root: %v", err)
	}
	if statfs.Bsize != 4096 {
		t.Errorf("statfs block size = %d, want 4096", statfs.Bsize)
	}
	if statfs.Files != 1 {
		t.Errorf("statfs file count = %d, want root only (1)", statfs.Files)
	}
}

func TestMountedFilesystemFileOperations(t *testing.T) {
	mountpoint := mountTestFilesystem(t)

	directory := filepath.Join(mountpoint, "documents")
	if err := os.Mkdir(directory, 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil {
		t.Fatalf("stat created directory: %v", err)
	}
	if !directoryInfo.IsDir() || directoryInfo.Mode().Perm() != 0750 {
		t.Errorf("created directory mode = %v, want directory 0750", directoryInfo.Mode())
	}

	oldPath := filepath.Join(directory, "note.txt")
	file, err := os.OpenFile(oldPath, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0600)
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	payload := []byte("FlexRAID mounted I/O works")
	if n, err := file.Write(payload); err != nil {
		_ = file.Close()
		t.Fatalf("write file: %v", err)
	} else if n != len(payload) {
		_ = file.Close()
		t.Fatalf("write returned %d bytes, want %d", n, len(payload))
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatalf("fsync file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}

	got, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("read content = %q, want %q", got, payload)
	}

	if err := os.Chmod(oldPath, 0640); err != nil {
		t.Fatalf("chmod file: %v", err)
	}
	fileInfo, err := os.Stat(oldPath)
	if err != nil {
		t.Fatalf("stat file after chmod: %v", err)
	}
	if !fileInfo.Mode().IsRegular() || fileInfo.Mode().Perm() != 0640 {
		t.Errorf("file mode after chmod = %v, want regular file 0640", fileInfo.Mode())
	}
	if fileInfo.Size() != int64(len(payload)) {
		t.Errorf("file size = %d, want %d", fileInfo.Size(), len(payload))
	}

	if err := os.Truncate(oldPath, 7); err != nil {
		t.Fatalf("truncate file: %v", err)
	}
	got, err = os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("read truncated file: %v", err)
	}
	if string(got) != string(payload[:7]) {
		t.Errorf("truncated content = %q, want %q", got, payload[:7])
	}

	newPath := filepath.Join(directory, "renamed.txt")
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatalf("rename file: %v", err)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("stat old path error = %v, want ENOENT", err)
	}
	if got, err := os.ReadFile(newPath); err != nil {
		t.Fatalf("read renamed file: %v", err)
	} else if string(got) != string(payload[:7]) {
		t.Errorf("renamed file content = %q, want %q", got, payload[:7])
	}

	if err := os.Remove(newPath); err != nil {
		t.Fatalf("unlink file: %v", err)
	}
	if _, err := os.Stat(newPath); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("stat unlinked path error = %v, want ENOENT", err)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatalf("rmdir empty directory: %v", err)
	}
}

func TestMountedFilesystemReturnsExpectedErrors(t *testing.T) {
	mountpoint := mountTestFilesystem(t)

	missing := filepath.Join(mountpoint, "missing")
	if _, err := os.Stat(missing); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("stat missing path error = %v, want ENOENT", err)
	}

	directory := filepath.Join(mountpoint, "existing")
	if err := os.Mkdir(directory, 0755); err != nil {
		t.Fatalf("mkdir existing directory: %v", err)
	}
	if err := os.Mkdir(directory, 0755); !errors.Is(err, syscall.EEXIST) {
		t.Errorf("mkdir existing path error = %v, want EEXIST", err)
	}

	filePath := filepath.Join(mountpoint, "regular-file")
	if err := os.WriteFile(filePath, []byte("x"), 0600); err != nil {
		t.Fatalf("create regular file: %v", err)
	}
	if err := os.Mkdir(filePath, 0755); !errors.Is(err, syscall.EEXIST) && !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("mkdir over regular file error = %v, want EEXIST or ENOTDIR", err)
	}

	if err := os.Remove(directory); err != nil {
		t.Fatalf("remove empty directory: %v", err)
	}
	if err := os.Remove(filePath); err != nil {
		t.Fatalf("remove regular file: %v", err)
	}
}

// mountTestFilesystem creates and mounts a fresh empty filesystem for OS-level
// integration tests. The mounted server is unmounted automatically at cleanup.
func mountTestFilesystem(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat("/dev/fuse"); err != nil {
		t.Skipf("FUSE device is unavailable: %v", err)
	}

	ioLayers := make([]*physicalio.IO, 4)
	for index := range ioLayers {
		ioLayers[index] = physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer
	}
	distributed, err := distribution.New(distribution.NewRequest{IOs: ioLayers})
	if err != nil {
		t.Fatalf("initialize Distribution: %v", err)
	}
	created, err := orchestration.CreateRoot(orchestration.CreateRootRequest{
		Distribution: distributed.Layer,
		Mode:         0755,
		UID:          uint32(os.Getuid()),
		GID:          uint32(os.Getgid()),
	})
	if err != nil {
		t.Fatalf("create filesystem root: %v", err)
	}
	adapter, err := fuse.New(created.Layer, created.RootID)
	if err != nil {
		t.Fatalf("create FUSE adapter: %v", err)
	}

	mountpoint := filepath.Join(t.TempDir(), "mount")
	if err := os.Mkdir(mountpoint, 0700); err != nil {
		t.Fatalf("create mountpoint: %v", err)
	}
	server, err := adapter.Mount(mountpoint, nil)
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("FUSE mounting is not permitted in this environment: %v", err)
		}
		t.Fatalf("mount filesystem: %v", err)
	}
	t.Cleanup(func() {
		if err := server.Unmount(); err != nil {
			t.Errorf("unmount filesystem: %v", err)
		}
	})
	if err := server.WaitMount(); err != nil {
		t.Fatalf("wait for FUSE mount: %v", err)
	}
	return mountpoint
}
