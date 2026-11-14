package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/tetsup/flexraid/pkg/shared/config"
)

const minimumDiskCount = config.DefaultDiskCount

type mountConfig struct {
	DiskRoots  []string
	Mountpoint string
}

// loadConfig parses the small YAML configuration accepted by the mount CLI.
func loadConfig(path string) (mountConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return mountConfig{}, fmt.Errorf("open %q: %w", path, err)
	}
	defer file.Close()

	var config mountConfig
	inDisks := false
	scanner := bufio.NewScanner(file)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "disks:" {
			if inDisks || len(config.DiskRoots) > 0 {
				return mountConfig{}, fmt.Errorf("line %d: duplicate disks key", lineNo)
			}
			inDisks = true
			continue
		}
		if strings.HasPrefix(line, "- ") {
			if !inDisks {
				return mountConfig{}, fmt.Errorf("line %d: disk entry outside disks sequence", lineNo)
			}
			value := unquote(strings.TrimSpace(strings.TrimPrefix(line, "- ")))
			if value == "" {
				return mountConfig{}, fmt.Errorf("line %d: empty disk path", lineNo)
			}
			config.DiskRoots = append(config.DiskRoots, value)
			continue
		}
		if strings.HasPrefix(line, "mountpoint:") {
			if config.Mountpoint != "" {
				return mountConfig{}, fmt.Errorf("line %d: duplicate mountpoint key", lineNo)
			}
			inDisks = false
			config.Mountpoint = unquote(strings.TrimSpace(strings.TrimPrefix(line, "mountpoint:")))
			if config.Mountpoint == "" {
				return mountConfig{}, fmt.Errorf("line %d: empty mountpoint", lineNo)
			}
			continue
		}
		return mountConfig{}, fmt.Errorf("line %d: unsupported YAML entry %q", lineNo, line)
	}
	if err := scanner.Err(); err != nil {
		return mountConfig{}, fmt.Errorf("read %q: %w", path, err)
	}
	if len(config.DiskRoots) < minimumDiskCount {
		return mountConfig{}, fmt.Errorf("got %d disk roots; want at least %d", len(config.DiskRoots), minimumDiskCount)
	}
	if config.Mountpoint == "" {
		return mountConfig{}, fmt.Errorf("mountpoint is required")
	}
	for i, root := range config.DiskRoots {
		for j := 0; j < i; j++ {
			if root == config.DiskRoots[j] {
				return mountConfig{}, fmt.Errorf("disk roots %d and %d are duplicated", j+1, i+1)
			}
		}
		if root == config.Mountpoint {
			return mountConfig{}, fmt.Errorf("mountpoint must not be a disk root")
		}
	}
	return config, nil
}

func unquote(value string) string {
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
		(value[0] == '\'' && value[len(value)-1] == '\'')) {
		return value[1 : len(value)-1]
	}
	return value
}
