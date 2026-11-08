package fuse_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMountedHardLinkRenameKeepsOtherNames(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	original := filepath.Join(mountpoint, "original")
	alias := filepath.Join(mountpoint, "alias")
	moved := filepath.Join(mountpoint, "moved")

	if err := os.WriteFile(original, []byte("shared-before"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, alias); err != nil {
		t.Fatalf("Link: %v", err)
	}
	before, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(alias, moved); err != nil {
		t.Fatalf("Rename one HardLink name: %v", err)
	}
	if _, err := os.Lstat(alias); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("old alias after Rename = %v, want ENOENT", err)
	}
	afterOriginal, err := os.Stat(original)
	if err != nil {
		t.Fatalf("stat original after alias Rename: %v", err)
	}
	afterMoved, err := os.Stat(moved)
	if err != nil {
		t.Fatalf("stat moved alias: %v", err)
	}
	beforeStat := before.Sys().(*syscall.Stat_t)
	originalStat := afterOriginal.Sys().(*syscall.Stat_t)
	movedStat := afterMoved.Sys().(*syscall.Stat_t)
	if originalStat.Ino != beforeStat.Ino || movedStat.Ino != beforeStat.Ino {
		t.Errorf("inode identities after Rename: before=%d original=%d moved=%d", beforeStat.Ino, originalStat.Ino, movedStat.Ino)
	}
	if originalStat.Nlink != 2 || movedStat.Nlink != 2 {
		t.Errorf("NLink after Rename: original=%d moved=%d, want 2", originalStat.Nlink, movedStat.Nlink)
	}
	if got, err := os.ReadFile(moved); err != nil || string(got) != "shared-before" {
		t.Errorf("read moved alias = %q, %v", got, err)
	}
	if err := os.WriteFile(moved, []byte("changed"), 0600); err != nil {
		t.Fatalf("write through moved alias: %v", err)
	}
	if got, err := os.ReadFile(original); err != nil || string(got) != "changed" {
		t.Errorf("read original after moved-alias write = %q, %v; want changed", got, err)
	}
}

func TestMountedOpenUnlinkRetainsOpenFileUntilRelease(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	path := filepath.Join(mountpoint, "open-unlinked")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("Unlink while open: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("stat unlinked path = %v, want ENOENT", err)
	}
	if _, err := file.WriteAt([]byte("after!"), 0); err != nil {
		t.Errorf("Write through open unlinked handle: %v", err)
	}
	got := make([]byte, len("after!"))
	if n, err := file.ReadAt(got, 0); err != nil || n != len(got) || string(got) != "after!" {
		t.Errorf("ReadAt open unlinked handle = %q (%d bytes), %v; want after!", got, n, err)
	}
	if err := file.Close(); err != nil {
		t.Errorf("Release: %v", err)
	}
}

func TestMountedRenameAcrossDirectoriesAndReplacementTypes(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	left := filepath.Join(mountpoint, "left")
	right := filepath.Join(mountpoint, "right")
	if err := os.Mkdir(left, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(right, 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(left, "source")
	destination := filepath.Join(right, "destination")
	if err := os.WriteFile(source, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("destination"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, destination); err != nil {
		t.Fatalf("cross-directory Rename replacing file: %v", err)
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "source" {
		t.Errorf("destination after cross-directory Rename = %q, %v", got, err)
	}
	if _, err := os.Lstat(source); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("source after cross-directory Rename = %v, want ENOENT", err)
	}

	dirSource := filepath.Join(left, "directory")
	dirDestination := filepath.Join(right, "directory")
	if err := os.Mkdir(dirSource, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dirDestination, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirDestination, "keep"), []byte("keep"), 0600); err != nil {
		t.Fatalf("Create destination child: %v", err)
	}
	if err := os.Rename(dirSource, dirDestination); !errors.Is(err, syscall.ENOTEMPTY) && !errors.Is(err, syscall.EEXIST) {
		t.Errorf("Rename directory onto non-empty directory = %v, want ENOTEMPTY or EEXIST", err)
	}
	if info, err := os.Stat(dirSource); err != nil || !info.IsDir() {
		t.Errorf("source directory after failed Rename: info=%v err=%v", info, err)
	}
	if info, err := os.Stat(dirDestination); err != nil || !info.IsDir() {
		t.Errorf("destination directory after failed Rename: info=%v err=%v", info, err)
	}
}

func TestMountedSymlinkUnlinkAndDirectoryEntrySemantics(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	target := filepath.Join(mountpoint, "target")
	link := filepath.Join(mountpoint, "link")
	if err := os.WriteFile(target, []byte("target"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	entries, err := os.ReadDir(mountpoint)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("Readdir count = %d, want 2", len(entries))
	}
	if err := os.Remove(link); err != nil {
		t.Fatalf("Unlink symlink: %v", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "target" {
		t.Errorf("target after symlink Unlink = %q, %v", got, err)
	}
	if _, err := os.Readlink(link); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("Readlink after Unlink = %v, want ENOENT", err)
	}
}

func TestMountedStatfsReportsSaneCapacity(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	var stat syscall.Statfs_t
	if err := syscall.Statfs(mountpoint, &stat); err != nil {
		t.Fatalf("Statfs: %v", err)
	}
	if stat.Bsize <= 0 || stat.Frsize <= 0 {
		t.Errorf("Statfs block sizes = %d/%d, want positive", stat.Bsize, stat.Frsize)
	}
	if stat.Blocks == 0 {
		t.Error("Statfs Blocks = 0, want nonzero capacity")
	}
}
