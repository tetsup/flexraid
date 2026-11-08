package fuse_test

import (
	"fmt"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func statInode(t *testing.T, path string) (uint64, uint64) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("stat %s returned %T, want *syscall.Stat_t", path, info.Sys())
	}
	return stat.Ino, stat.Nlink
}

func TestMountedOpenFlagsAndAccessModes(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	path := filepath.Join(mountpoint, "flags")

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0640)
	if err != nil {
		t.Fatalf("Create O_WRONLY: %v", err)
	}
	if _, err := file.Write([]byte("first")); err != nil {
		t.Fatalf("write O_WRONLY: %v", err)
	}
	if _, err := file.Read(make([]byte, 1)); !errors.Is(err, syscall.EBADF) {
		t.Errorf("Read on O_WRONLY handle = %v, want EBADF", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close O_WRONLY: %v", err)
	}

	file, err = os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("Open O_RDONLY: %v", err)
	}
	if _, err := file.Write([]byte("x")); !errors.Is(err, syscall.EBADF) {
		t.Errorf("Write on O_RDONLY handle = %v, want EBADF", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close O_RDONLY: %v", err)
	}

	file, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("Open O_APPEND: %v", err)
	}
	if _, err := file.Write([]byte("-append")); err != nil {
		t.Fatalf("append write: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close append handle: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "first-append" {
		t.Errorf("content after O_APPEND = %q, %v; want first-append", got, err)
	}

	file, err = os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatalf("Open O_TRUNC: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close O_TRUNC: %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Errorf("size after O_TRUNC = %v, %v; want 0", info, err)
	}
}

func TestMountedSetattrTimesAndHardLinkPropagation(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	original := filepath.Join(mountpoint, "original")
	alias := filepath.Join(mountpoint, "alias")
	if err := os.WriteFile(original, []byte("timestamps"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, alias); err != nil {
		t.Fatalf("Link: %v", err)
	}

	atime := time.Unix(1_700_000_000, 123_000_000)
	mtime := time.Unix(1_700_000_100, 456_000_000)
	if err := os.Chtimes(alias, atime, mtime); err != nil {
		t.Fatalf("Setattr times through alias: %v", err)
	}
	for _, path := range []string{original, alias} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if !info.ModTime().Equal(mtime) {
			t.Errorf("%s mtime = %v, want %v", path, info.ModTime(), mtime)
		}
	}
	if err := os.Chmod(alias, 0600); err != nil {
		t.Fatalf("Setattr mode through alias: %v", err)
	}
	for _, path := range []string{original, alias} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("%s mode = %#o, want 0600", path, info.Mode().Perm())
		}
	}
	if _, nlink := statInode(t, original); nlink != 2 {
		t.Errorf("NLink after Setattr = %d, want 2", nlink)
	}
}

func TestMountedDirectoryNLinkTracksSubdirectories(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	rootIno, rootLinks := statInode(t, mountpoint)
	if rootLinks != 2 {
		t.Fatalf("empty root NLink = %d, want 2", rootLinks)
	}

	parent := filepath.Join(mountpoint, "parent")
	if err := os.Mkdir(parent, 0755); err != nil {
		t.Fatal(err)
	}
	parentIno, parentLinks := statInode(t, parent)
	if parentIno == 0 {
		t.Error("new directory inode is zero")
	}
	if parentLinks != 2 {
		t.Errorf("new directory NLink = %d, want 2", parentLinks)
	}
	if _, links := statInode(t, mountpoint); links != rootLinks+1 {
		t.Errorf("root NLink after Mkdir = %d, want %d", links, rootLinks+1)
	}

	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	if _, links := statInode(t, parent); links != parentLinks+1 {
		t.Errorf("parent NLink after child Mkdir = %d, want %d", links, parentLinks+1)
	}
	if err := os.Rename(child, filepath.Join(mountpoint, "moved-child")); err != nil {
		t.Fatalf("move child directory across parents: %v", err)
	}
	if _, links := statInode(t, parent); links != parentLinks {
		t.Errorf("parent NLink after child moved out = %d, want %d", links, parentLinks)
	}
	if _, links := statInode(t, mountpoint); links != rootLinks+2 {
		t.Errorf("root NLink after child moved in = %d, want %d", links, rootLinks+2)
	}
	if err := os.Remove(filepath.Join(mountpoint, "moved-child")); err != nil {
		t.Fatalf("Rmdir moved child: %v", err)
	}
	if err := os.Remove(parent); err != nil {
		t.Fatalf("Rmdir parent: %v", err)
	}
	if ino, links := statInode(t, mountpoint); ino != rootIno || links != rootLinks {
		t.Errorf("root identity/NLink after cleanup = %d/%d, want %d/%d", ino, links, rootIno, rootLinks)
	}
}

