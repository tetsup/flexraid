package fuse_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestMountedReadWriteOffsetsAndZeroLength(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	path := filepath.Join(mountpoint, "offsets")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0600)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if n, err := file.WriteAt(nil, 0); err != nil || n != 0 {
		t.Errorf("zero-length WriteAt = %d, %v; want 0, nil", n, err)
	}
	if n, err := file.ReadAt(nil, 0); err != nil || n != 0 {
		t.Errorf("zero-length ReadAt = %d, %v; want 0, nil", n, err)
	}
	if n, err := file.WriteAt([]byte("tail"), 8); err != nil || n != 4 {
		t.Fatalf("sparse WriteAt = %d, %v; want 4", n, err)
	}
	content := make([]byte, 12)
	if n, err := file.ReadAt(content, 0); err != nil || n != len(content) {
		t.Fatalf("ReadAt sparse file = %d, %v; want 12", n, err)
	}
	want := []byte{0, 0, 0, 0, 0, 0, 0, 0, 't', 'a', 'i', 'l'}
	if string(content) != string(want) {
		t.Errorf("sparse file content = %v, want %v", content, want)
	}
	if n, err := file.ReadAt(make([]byte, 4), 12); n != 0 || !errors.Is(err, io.EOF) {
		t.Errorf("ReadAt beyond EOF = %d, %v; want 0, EOF", n, err)
	}
	if err := file.Close(); err != nil {
		t.Errorf("Release: %v", err)
	}
}

func TestMountedRenameSamePathIsNoop(t *testing.T) {
	mountpoint := mountTestFilesystem(t)
	path := filepath.Join(mountpoint, "same")
	if err := os.WriteFile(path, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	ino, nlink := statInode(t, path)
	if err := os.Rename(path, path); err != nil {
		t.Fatalf("Rename path onto itself: %v", err)
	}
	if gotIno, gotNlink := statInode(t, path); gotIno != ino || gotNlink != nlink {
		t.Errorf("identity after same-path Rename = %d/%d, want %d/%d", gotIno, gotNlink, ino, nlink)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "unchanged" {
		t.Errorf("content after same-path Rename = %q, %v", got, err)
	}
}
