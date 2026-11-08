package fuse_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMountedLookupReaddirAndOpenErrors(t *testing.T) {
	mountpoint := mountTestFilesystem(t)

	filePath := filepath.Join(mountpoint, "entry.txt")
	if err := os.WriteFile(filePath, []byte("lookup-data"), 0640); err != nil {
		t.Fatalf("create file: %v", err)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("Lookup/Getattr existing file: %v", err)
	}
	if !info.Mode().IsRegular() || info.Size() != int64(len("lookup-data")) {
		t.Errorf("stat existing file = mode %v size %d", info.Mode(), info.Size())
	}

	entries, err := os.ReadDir(mountpoint)
	if err != nil {
		t.Fatalf("Readdir root: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "entry.txt" || entries[0].IsDir() {
		t.Errorf("Readdir entries = %#v, want one regular entry.txt", entries)
	}

	if _, err := os.Stat(filepath.Join(mountpoint, "missing")); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("Lookup missing entry error = %v, want ENOENT", err)
	}
	if _, err := os.ReadDir(filePath); !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("Readdir regular file error = %v, want ENOTDIR", err)
	}
}

func TestMountedCreateOpenReadWriteFlushFsyncRelease(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	path := filepath.Join(mountpoint, "data")

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatalf("Create/Open: %v", err)
	}
	if _, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600); !errors.Is(err, syscall.EEXIST) {
		t.Errorf("Create existing name error = %v, want EEXIST", err)
	}
	if n, err := file.WriteAt([]byte("abcdef"), 0); err != nil || n != 6 {
		t.Fatalf("WriteAt = %d, %v; want 6 bytes", n, err)
	}
	if n, err := file.WriteAt([]byte("XY"), 2); err != nil || n != 2 {
		t.Fatalf("overwrite WriteAt = %d, %v; want 2 bytes", n, err)
	}
	if err := file.Sync(); err != nil {
		t.Fatalf("Fsync: %v", err)
	}
	got := make([]byte, 6)
	if n, err := file.ReadAt(got, 0); err != nil || n != len(got) {
		t.Fatalf("ReadAt = %d, %v; want 6 bytes", n, err)
	}
	if string(got) != "abXYef" {
		t.Errorf("ReadAt content = %q, want %q", got, "abXYef")
	}
	if _, err := file.ReadAt(make([]byte, 1), 6); !errors.Is(err, io.EOF) {
		t.Errorf("ReadAt at EOF error = %v, want EOF", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Release/Close: %v", err)
	}
}

func TestMountedSetattrChmodAndTruncate(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	path := filepath.Join(mountpoint, "attributes")
	if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		t.Fatalf("create file: %v", err)
	}

	if err := os.Chmod(path, 0640); err != nil {
		t.Fatalf("Setattr chmod: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Getattr after chmod: %v", err)
	}
	if got := info.Mode().Perm(); got != 0640 {
		t.Errorf("mode after chmod = %#o, want %#o", got, 0640)
	}

	if err := os.Truncate(path, 4); err != nil {
		t.Fatalf("Setattr shrink: %v", err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatalf("Getattr after shrink: %v", err)
	}
	if info.Size() != 4 {
		t.Errorf("size after shrink = %d, want 4", info.Size())
	}
	if err := os.Truncate(path, 9); err != nil {
		t.Fatalf("Setattr expand: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Read after expand: %v", err)
	}
	if string(got) != "0123\x00\x00\x00\x00\x00" {
		t.Errorf("expanded content = %q, want zero-filled tail", got)
	}
	if err := os.Chmod(filepath.Join(mountpoint, "missing"), 0600); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("Setattr missing path error = %v, want ENOENT", err)
	}
}

func TestMountedMkdirUnlinkAndRmdirErrors(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	dir := filepath.Join(mountpoint, "directory")
	if err := os.Mkdir(dir, 0750); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.Mkdir(dir, 0750); !errors.Is(err, syscall.EEXIST) {
		t.Errorf("Mkdir existing directory error = %v, want EEXIST", err)
	}
	if err := os.Mkdir(filepath.Join(mountpoint, "missing", "child"), 0750); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("Mkdir with missing parent error = %v, want ENOENT", err)
	}

	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := os.Remove(dir); !errors.Is(err, syscall.ENOTEMPTY) {
		t.Errorf("Rmdir non-empty directory error = %v, want EPERM or ENOTEMPTY", err)
	}
	if err := syscall.Unlink(dir); !errors.Is(err, syscall.EISDIR) {
		t.Errorf("Unlink directory error = %v, want EISDIR", err)
	}
	if err := syscall.Rmdir(file); !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("Rmdir regular file error = %v, want ENOTDIR", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatalf("Unlink child: %v", err)
	}
	if err := os.Remove(file); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("Unlink missing child error = %v, want ENOENT", err)
	}
	if err := syscall.Rmdir(dir); err != nil {
		t.Errorf("Rmdir empty directory: %v", err)
	}
	if err := syscall.Rmdir(dir); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("Rmdir missing directory error = %v, want ENOENT", err)
	}
}

