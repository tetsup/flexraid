package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/tetsup/flexraid/pkg/shared/config"
)

const minimumDiskCount = config.DefaultDiskCount

type diskConfig struct {
	Root       string
	Mountpoint string
	WWN        string
}

type mountConfig struct {
	Disks      []diskConfig
	Mountpoint string
}

// loadConfig parses the FlexRAID YAML configuration.
func loadConfig(path string) (mountConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return mountConfig{}, fmt.Errorf("open %q: %w", path, err)
	}
	defer file.Close()

	var result mountConfig
	inDisks := false
	var current *diskConfig
	scanner := bufio.NewScanner(file)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		raw := scanner.Text()
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		if indent == 0 && line == "disks:" {
			if inDisks || len(result.Disks) > 0 {
				return mountConfig{}, fmt.Errorf("line %d: duplicate disks key", lineNo)
			}
			inDisks = true
			current = nil
			continue
		}
		if inDisks && indent >= 2 && strings.HasPrefix(line, "- root:") {
			if current != nil {
				result.Disks = append(result.Disks, *current)
			}
			current = &diskConfig{Root: unquote(strings.TrimSpace(strings.TrimPrefix(line, "- root:")))}
			if current.Root == "" {
				return mountConfig{}, fmt.Errorf("line %d: empty disk root", lineNo)
			}
			continue
		}
		if inDisks && indent >= 4 && current != nil {
			switch {
			case strings.HasPrefix(line, "mountpoint:"):
				if current.Mountpoint != "" {
					return mountConfig{}, fmt.Errorf("line %d: duplicate disk mountpoint", lineNo)
				}
				current.Mountpoint = unquote(strings.TrimSpace(strings.TrimPrefix(line, "mountpoint:")))
			case strings.HasPrefix(line, "wwn:"):
				if current.WWN != "" {
					return mountConfig{}, fmt.Errorf("line %d: duplicate disk WWN", lineNo)
				}
				current.WWN = unquote(strings.TrimSpace(strings.TrimPrefix(line, "wwn:")))
			default:
				return mountConfig{}, fmt.Errorf("line %d: unsupported disk entry %q", lineNo, line)
			}
			continue
		}
		if indent == 0 && strings.HasPrefix(line, "mountpoint:") {
			if result.Mountpoint != "" {
				return mountConfig{}, fmt.Errorf("line %d: duplicate mountpoint key", lineNo)
			}
			inDisks = false
			current = nil
			result.Mountpoint = unquote(strings.TrimSpace(strings.TrimPrefix(line, "mountpoint:")))
			continue
		}
		return mountConfig{}, fmt.Errorf("line %d: unsupported YAML entry %q", lineNo, line)
	}
	if err := scanner.Err(); err != nil {
		return mountConfig{}, fmt.Errorf("read %q: %w", path, err)
	}
	if current != nil {
		result.Disks = append(result.Disks, *current)
	}
	if len(result.Disks) < minimumDiskCount {
		return mountConfig{}, fmt.Errorf("got %d disks; want at least %d", len(result.Disks), minimumDiskCount)
	}
	if result.Mountpoint == "" {
		return mountConfig{}, fmt.Errorf("mountpoint is required")
	}
	roots := make(map[string]struct{}, len(result.Disks))
	mountpoints := make(map[string]struct{}, len(result.Disks))
	wwns := make(map[string]struct{}, len(result.Disks))
	for index, disk := range result.Disks {
		if disk.Root == "" || disk.Mountpoint == "" || disk.WWN == "" {
			return mountConfig{}, fmt.Errorf("disk %d requires root, mountpoint, and WWN", index+1)
		}
		if _, exists := roots[disk.Root]; exists {
			return mountConfig{}, fmt.Errorf("disk root %q is duplicated", disk.Root)
		}
		if _, exists := mountpoints[disk.Mountpoint]; exists {
			return mountConfig{}, fmt.Errorf("disk mountpoint %q is duplicated", disk.Mountpoint)
		}
		if _, exists := wwns[disk.WWN]; exists {
			return mountConfig{}, fmt.Errorf("WWN %q is duplicated", disk.WWN)
		}
		roots[disk.Root] = struct{}{}
		mountpoints[disk.Mountpoint] = struct{}{}
		wwns[disk.WWN] = struct{}{}
	}
	return result, nil
}

func unquote(value string) string {
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
		(value[0] == '\'' && value[len(value)-1] == '\'')) {
		return value[1 : len(value)-1]
	}
	return value
}
