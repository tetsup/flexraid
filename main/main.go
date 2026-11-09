package main

import (
	"flag"
	"log"
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
	server, err := mountFilesystem(config)
	if err != nil {
		log.Fatalf("mount FlexRAID: %v", err)
	}
	log.Printf("FlexRAID mounted at %s", config.Mountpoint)
	if err := server.WaitMount(); err != nil {
		_ = server.Unmount()
		log.Fatalf("wait for mount: %v", err)
	}
}
