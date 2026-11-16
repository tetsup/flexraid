package management

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/tetsup/flexraid/pkg/shared/config"
)

// Config is the startup configuration owned and loaded by Management.
type Config struct {
	Disks      []DriveConfig
	Mountpoint string
}

// LoadConfig reads and validates the structural contents of the Management configuration.
// The caller must resolve every configured WWN with ValidateDriveMappings before initializing storage.
// LoadConfig deliberately performs no device I/O so callers can validate before creating storage roots.
func LoadConfig(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open %q: %w", path, err)
	}
	defer file.Close()

	var result Config
	inDisks := false
	var current *DriveConfig
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
				return Config{}, fmt.Errorf("line %d: duplicate disks key", lineNo)
			}
			inDisks = true
			current = nil
			continue
		}
		if inDisks && indent >= 2 && strings.HasPrefix(line, "- root:") {
			if current != nil {
				result.Disks = append(result.Disks, *current)
			}
			current = &DriveConfig{Root: unquoteConfig(strings.TrimSpace(strings.TrimPrefix(line, "- root:")))}
			if current.Root == "" {
				return Config{}, fmt.Errorf("line %d: empty disk root", lineNo)
			}
			continue
		}
		if inDisks && indent >= 4 && current != nil {
			switch {
			case strings.HasPrefix(line, "mountpoint:"):
				if current.Mountpoint != "" {
					return Config{}, fmt.Errorf("line %d: duplicate disk mountpoint", lineNo)
				}
				current.Mountpoint = unquoteConfig(strings.TrimSpace(strings.TrimPrefix(line, "mountpoint:")))
			case strings.HasPrefix(line, "wwn:"):
				if current.WWN != "" {
					return Config{}, fmt.Errorf("line %d: duplicate disk WWN", lineNo)
				}
				current.WWN = unquoteConfig(strings.TrimSpace(strings.TrimPrefix(line, "wwn:")))
			default:
				return Config{}, fmt.Errorf("line %d: unsupported disk entry %q", lineNo, line)
			}
			continue
		}
		if indent == 0 && strings.HasPrefix(line, "mountpoint:") {
			if result.Mountpoint != "" {
				return Config{}, fmt.Errorf("line %d: duplicate mountpoint key", lineNo)
			}
			if current != nil {
				result.Disks = append(result.Disks, *current)
				current = nil
			}
			inDisks = false
			result.Mountpoint = unquoteConfig(strings.TrimSpace(strings.TrimPrefix(line, "mountpoint:")))
			continue
		}
		return Config{}, fmt.Errorf("line %d: unsupported YAML entry %q", lineNo, line)
	}
	if err := scanner.Err(); err != nil {
		return Config{}, fmt.Errorf("read %q: %w", path, err)
	}
	if current != nil {
		result.Disks = append(result.Disks, *current)
	}
	if len(result.Disks) < config.DefaultDiskCount {
		return Config{}, fmt.Errorf("got %d disks; want at least %d", len(result.Disks), config.DefaultDiskCount)
	}
	if result.Mountpoint == "" {
		return Config{}, fmt.Errorf("mountpoint is required")
	}
	if _, err := validateDriveConfig(result.Disks); err != nil {
		return Config{}, err
	}
	for index, disk := range result.Disks {
		if disk.Root == result.Mountpoint || disk.Mountpoint == result.Mountpoint {
			return Config{}, fmt.Errorf("filesystem mountpoint must not equal disk %d root or physical mountpoint", index+1)
		}
	}
	return result, nil
}

func unquoteConfig(value string) string {
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
		(value[0] == '\'' && value[len(value)-1] == '\'')) {
		return value[1 : len(value)-1]
	}
	return value
}