func TestMountedHardLinkSharesInodeAndUnlinksOneName(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	source := filepath.Join(mountpoint, "source")
	alias := filepath.Join(mountpoint, "alias")
	if err := os.WriteFile(source, []byte("shared-data"), 0640); err != nil {
		t.Fatalf("create source: %v", err)
	}
	if err := os.Link(source, alias); err != nil {
		t.Fatalf("Link: %v", err)
	}

	sourceInfo, err := os.Stat(source)
	if err != nil {
		t.Fatalf("stat source: %v", err)
	}
	aliasInfo, err := os.Stat(alias)
	if err != nil {
		t.Fatalf("stat alias: %v", err)
	}
	sourceStat, sourceOK := sourceInfo.Sys().(*syscall.Stat_t)
	aliasStat, aliasOK := aliasInfo.Sys().(*syscall.Stat_t)
	if !sourceOK || !aliasOK {
		t.Fatalf("unexpected stat types: source=%T alias=%T", sourceInfo.Sys(), aliasInfo.Sys())
	}
	if sourceStat.Ino != aliasStat.Ino {
		t.Errorf("hard-link inode mismatch: source=%d alias=%d", sourceStat.Ino, aliasStat.Ino)
	}
	if sourceStat.Nlink != 2 || aliasStat.Nlink != 2 {
		t.Errorf("hard-link nlink: source=%d alias=%d, want 2", sourceStat.Nlink, aliasStat.Nlink)
	}
	if got, err := os.ReadFile(alias); err != nil || string(got) != "shared-data" {
		t.Errorf("read alias = %q, %v; want shared-data", got, err)
	}
	if err := os.Link(filepath.Join(mountpoint, "missing"), filepath.Join(mountpoint, "missing-alias")); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("Link missing source error = %v, want ENOENT", err)
	}
	if err := os.Link(mountpoint, filepath.Join(mountpoint, "directory-alias")); !errors.Is(err, syscall.EPERM) {
		t.Errorf("Link directory error = %v, want EPERM or EISDIR", err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatalf("Unlink original name: %v", err)
	}
	if got, err := os.ReadFile(alias); err != nil || string(got) != "shared-data" {
		t.Errorf("read surviving alias = %q, %v; want shared-data", got, err)
	}
	aliasInfo, err = os.Stat(alias)
	if err != nil {
		t.Fatalf("stat surviving alias: %v", err)
	}
	if stat, ok := aliasInfo.Sys().(*syscall.Stat_t); !ok || stat.Nlink != 1 {
		t.Errorf("surviving alias stat = %#v, want nlink 1", aliasInfo.Sys())
	}
}

func TestMountedSymlinkAndReadlink(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	target := filepath.Join(mountpoint, "target")
	link := filepath.Join(mountpoint, "symbolic")
	if err := os.WriteFile(target, []byte("target-data"), 0600); err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := os.Symlink("target", link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if got, err := os.Readlink(link); err != nil || got != "target" {
		t.Errorf("Readlink = %q, %v; want target", got, err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Getattr symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink mode = %v, want symlink", info.Mode())
	}
	if got, err := os.ReadFile(link); err != nil || string(got) != "target-data" {
		t.Errorf("follow symlink read = %q, %v; want target-data", got, err)
	}
	if err := os.Symlink("other", link); !errors.Is(err, syscall.EEXIST) {
		t.Errorf("Symlink existing name error = %v, want EEXIST", err)
	}
	if err := os.Symlink("missing-target", filepath.Join(mountpoint, "dangling")); err != nil {
		t.Fatalf("create dangling symlink: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mountpoint, "dangling")); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("follow dangling symlink error = %v, want ENOENT", err)
	}
	if _, err := os.Lstat(filepath.Join(mountpoint, "dangling")); err != nil {
		t.Errorf("Lstat dangling symlink: %v", err)
	}
}

func TestMountedRenameReplacementAndFailureCases(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	source := filepath.Join(mountpoint, "source")
	destination := filepath.Join(mountpoint, "destination")
	if err := os.WriteFile(source, []byte("source-data"), 0600); err != nil {
		t.Fatalf("create source: %v", err)
	}
	if err := os.WriteFile(destination, []byte("old-data"), 0600); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	if err := os.Rename(source, destination); err != nil {
		t.Fatalf("Rename replacing regular file: %v", err)
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "source-data" {
		t.Errorf("destination after replacement = %q, %v; want source-data", got, err)
	}
	if _, err := os.Stat(source); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("source after Rename error = %v, want ENOENT", err)
	}
	if err := os.Rename(filepath.Join(mountpoint, "missing"), destination); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("Rename missing source error = %v, want ENOENT", err)
	}
	if err := os.Rename(destination, filepath.Join(mountpoint, "missing-parent", "new")); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("Rename missing destination parent error = %v, want ENOENT", err)
	}

	dir := filepath.Join(mountpoint, "dir")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	child := filepath.Join(dir, "child")
	if err := os.WriteFile(child, []byte("x"), 0600); err != nil {
		t.Fatalf("create directory child: %v", err)
	}
	if err := os.Rename(dir, child); !errors.Is(err, syscall.EINVAL) {
		t.Errorf("Rename directory onto descendant error = %v, want EINVAL", err)
	}
	if err := os.Rename(dir, filepath.Join(mountpoint, "moved-dir")); err != nil {
		t.Errorf("Rename directory to new name: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mountpoint, "moved-dir", "child")); err != nil {
		t.Errorf("child after directory Rename: %v", err)
	}
}
