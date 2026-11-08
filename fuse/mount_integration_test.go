package fuse_test

import (
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