func TestMountedRenameSameHardLinkInodeIsNoop(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	first := filepath.Join(mountpoint, "first")
	second := filepath.Join(mountpoint, "second")
	if err := os.WriteFile(first, []byte("same-inode"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		t.Fatalf("Link: %v", err)
	}
	ino, links := statInode(t, first)
	if err := os.Rename(first, second); err != nil {
		t.Fatalf("Rename one name over another name for same inode: %v", err)
	}
	if gotIno, gotLinks := statInode(t, first); gotIno != ino || gotLinks != links {
		t.Errorf("source after same-inode Rename = %d/%d, want %d/%d", gotIno, gotLinks, ino, links)
	}
	if gotIno, gotLinks := statInode(t, second); gotIno != ino || gotLinks != links {
		t.Errorf("destination after same-inode Rename = %d/%d, want %d/%d", gotIno, gotLinks, ino, links)
	}
	if got, err := os.ReadFile(first); err != nil || string(got) != "same-inode" {
		t.Errorf("read first after same-inode Rename = %q, %v", got, err)
	}
}

func TestMountedRenameTypeMismatchPreservesBothEntries(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	file := filepath.Join(mountpoint, "file")
	dir := filepath.Join(mountpoint, "directory")
	if err := os.WriteFile(file, []byte("file-data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(file, dir); !errors.Is(err, syscall.EISDIR) {
		t.Errorf("Rename file over directory = %v, want EISDIR", err)
	}
	if got, err := os.ReadFile(file); err != nil || string(got) != "file-data" {
		t.Errorf("source after failed file-to-directory Rename = %q, %v", got, err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("destination after failed file-to-directory Rename = %v, %v", info, err)
	}

	file2 := filepath.Join(mountpoint, "file2")
	dir2 := filepath.Join(mountpoint, "directory2")
	if err := os.WriteFile(file2, []byte("other-data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir2, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir2, file2); !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("Rename directory over file = %v, want ENOTDIR", err)
	}
	if info, err := os.Stat(dir2); err != nil || !info.IsDir() {
		t.Errorf("source after failed directory-to-file Rename = %v, %v", info, err)
	}
	if got, err := os.ReadFile(file2); err != nil || string(got) != "other-data" {
		t.Errorf("destination after failed directory-to-file Rename = %q, %v", got, err)
	}
}

func TestMountedSymlinkRenameAndAbsoluteTarget(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	target := filepath.Join(mountpoint, "target")
	link := filepath.Join(mountpoint, "relative-link")
	otherDirectory := filepath.Join(mountpoint, "other-directory")
	moved := filepath.Join(otherDirectory, "moved-link")
	if err := os.WriteFile(target, []byte("target-content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", link); err != nil {
		t.Fatalf("Symlink relative: %v", err)
	}
	if err := os.Mkdir(otherDirectory, 0755); err != nil {
		t.Fatalf("Mkdir other directory: %v", err)
	}
	if err := os.Rename(link, moved); err != nil {
		t.Fatalf("Rename symlink: %v", err)
	}
	if got, err := os.Readlink(moved); err != nil || got != "target" {
		t.Errorf("Readlink after Rename = %q, %v; want target", got, err)
	}
	if _, err := os.Stat(moved); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("relative symlink after move should be dangling: %v, want ENOENT", err)
	}

	absoluteTarget := filepath.Join(mountpoint, "absolute-target")
	absoluteLink := filepath.Join(mountpoint, "absolute-link")
	if err := os.WriteFile(absoluteTarget, []byte("absolute"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(absoluteTarget, absoluteLink); err != nil {
		t.Fatalf("Symlink absolute: %v", err)
	}
	if got, err := os.ReadFile(absoluteLink); err != nil || string(got) != "absolute" {
		t.Errorf("read absolute symlink = %q, %v", got, err)
	}
}

func TestMountedEmptyDirectoryAndStatfsLogicalCounts(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	entries, err := os.ReadDir(mountpoint)
	if err != nil {
		t.Fatalf("Readdir empty directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("empty Readdir returned %d entries: %v", len(entries), entries)
	}
	var before syscall.Statfs_t
	if err := syscall.Statfs(mountpoint, &before); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(mountpoint, "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	var afterCreate syscall.Statfs_t
	if err := syscall.Statfs(mountpoint, &afterCreate); err != nil {
		t.Fatal(err)
	}
	if afterCreate.Files != before.Files+1 {
		t.Errorf("Statfs Files after Create = %d, want %d", afterCreate.Files, before.Files+1)
	}
	alias := filepath.Join(mountpoint, "alias")
	if err := os.Link(file, alias); err != nil {
		t.Fatal(err)
	}
	var afterLink syscall.Statfs_t
	if err := syscall.Statfs(mountpoint, &afterLink); err != nil {
		t.Fatal(err)
	}
	if afterLink.Files != afterCreate.Files {
		t.Errorf("Statfs Files after HardLink = %d, want unchanged %d", afterLink.Files, afterCreate.Files)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	var afterDelete syscall.Statfs_t
	if err := syscall.Statfs(mountpoint, &afterDelete); err != nil {
		t.Fatal(err)
	}
	if afterDelete.Files != before.Files {
		t.Errorf("Statfs Files after final Unlink = %d, want %d", afterDelete.Files, before.Files)
	}
}

func TestMountedRenameDirectoryCycleAndEmptyDirectoryReplacement(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	parent := filepath.Join(mountpoint, "parent")
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(parent, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	parentIno, parentLinks := statInode(t, parent)
	rootIno, rootLinks := statInode(t, mountpoint)

	if err := os.Rename(parent, child); !errors.Is(err, syscall.EINVAL) {
		t.Errorf("Rename directory into its descendant = %v, want EINVAL", err)
	}
	if got, err := os.Stat(parent); err != nil || !got.IsDir() {
		t.Errorf("parent after rejected cycle move: info=%v err=%v", got, err)
	}
	if got, err := os.Stat(child); err != nil || !got.IsDir() {
		t.Errorf("child after rejected cycle move: info=%v err=%v", got, err)
	}
	if ino, links := statInode(t, parent); ino != parentIno || links != parentLinks {
		t.Errorf("parent identity/NLink after rejected cycle = %d/%d, want %d/%d", ino, links, parentIno, parentLinks)
	}
	if ino, links := statInode(t, mountpoint); ino != rootIno || links != rootLinks {
		t.Errorf("root identity/NLink after rejected cycle = %d/%d, want %d/%d", ino, links, rootIno, rootLinks)
	}

	source := filepath.Join(mountpoint, "source-dir")
	destination := filepath.Join(mountpoint, "destination-dir")
	if err := os.Mkdir(source, 0710); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	sourceIno, _ := statInode(t, source)
	if err := os.Rename(source, destination); err != nil {
		t.Fatalf("Rename directory over empty directory: %v", err)
	}
	if _, err := os.Lstat(source); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("source directory after replacement = %v, want ENOENT", err)
	}
	if ino, links := statInode(t, destination); ino != sourceIno || links != 2 {
		t.Errorf("destination identity/NLink after empty-dir replacement = %d/%d, want %d/2", ino, links, sourceIno)
	}
	if mode, err := os.Stat(destination); err != nil || mode.Mode().Perm() != 0710 {
		t.Errorf("destination mode after replacement = %v, %v; want source mode 0710", mode, err)
	}
}

func TestMountedRenameReplacesOneHardLinkWithoutLosingOtherAlias(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	source := filepath.Join(mountpoint, "source")
	destination := filepath.Join(mountpoint, "destination")
	remainingAlias := filepath.Join(mountpoint, "remaining-alias")
	if err := os.WriteFile(source, []byte("source-data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("destination-data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(destination, remainingAlias); err != nil {
		t.Fatalf("Link destination alias: %v", err)
	}
	sourceIno, sourceLinks := statInode(t, source)
	destinationIno, destinationLinks := statInode(t, destination)
	if sourceLinks != 1 || destinationLinks != 2 {
		t.Fatalf("initial NLinks = source %d, destination %d; want 1/2", sourceLinks, destinationLinks)
	}

	if err := os.Rename(source, destination); err != nil {
		t.Fatalf("Rename over one name of another hard-linked file: %v", err)
	}
	if _, err := os.Lstat(source); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("source after Rename = %v, want ENOENT", err)
	}
	if ino, links := statInode(t, destination); ino != sourceIno || links != 1 {
		t.Errorf("replacement destination inode/NLink = %d/%d, want %d/1", ino, links, sourceIno)
	}
	if ino, links := statInode(t, remainingAlias); ino != destinationIno || links != 1 {
		t.Errorf("remaining old alias inode/NLink = %d/%d, want %d/1", ino, links, destinationIno)
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "source-data" {
		t.Errorf("replacement destination data = %q, %v", got, err)
	}
	if got, err := os.ReadFile(remainingAlias); err != nil || string(got) != "destination-data" {
		t.Errorf("remaining alias data = %q, %v", got, err)
	}
}

func TestMountedReaddirPaginatesWithoutLosingOrDuplicatingEntries(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	const count = 128
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("entry-%03d-%032x", i, i)
		if err := os.WriteFile(filepath.Join(mountpoint, name), []byte{byte(i)}, 0600); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}
	entries, err := os.ReadDir(mountpoint)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != count {
		t.Fatalf("ReadDir returned %d entries, want %d", len(entries), count)
	}
	seen := make(map[string]bool, count)
	for _, entry := range entries {
		if seen[entry.Name()] {
			t.Errorf("Readdir returned duplicate entry %q", entry.Name())
		}
		seen[entry.Name()] = true
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			t.Errorf("Readdir entry %q has unexpected type %v", entry.Name(), entry.Type())
		}
	}
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("entry-%03d-%032x", i, i)
		if !seen[name] {
			t.Errorf("Readdir omitted expected entry %q", name)
		}
	}
}
