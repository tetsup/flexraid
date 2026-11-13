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
	options := fuse.MountOptions()
	// These integration tests exercise the mounted filesystem as its owner.
	// They do not need cross-user access, which requires user_allow_other in
	// the host's /etc/fuse.conf. The production default is covered separately.
	options.MountOptions.AllowOther = false
	server, err := adapter.Mount(mountpoint, options)
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
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

	beforeRenameInfo, err := os.Stat(oldPath)
	if err != nil {
		t.Fatalf("stat file before rename: %v", err)
	}
	beforeRenameStat, ok := beforeRenameInfo.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("pre-rename stat has unexpected type %T", beforeRenameInfo.Sys())
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
	renamedInfo, err := os.Stat(newPath)
	if err != nil {
		t.Fatalf("stat renamed file: %v", err)
	}
	if renamedInfo.Size() != 7 {
		t.Errorf("renamed file size = %d, want 7", renamedInfo.Size())
	}
	renamedStat, ok := renamedInfo.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("post-rename stat has unexpected type %T", renamedInfo.Sys())
	}
	if renamedStat.Ino != beforeRenameStat.Ino {
		t.Errorf("inode changed across rename: before=%d after=%d", beforeRenameStat.Ino, renamedStat.Ino)
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
	if err := os.Mkdir(filePath, 0755); !errors.Is(err, syscall.EEXIST) {
		t.Errorf("mkdir over existing regular file error = %v, want EEXIST", err)
	}

	if err := os.Remove(directory); err != nil {
		t.Fatalf("remove empty directory: %v", err)
	}
	if err := os.Remove(filePath); err != nil {
		t.Fatalf("remove regular file: %v", err)
	}
}

// TestMountedRmdirReturnsPOSIXErrnos verifies the errno values observable by
// applications through the mounted FUSE filesystem, rather than only checking
// that the Orchestration operation returned some error.
func TestMountedRmdirReturnsPOSIXErrnos(t *testing.T) {
	mountpoint := mountTestFilesystem(t)

	missing := filepath.Join(mountpoint, "missing-directory")
	if err := syscall.Rmdir(missing); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("rmdir missing path error = %v, want ENOENT", err)
	}

	filePath := filepath.Join(mountpoint, "regular-file")
	if err := os.WriteFile(filePath, []byte("x"), 0600); err != nil {
		t.Fatalf("create regular file: %v", err)
	}
	if err := syscall.Rmdir(filePath); !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("rmdir regular file error = %v, want ENOTDIR", err)
	}

	symlinkPath := filepath.Join(mountpoint, "symlink")
	if err := os.Symlink("missing-target", symlinkPath); err != nil {
		t.Fatalf("create symlink for rmdir check: %v", err)
	}
	if err := syscall.Rmdir(symlinkPath); !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("rmdir symlink error = %v, want ENOTDIR", err)
	}
	if info, err := os.Lstat(symlinkPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink after rejected rmdir = %v, %v; want existing symlink", info, err)
	}
	if err := os.Remove(symlinkPath); err != nil {
		t.Errorf("unlink symlink after rejected rmdir: %v", err)
	}

	nonEmpty := filepath.Join(mountpoint, "non-empty-directory")
	if err := os.Mkdir(nonEmpty, 0755); err != nil {
		t.Fatalf("create non-empty directory: %v", err)
	}
	child := filepath.Join(nonEmpty, "child")
	if err := os.WriteFile(child, []byte("x"), 0600); err != nil {
		t.Fatalf("create child file: %v", err)
	}
	if err := syscall.Rmdir(nonEmpty); !errors.Is(err, syscall.ENOTEMPTY) {
		t.Errorf("rmdir non-empty directory error = %v, want ENOTEMPTY", err)
	}

	empty := filepath.Join(mountpoint, "empty-directory")
	if err := os.Mkdir(empty, 0755); err != nil {
		t.Fatalf("create empty directory: %v", err)
	}
	if err := syscall.Rmdir(empty); err != nil {
		t.Errorf("rmdir empty directory error = %v, want success", err)
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
