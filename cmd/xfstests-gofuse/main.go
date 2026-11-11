package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

func main() {
	if len(os.Args) != 3 {
		log.Fatalf("usage: %s MOUNTPOINT BACKING_DIRECTORY", os.Args[0])
	}

	mountpoint := os.Args[1]
	backingDir := os.Args[2]

	root, err := fs.NewLoopbackRoot(backingDir)
	if err != nil {
		log.Fatalf("create loopback root: %v", err)
	}

	timeout := time.Second
	server, err := fs.Mount(mountpoint, root, &fs.Options{
		AttrTimeout:  &timeout,
		EntryTimeout: &timeout,
		MountOptions: fuse.MountOptions{
			FsName:    backingDir,
			Name:      "gofuse-loopback",
			AllowOther: true,
			// Let the kernel enforce POSIX mode bits for callers using
			// different UIDs, as pjdfstest does.
			Options: []string{"default_permissions"},
		},
	})
	if err != nil {
		log.Fatalf("mount go-fuse loopback: %v", err)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		if err := server.Unmount(); err != nil {
			log.Printf("unmount go-fuse loopback: %v", err)
		}
	}()

	server.Wait()
}
