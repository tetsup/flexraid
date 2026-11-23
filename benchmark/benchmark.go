package benchmark

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
	"syscall"
	"time"

	"github.com/tetsup/flexraid/management"
)

const fuseSuperMagic = 0x65735546

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

// Run executes the sequential storage benchmark against an already-running Worker.
func Run(args []string) (runErr error) {
	var opts options
	flags := flag.NewFlagSet("flexraid benchmark", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&opts.configPath, "config", "", "Worker YAML configuration")
	flags.StringVar(&opts.outputDir, "output", "./flexraid-bench-results", "result directory")
	flags.StringVar(&opts.fioPath, "fio", "fio", "fio executable path")
	flags.Int64Var(&opts.totalBytes, "size", 512<<20, "total logical bytes per test case")
	flags.DurationVar(&opts.runtime, "runtime", 0, "optional time-based fio runtime (0 runs the full size)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
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
	if err := management.ValidateDriveMappings(config.Disks); err != nil {
		return fmt.Errorf("validate configured physical drives: %w", err)
	}
	mountpoint, err := filepath.Abs(config.Mountpoint)
	if err != nil {
		return fmt.Errorf("resolve FlexRAID mountpoint: %w", err)
	}
	config.Mountpoint = mountpoint
	for index := range config.Disks {
		config.Disks[index].Root, err = filepath.Abs(config.Disks[index].Root)
		if err != nil {
			return fmt.Errorf("resolve disk %d root: %w", index+1, err)
		}
		config.Disks[index].Mountpoint, err = filepath.Abs(config.Disks[index].Mountpoint)
		if err != nil {
			return fmt.Errorf("resolve disk %d mountpoint: %w", index+1, err)
		}
	}
	if err := validatePhysicalRoots(config); err != nil {
		return err
	}
	benchmarkDisks, err := selectBenchmarkDisks(config.Disks)
	if err != nil {
		return err
	}
	for index, disk := range benchmarkDisks {
		fmt.Printf("Benchmark drive %d: WWN=%s root=%s\n", index+1, disk.WWN, disk.Root)
	}
	benchmarkConfig := config
	benchmarkConfig.Disks = benchmarkDisks
	var filesystem syscall.Statfs_t
	if err := syscall.Statfs(mountpoint, &filesystem); err != nil {
		return fmt.Errorf("inspect FlexRAID mountpoint %s: %w", mountpoint, err)
	}
	if uint64(filesystem.Type) != fuseSuperMagic {
		return fmt.Errorf("%s is not a mounted FUSE filesystem", mountpoint)
	}
	if err := validateOutputPath(opts.outputDir, config); err != nil {
		return err
	}
	if err := os.MkdirAll(opts.outputDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	workDirs, err := createWorkDirs(benchmarkConfig)
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := removeWorkDirs(workDirs); cleanupErr != nil {
			runErr = errors.Join(runErr, cleanupErr)
		}
	}()

	modes := []struct {
		name  string
		paths []string
	}{
		{name: "raw-1drive", paths: workDirs[:1]},
		{name: "raw-3drive", paths: workDirs[:3]},
		{name: "raw-4drive", paths: workDirs[:4]},
		{name: "flexraid", paths: workDirs[len(workDirs)-1:]},
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
				if direction == "read" {
					if err := removeModeFiles(mode.paths); err != nil {
						return fmt.Errorf("remove %s benchmark files: %w", mode.name, err)
					}
				}
			}
		}
	}
	if err := writeReports(opts.outputDir, samples); err != nil {
		return err
	}
	fmt.Printf("Results: %s\n", opts.outputDir)
	return nil
}

func selectBenchmarkDisks(disks []management.DriveConfig) ([]management.DriveConfig, error) {
	type candidate struct {
		drive management.DriveConfig
		free  uint64
	}
	candidates := make([]candidate, 0, len(disks))
	for index, disk := range disks {
		var stats syscall.Statfs_t
		if err := syscall.Statfs(disk.Root, &stats); err != nil {
			return nil, fmt.Errorf("get available capacity for disk %d (%s): %w", index+1, disk.WWN, err)
		}
		candidates = append(candidates, candidate{
			drive: disk,
			free:  uint64(stats.Bavail) * uint64(stats.Bsize),
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].free > candidates[j].free
	})
	selected := make([]management.DriveConfig, 4)
	for index := range selected {
		selected[index] = candidates[index].drive
	}
	return selected, nil
}

func validatePhysicalRoots(config management.Config) error {
	mounts, err := readMountpoints()
	if err != nil {
		return fmt.Errorf("read mounted filesystems: %w", err)
	}
	for index, disk := range config.Disks {
		mountpoint, err := filepath.EvalSymlinks(disk.Mountpoint)
		if err != nil {
			return fmt.Errorf("resolve physical mountpoint for disk %d: %w", index+1, err)
		}
		mountpoint = filepath.Clean(mountpoint)
		if _, ok := mounts[mountpoint]; !ok {
			return fmt.Errorf("physical mountpoint %s for disk %d is not mounted", mountpoint, index+1)
		}
		rootInfo, err := os.Stat(disk.Root)
		if err != nil {
			return fmt.Errorf("stat storage root for disk %d: %w", index+1, err)
		}
		mountInfo, err := os.Stat(mountpoint)
		if err != nil {
			return fmt.Errorf("stat physical mountpoint for disk %d: %w", index+1, err)
		}
		rootStat, rootOK := rootInfo.Sys().(*syscall.Stat_t)
		mountStat, mountOK := mountInfo.Sys().(*syscall.Stat_t)
		if !rootOK || !mountOK || rootStat.Dev != mountStat.Dev {
			return fmt.Errorf("storage root %s for disk %d is not on the configured physical mount %s", disk.Root, index+1, mountpoint)
		}
	}
	return nil
}

