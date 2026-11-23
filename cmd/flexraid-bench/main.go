package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tetsup/flexraid/management"
)

var ioSizes = []struct {
	label string
	bytes int64
}{
	{"4KiB", 4 << 10},
	{"4KiB+1B", (4 << 10) + 1},
	{"64KiB", 64 << 10},
	{"6MiB", 6 << 20},
	{"6MiB+1B", (6 << 20) + 1},
	{"64MiB", 64 << 20},
}

type options struct {
	configPath string
	outputDir  string
	fioPath    string
	totalBytes int64
	runtime    time.Duration
	direct     bool
}

type fioJob struct {
	Read  fioDirection `json:"read"`
	Write fioDirection `json:"write"`
}

type fioDirection struct {
	BWBytes float64 `json:"bw_bytes"`
	IOPS    float64 `json:"iops"`
}

type fioReport struct {
	Jobs []fioJob `json:"jobs"`
}

type sample struct {
	Filesystem string
	IOSize     int64
	Label      string
	Direction  string
	BWBytes    float64
	IOPS       float64
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "flexraid-bench:", err)
		os.Exit(1)
	}
}

func run() error {
	var opts options
	flag.StringVar(&opts.configPath, "config", "", "Worker YAML configuration")
	flag.StringVar(&opts.outputDir, "output", "./flexraid-bench-results", "result directory")
	flag.StringVar(&opts.fioPath, "fio", "fio", "fio executable path")
	flag.Int64Var(&opts.totalBytes, "size", 512<<20, "total logical bytes per test case")
	flag.DurationVar(&opts.runtime, "runtime", 0, "optional time-based fio runtime (0 runs the full size)")
	flag.BoolVar(&opts.direct, "direct", false, "use O_DIRECT (requires FUSE and filesystem support)")
	flag.Parse()

	if opts.configPath == "" {
		return errors.New("--config is required")
	}
	if opts.totalBytes <= 0 || opts.runtime < 0 {
		return errors.New("--size must be positive and --runtime must not be negative")
	}
	if _, err := exec.LookPath(opts.fioPath); err != nil {
		return fmt.Errorf("find fio executable: %w", err)
	}
	config, err := management.LoadConfig(opts.configPath)
	if err != nil {
		return fmt.Errorf("load Worker configuration: %w", err)
	}
	if len(config.Disks) < 4 {
		return fmt.Errorf("configuration has %d disks; at least 4 are required", len(config.Disks))
	}
	if err := validateConfigPaths(config); err != nil {
		return err
	}
	if err := os.MkdirAll(opts.outputDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	workDirs, err := createWorkDirs(config)
	if err != nil {
		return err
	}
	defer func() {
		for _, path := range workDirs {
			if removeErr := os.RemoveAll(path); removeErr != nil {
				fmt.Fprintf(os.Stderr, "warning: remove benchmark directory %s: %v\n", path, removeErr)
			}
		}
	}()

	modes := []struct {
		name  string
		paths []string
	}{
		{name: "raw-1drive", paths: workDirs[:1]},
		{name: "raw-3drive", paths: workDirs[:3]},
		{name: "raw-4drive", paths: workDirs[:4]},
		{name: "flexraid", paths: []string{filepath.Join(config.Mountpoint, filepath.Base(workDirs[0]))}},
	}
	if err := os.MkdirAll(modes[3].paths[0], 0700); err != nil {
		return fmt.Errorf("create FlexRAID benchmark directory: %w", err)
	}

	var samples []sample
	for _, block := range ioSizes {
		if opts.totalBytes < block.bytes {
			return fmt.Errorf("test size %d is smaller than I/O size %d", opts.totalBytes, block.bytes)
		}
		for _, direction := range []string{"write", "read"} {
			for _, mode := range modes {
				fmt.Printf("Running %-10s %-5s %s ...\n", mode.name, direction, block.label)
				measured, err := runCase(opts, mode.name, mode.paths, block.bytes, block.label, direction)
				if err != nil {
					return fmt.Errorf("%s %s %s: %w", mode.name, direction, block.label, err)
				}
				samples = append(samples, measured)
			}
		}
	}
	if err := writeReports(opts.outputDir, samples); err != nil {
		return err
	}
	fmt.Printf("Results: %s\n", opts.outputDir)
	return nil
}

func validateConfigPaths(config management.Config) error {
	mountpoint, err := filepath.Abs(config.Mountpoint)
	if err != nil {
		return fmt.Errorf("resolve FlexRAID mountpoint: %w", err)
	}
	info, err := os.Stat(mountpoint)
	if err != nil {
		return fmt.Errorf("stat FlexRAID mountpoint %s: %w", mountpoint, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("FlexRAID mountpoint %s is not a directory", mountpoint)
	}
	for i, disk := range config.Disks {
		root, err := filepath.Abs(disk.Root)
		if err != nil {
			return fmt.Errorf("resolve disk %d root: %w", i+1, err)
		}
		info, err := os.Stat(root)
		if err != nil {
			return fmt.Errorf("stat disk %d root %s: %w", i+1, root, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("disk %d root %s is not a directory", i+1, root)
		}
		if root == mountpoint || strings.HasPrefix(mountpoint+string(os.PathSeparator), root+string(os.PathSeparator)) ||
			strings.HasPrefix(root+string(os.PathSeparator), mountpoint+string(os.PathSeparator)) {
			return fmt.Errorf("disk %d root and FlexRAID mountpoint overlap", i+1)
		}
	}
	return nil
}

func createWorkDirs(config management.Config) ([]string, error) {
	paths := make([]string, 0, 5)
	for i, disk := range config.Disks {
		path, err := os.MkdirTemp(disk.Root, ".flexraid-bench-")
		if err != nil {
			for _, created := range paths {
				_ = os.RemoveAll(created)
			}
			return nil, fmt.Errorf("create temporary directory on disk %d: %w", i+1, err)
		}
		paths = append(paths, path)
	}
	fusePath, err := os.MkdirTemp(config.Mountpoint, ".flexraid-bench-")
	if err != nil {
		for _, created := range paths {
			_ = os.RemoveAll(created)
		}
		return nil, fmt.Errorf("create temporary directory in FlexRAID mount: %w", err)
	}
	paths = append(paths, fusePath)
	return paths, nil
}

func runCase(opts options, filesystem string, paths []string, blockSize int64, label, direction string) (sample, error) {
	jobCount := len(paths)
	perJob := opts.totalBytes / int64(jobCount)
	if perJob < blockSize {
		return sample{}, fmt.Errorf("per-drive size %d is smaller than block size %d", perJob, blockSize)
	}
	jobPath := filepath.Join(opts.outputDir, filesystem+"-"+label+"-"+direction+".fio")
	resultPath := filepath.Join(opts.outputDir, filesystem+"-"+label+"-"+direction+".json")
	var builder strings.Builder
	builder.WriteString("[global]\n")
	builder.WriteString("ioengine=sync\n")
	builder.WriteString(fmt.Sprintf("direct=%t\n", opts.direct))
	builder.WriteString("rw=" + direction + "\n")
	builder.WriteString("bs=" + strconv.FormatInt(blockSize, 10) + "\n")
	builder.WriteString("fallocate=none\n")
	builder.WriteString("group_reporting=1\n")
	builder.WriteString("end_fsync=1\n")
	builder.WriteString("invalidate=1\n")
	builder.WriteString("time_based=0\n")
	if opts.runtime > 0 {
		builder.WriteString("time_based=1\n")
		builder.WriteString("runtime=" + opts.runtime.String() + "\n")
	}
	builder.WriteString("size=" + strconv.FormatInt(perJob, 10) + "\n")
	for i, dir := range paths {
		fmt.Fprintf(&builder, "[job-%d]\nfilename=%s\n", i+1, filepath.Join(dir, "data.bin"))
	}
	if err := os.WriteFile(jobPath, []byte(builder.String()), 0600); err != nil {
		return sample{}, fmt.Errorf("write fio job: %w", err)
	}
	command := exec.Command(opts.fioPath, jobPath, "--output-format=json", "--output="+resultPath)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return sample{}, fmt.Errorf("run fio: %w", err)
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		return sample{}, fmt.Errorf("read fio result: %w", err)
	}
	var report fioReport
	if err := json.Unmarshal(data, &report); err != nil {
		return sample{}, fmt.Errorf("decode fio result: %w", err)
	}
	var totalBW, totalIOPS float64
	for _, job := range report.Jobs {
		result := job.Read
		if direction == "write" {
			result = job.Write
		}
		totalBW += result.BWBytes
		totalIOPS += result.IOPS
	}
	return sample{Filesystem: filesystem, IOSize: blockSize, Label: label, Direction: direction, BWBytes: totalBW, IOPS: totalIOPS}, nil
}

func writeReports(outputDir string, samples []sample) error {
	csvFile, err := os.Create(filepath.Join(outputDir, "summary.csv"))
	if err != nil {
		return fmt.Errorf("create CSV summary: %w", err)
	}
	writer := csv.NewWriter(csvFile)
	_ = writer.Write([]string{"filesystem", "io_size_bytes", "label", "direction", "bw_bytes_per_sec", "iops"})
	for _, value := range samples {
		_ = writer.Write([]string{value.Filesystem, strconv.FormatInt(value.IOSize, 10), value.Label, value.Direction,
			strconv.FormatFloat(value.BWBytes, 'f', 2, 64), strconv.FormatFloat(value.IOPS, 'f', 2, 64)})
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		_ = csvFile.Close()
		return fmt.Errorf("write CSV summary: %w", err)
	}
	if err := csvFile.Close(); err != nil {
		return fmt.Errorf("close CSV summary: %w", err)
	}
	var markdown strings.Builder
	markdown.WriteString("| I/O size | Direction | Raw 1 drive | Raw 3 drives | Raw 4 drives | FlexRAID | FlexRAID / raw 3-drive |\n")
	markdown.WriteString("|---|---|---:|---:|---:|---:|---:|\n")
	for _, block := range ioSizes {
		for _, direction := range []string{"read", "write"} {
			values := map[string]float64{}
			for _, value := range samples {
				if value.IOSize == block.bytes && value.Direction == direction {
					values[value.Filesystem] = value.BWBytes / (1024 * 1024)
				}
			}
			ratio := 0.0
			if values["raw-3drive"] > 0 {
				ratio = values["flexraid"] / values["raw-3drive"] * 100
			}
			fmt.Fprintf(&markdown, "| %s | %s | %.2f MiB/s | %.2f MiB/s | %.2f MiB/s | %.2f MiB/s | %.1f%% |\n",
				block.label, direction, values["raw-1drive"], values["raw-3drive"], values["raw-4drive"], values["flexraid"], ratio)
		}
	}
	if err := os.WriteFile(filepath.Join(outputDir, "summary.md"), []byte(markdown.String()), 0644); err != nil {
		return fmt.Errorf("write Markdown summary: %w", err)
	}
	fmt.Print(markdown.String())
	return nil
}
