package main

import (
	"context"
	"flag"
	"log"
	"time"
)

func main() {
	configPath := flag.String("config", "", "path to FlexRAID YAML configuration")
	flag.Parse()
	if *configPath == "" {
		log.Fatal("-config is required")
	}
	config, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}
	mounted, err := mountFilesystem(config)
	if err != nil {
		log.Fatalf("mount FlexRAID: %v", err)
	}
	log.Printf("FlexRAID mounted at %s", config.Mountpoint)
	if err := mounted.server.WaitMount(); err != nil {
		_ = mounted.server.Unmount()
		mounted.stop()
		log.Fatalf("wait for mount: %v", err)
	}
	// WaitMount only confirms that the first FUSE request was served. Keep
	// the daemon alive until the mount is unmounted and the serve loop exits.
	mounted.server.Wait()
	mounted.stop()
}

func (mounted *mountedFilesystem) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mounted.management.Stop(ctx); err != nil {
		log.Printf("stop Management: %v", err)
	}
}