func readMountpoints() (map[string]struct{}, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	unescape := strings.NewReplacer(
		"\\040", " ",
		"\\011", "\t",
		"\\012", "\n",
		"\\134", "\\",
	)
	mounts := make(map[string]struct{})
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 4 {
			mounts[filepath.Clean(unescape.Replace(fields[4]))] = struct{}{}
		}
	}
	return mounts, nil
}

func validateOutputPath(output string, config management.Config) error {
	absolute, err := resolvePath(output)
	if err != nil {
		return fmt.Errorf("resolve output directory: %w", err)
	}
	protected := []string{config.Mountpoint}
	for _, disk := range config.Disks {
		protected = append(protected, disk.Root, disk.Mountpoint)
	}
	for _, path := range protected {
		root, err := resolvePath(path)
		if err != nil {
			return fmt.Errorf("resolve protected path %s: %w", path, err)
		}
		if pathsOverlap(absolute, root) {
			return fmt.Errorf("output directory %s must not overlap storage path %s", absolute, root)
		}
	}
	return nil
}

func resolvePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := absolute
	var suffix []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	for index := len(suffix) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, suffix[index])
	}
	return filepath.Clean(resolved), nil
}

func pathsOverlap(first, second string) bool {
	firstToSecond, err := filepath.Rel(first, second)
	if err != nil {
		return true
	}
	secondToFirst, err := filepath.Rel(second, first)
	if err != nil {
		return true
	}
	isWithin := func(relative string) bool {
		return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
	}
	return isWithin(firstToSecond) || isWithin(secondToFirst)
}

func createWorkDirs(config management.Config) ([]string, error) {
	paths := make([]string, 0, len(config.Disks)+1)
	for i, disk := range config.Disks {
		path, err := os.MkdirTemp(disk.Root, ".flexraid-bench-")
		if err != nil {
			cleanupErr := removeWorkDirs(paths)
			return nil, errors.Join(fmt.Errorf("create temporary directory on disk %d: %w", i+1, err), cleanupErr)
		}
		paths = append(paths, path)
	}
	fusePath, err := os.MkdirTemp(config.Mountpoint, ".flexraid-bench-")
	if err != nil {
		cleanupErr := removeWorkDirs(paths)
		return nil, errors.Join(fmt.Errorf("create temporary directory in FlexRAID mount: %w", err), cleanupErr)
	}
	paths = append(paths, fusePath)
	return paths, nil
}

func removeModeFiles(paths []string) error {
	var cleanupErr error
	for _, dir := range paths {
		if err := os.Remove(filepath.Join(dir, "data.bin")); err != nil && !os.IsNotExist(err) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove %s: %w", filepath.Join(dir, "data.bin"), err))
		}
	}
	return cleanupErr
}

func removeWorkDirs(paths []string) error {
	var cleanupErr error
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove benchmark directory %s: %w", path, err))
		}
	}
	return cleanupErr
}

func runCase(opts options, filesystem string, paths []string, blockSize int64, label, direction string) (sample, error) {
	jobCount := len(paths)
	perJob := opts.totalBytes / int64(jobCount)
	if perJob < blockSize {
		return sample{}, fmt.Errorf("per-job size %d is smaller than block size %d", perJob, blockSize)
	}
	jobPath := filepath.Join(opts.outputDir, filesystem+"-"+label+"-"+direction+".fio")
	resultPath := filepath.Join(opts.outputDir, filesystem+"-"+label+"-"+direction+".json")
	var builder strings.Builder
	builder.WriteString("[global]\n")
	builder.WriteString("ioengine=sync\n")
	builder.WriteString("direct=0\n")
	builder.WriteString("rw=" + direction + "\n")
	builder.WriteString("bs=" + strconv.FormatInt(blockSize, 10) + "\n")
	builder.WriteString("fallocate=none\n")
	builder.WriteString("group_reporting=1\n")
	builder.WriteString("end_fsync=1\n")
	builder.WriteString("invalidate=1\n")
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
	if err := writer.Write([]string{"filesystem", "io_size_bytes", "label", "direction", "bw_bytes_per_sec", "iops"}); err != nil {
		_ = csvFile.Close()
		return fmt.Errorf("write CSV header: %w", err)
	}
	for _, value := range samples {
		if err := writer.Write([]string{value.Filesystem, strconv.FormatInt(value.IOSize, 10), value.Label, value.Direction,
			strconv.FormatFloat(value.BWBytes, 'f', 2, 64), strconv.FormatFloat(value.IOPS, 'f', 2, 64)}); err != nil {
			_ = csvFile.Close()
			return fmt.Errorf("write CSV row: %w", err)
		}
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
