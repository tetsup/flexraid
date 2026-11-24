package benchmark

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tetsup/flexraid/management"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	fuseSuperMagic       = 0x65735546
	workerStartupTimeout = 90 * time.Second
	workerStopTimeout    = 10 * time.Second
)

var ioSizes = []struct {
	label string
	bytes int64
}{
	{"4KiB", 4 << 10},
	{"4KiB+1B", (4 << 10) + 1},
	{"64KiB", 64 << 10},
	{"6MiB+11KiB", (6 << 20) + (11 << 10)},
	{"6MiB+11KiB+1B", (6 << 20) + (11 << 10) + 1},
	{"64MiB", 64 << 20},
}

type options struct {
	configPath     string
	outputDir      string
	maxFiles       int
	referenceBytes int64
}

type sample struct {
	Filesystem string
	IOSize     int64
	Label      string
	Direction  string
	BWBytes    float64
	IOPS       float64
	Elapsed    time.Duration
	Bytes      int64
	Operations int64
}

type workerProcess struct {
	command    *exec.Cmd
	mountpoint string
	logPath    string
	done       chan struct{}
	waitErr    error
	waitMu     sync.Mutex
}

// Run measures an isolated temporary FlexRAID Worker and FUSE mount.
func Run(args []string) (runErr error) {
	if len(args) > 0 && args[0] == "raw" {
		return runRaw(args[1:])
	}
	var opts options
	flags := flag.NewFlagSet("flexraid benchmark", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&opts.configPath, "config", "", "Worker YAML configuration")
	flags.StringVar(&opts.outputDir, "output", "./flexraid-bench-results", "result directory")
	flags.IntVar(&opts.maxFiles, "max-files", 256, "maximum files per I/O size")
	flags.Int64Var(&opts.referenceBytes, "reference-bytes", 768<<20, "logical byte target used to derive per-size file counts")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if opts.configPath == "" {
		return errors.New("--config is required")
	}
	if opts.maxFiles <= 0 {
		return errors.New("--max-files must be positive")
	}
	if opts.referenceBytes <= 0 {
		return errors.New("--reference-bytes must be positive")
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
	config.Mountpoint, err = filepath.Abs(config.Mountpoint)
	if err != nil {
		return fmt.Errorf("resolve configured FUSE mountpoint: %w", err)
	}
	for index := range config.Disks {
		config.Disks[index].Root, err = filepath.Abs(config.Disks[index].Root)
		if err != nil {
			return fmt.Errorf("resolve disk %d storage root: %w", index+1, err)
		}
		config.Disks[index].Mountpoint, err = filepath.Abs(config.Disks[index].Mountpoint)
		if err != nil {
			return fmt.Errorf("resolve disk %d physical mountpoint: %w", index+1, err)
		}
	}
	if mounted, err := isFUSEMountpoint(config.Mountpoint); err != nil {
		return fmt.Errorf("inspect configured FUSE mountpoint: %w", err)
	} else if mounted {
		return fmt.Errorf("configured Worker is already mounted at %s; unmount it before running an isolated benchmark", config.Mountpoint)
	}
	if err := validatePhysicalMountpoints(config.Disks); err != nil {
		return err
	}
	if err := validateOutputPath(opts.outputDir, config); err != nil {
		return err
	}
	if err := os.MkdirAll(opts.outputDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	selected, err := selectBenchmarkDisks(config.Disks)
	if err != nil {
		return err
	}
	largestCaseFiles := benchmarkFileCount(ioSizes[len(ioSizes)-1].bytes, opts.referenceBytes, opts.maxFiles)
	physicalBytesPerDisk := int64(largestCaseFiles) * (172 << 20) / 4
	if err := validateBenchmarkCapacity(selected, physicalBytesPerDisk); err != nil {
		return err
	}
	for index, disk := range selected {
		fmt.Printf("Benchmark drive %d: WWN=%s mountpoint=%s\n", index+1, disk.WWN, disk.Mountpoint)
	}

	temporaryDir, err := os.MkdirTemp("", "flexraid-bench-")
	if err != nil {
		return fmt.Errorf("create benchmark workspace: %w", err)
	}
	var storageRoots []string
	var workDirs []string
	var worker *workerProcess
	defer func() {
		if cleanupErr := removeWorkDirs(workDirs); cleanupErr != nil {
			runErr = errors.Join(runErr, cleanupErr)
		}
		mountStillActive := false
		if worker != nil {
			if stopErr := worker.stop(); stopErr != nil {
				runErr = errors.Join(runErr, stopErr)
			}
			mounted, mountErr := isFUSEMountpoint(worker.mountpoint)
			if mountErr != nil || mounted {
				mountStillActive = true
				runErr = errors.Join(runErr, fmt.Errorf("temporary Worker mount may still be active; preserving temporary storage roots at %v and workspace %s (mount check error: %v)", storageRoots, temporaryDir, mountErr))
			}
		}
		if !mountStillActive {
			if cleanupErr := removeWorkDirs(storageRoots); cleanupErr != nil {
				runErr = errors.Join(runErr, cleanupErr)
			}
			if cleanupErr := os.RemoveAll(temporaryDir); cleanupErr != nil {
				runErr = errors.Join(runErr, fmt.Errorf("remove benchmark workspace %s: %w", temporaryDir, cleanupErr))
			}
		}
	}()

	storageRoots, err = createTemporaryStorageRoots(selected)
	if err != nil {
		return err
	}
	workerMountpoint := filepath.Join(temporaryDir, "mount")
	socketPath := filepath.Join(temporaryDir, "worker.sock")
	workerConfigPath := filepath.Join(temporaryDir, "worker.yaml")
	workerLogPath := filepath.Join(temporaryDir, "worker.log")
	if err := os.Mkdir(workerMountpoint, 0700); err != nil {
		return fmt.Errorf("create temporary Worker mountpoint: %w", err)
	}
	workerConfig, err := renderWorkerConfig(selected, storageRoots, workerMountpoint, socketPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(workerConfigPath, []byte(workerConfig), 0600); err != nil {
		return fmt.Errorf("write temporary Worker configuration: %w", err)
	}
	worker, err = startWorker(workerMountpoint, workerConfigPath, workerLogPath)
	if err != nil {
		return err
	}
	if err := worker.waitReady(workerStartupTimeout); err != nil {
		return err
	}
	if err := invokeTraceRPC(socketPath, "StartTrace"); err != nil {
		return fmt.Errorf("enable Worker trace: %w", err)
	}

	benchmarkConfig := config
	benchmarkConfig.Disks = selected
	benchmarkConfig.Mountpoint = workerMountpoint
	workDirs, err = createWorkDirs(benchmarkConfig)
	if err != nil {
		return err
	}
	var samples []sample
	for _, block := range ioSizes {
		fileCount := benchmarkFileCount(block.bytes, opts.referenceBytes, opts.maxFiles)
		fmt.Printf("Benchmark workload %s: files=%d logical_bytes=%d\n", block.label, fileCount, int64(fileCount)*block.bytes)
		var files []string
		for _, direction := range []string{"write", "overwrite", "read"} {
			fmt.Printf("Running %-10s %-8s %s ...\n", "flexraid", direction, block.label)
			measured, generated, err := runCase(opts, "flexraid", workDirs, block.bytes, block.label, direction, files, fileCount)
			if err != nil {
				return fmt.Errorf("flexraid %s %s: %w", direction, block.label, err)
			}
			if direction == "write" {
				files = generated
			}
			samples = append(samples, measured)
		}
		if err := removeBenchmarkFiles(files); err != nil {
			return fmt.Errorf("remove FlexRAID benchmark files: %w", err)
		}
	}
	if err := saveWorkerTrace(socketPath, opts.outputDir); err != nil {
		return fmt.Errorf("save Worker trace: %w", err)
	}
	if err := writeReports(opts.outputDir, samples); err != nil {
		return err
	}
	fmt.Printf("Results: %s\n", opts.outputDir)
	return nil
}

// runRaw measures one caller-selected directory without loading or consulting
// FlexRAID configuration. It deliberately has no dependency on Worker setup.
func runRaw(args []string) error {
	var opts options
	flags := flag.NewFlagSet("flexraid benchmark raw", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var directory string
	flags.StringVar(&directory, "directory", "", "directory on the physical filesystem to benchmark")
	flags.StringVar(&opts.outputDir, "output", "./flexraid-raw-bench-results", "result directory")
	flags.IntVar(&opts.maxFiles, "max-files", 256, "maximum files per I/O size")
	flags.Int64Var(&opts.referenceBytes, "reference-bytes", 768<<20, "logical byte target used to derive per-size file counts")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if directory == "" {
		return errors.New("--directory is required")
	}
	if opts.maxFiles <= 0 {
		return errors.New("--max-files must be positive")
	}
	if opts.referenceBytes <= 0 {
		return errors.New("--reference-bytes must be positive")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return fmt.Errorf("resolve benchmark directory: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return fmt.Errorf("stat benchmark directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("benchmark path %s is not a directory", absolute)
	}
	outputPath, err := resolvePath(opts.outputDir)
	if err != nil {
		return fmt.Errorf("resolve output directory: %w", err)
	}
	if pathsOverlap(outputPath, absolute) {
		return fmt.Errorf("output directory %s must not overlap benchmark directory %s", outputPath, absolute)
	}
	if err := os.MkdirAll(opts.outputDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	workDir, err := os.MkdirTemp(absolute, ".flexraid-raw-bench-")
	if err != nil {
		return fmt.Errorf("create temporary raw benchmark directory: %w", err)
	}
	defer os.RemoveAll(workDir)
	var samples []sample
	for _, block := range ioSizes {
		fileCount := benchmarkFileCount(block.bytes, opts.referenceBytes, opts.maxFiles)
		fmt.Printf("Benchmark workload %s: files=%d logical_bytes=%d\n", block.label, fileCount, int64(fileCount)*block.bytes)
		var files []string
		for _, direction := range []string{"write", "overwrite", "read"} {
			fmt.Printf("Running %-10s %-8s %s ...\n", "raw", direction, block.label)
			measured, generated, err := runCase(opts, "raw", []string{workDir}, block.bytes, block.label, direction, files, fileCount)
			if err != nil {
				return fmt.Errorf("raw %s %s: %w", direction, block.label, err)
			}
			if direction == "write" {
				files = generated
			}
			samples = append(samples, measured)
		}
		if err := removeBenchmarkFiles(files); err != nil {
			return fmt.Errorf("remove raw benchmark files: %w", err)
		}
	}
	if err := writeReports(opts.outputDir, samples); err != nil {
		return err
	}
	return nil
}

func validatePhysicalMountpoints(disks []management.DriveConfig) error {
	mounts, err := readMountpoints()
	if err != nil {
		return fmt.Errorf("read mounted filesystems: %w", err)
	}
	for index, disk := range disks {
		mountpoint, err := filepath.EvalSymlinks(disk.Mountpoint)
		if err != nil {
			return fmt.Errorf("resolve physical mountpoint for disk %d: %w", index+1, err)
		}
		mountpoint = filepath.Clean(mountpoint)
		if _, ok := mounts[mountpoint]; !ok {
			return fmt.Errorf("physical mountpoint %s for disk %d is not mounted", mountpoint, index+1)
		}
		mountInfo, err := os.Stat(mountpoint)
		if err != nil {
			return fmt.Errorf("stat physical mountpoint for disk %d: %w", index+1, err)
		}
		if !mountInfo.IsDir() {
			return fmt.Errorf("physical mountpoint %s for disk %d is not a directory", mountpoint, index+1)
		}
		rootInfo, err := os.Stat(disk.Root)
		if err == nil {
			if !rootInfo.IsDir() {
				return fmt.Errorf("configured Storage Root %s for disk %d is not a directory", disk.Root, index+1)
			}
			rootStat, rootOK := rootInfo.Sys().(*syscall.Stat_t)
			mountStat, mountOK := mountInfo.Sys().(*syscall.Stat_t)
			if !rootOK || !mountOK || rootStat.Dev != mountStat.Dev {
				return fmt.Errorf("configured Storage Root %s for disk %d is not on physical mountpoint %s", disk.Root, index+1, mountpoint)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat configured Storage Root for disk %d: %w", index+1, err)
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
		"\\011", "	",
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

func isFUSEMountpoint(path string) (bool, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return uint64(stats.Type) == fuseSuperMagic, nil
}

func driveCapacityPath(disk management.DriveConfig) (string, error) {
	info, err := os.Stat(disk.Root)
	if err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("configured Storage Root %s is not a directory", disk.Root)
		}
		return disk.Root, nil
	}
	if os.IsNotExist(err) {
		return disk.Mountpoint, nil
	}
	return "", fmt.Errorf("stat configured Storage Root %s: %w", disk.Root, err)
}

func selectBenchmarkDisks(disks []management.DriveConfig) ([]management.DriveConfig, error) {
	type candidate struct {
		drive management.DriveConfig
		free  uint64
	}
	candidates := make([]candidate, 0, len(disks))
	for index, disk := range disks {
		path, err := driveCapacityPath(disk)
		if err != nil {
			return nil, fmt.Errorf("get capacity path for disk %d (%s): %w", index+1, disk.WWN, err)
		}
		var stats syscall.Statfs_t
		if err := syscall.Statfs(path, &stats); err != nil {
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

func validateBenchmarkCapacity(disks []management.DriveConfig, perDiskRequired int64) error {
	if perDiskRequired <= 0 {
		return errors.New("required per-disk capacity must be positive")
	}
	required := uint64(perDiskRequired) + uint64(perDiskRequired/5) + uint64(256<<20)
	for index, disk := range disks {
		path, err := driveCapacityPath(disk)
		if err != nil {
			return fmt.Errorf("get capacity path for disk %d (%s): %w", index+1, disk.WWN, err)
		}
		var stats syscall.Statfs_t
		if err := syscall.Statfs(path, &stats); err != nil {
			return fmt.Errorf("get available capacity for disk %d (%s): %w", index+1, disk.WWN, err)
		}
		available := uint64(stats.Bavail) * uint64(stats.Bsize)
		if available < required {
			return fmt.Errorf("disk %d (%s) has %d bytes available; benchmark needs at least %d bytes for isolated FlexRAID data", index+1, disk.WWN, available, required)
		}
	}
	return nil
}

func createTemporaryStorageRoots(disks []management.DriveConfig) ([]string, error) {
	roots := make([]string, 0, len(disks))
	for index, disk := range disks {
		root, err := os.MkdirTemp(disk.Mountpoint, ".flexraid-bench-storage-")
		if err != nil {
			cleanupErr := removeWorkDirs(roots)
			return nil, errors.Join(fmt.Errorf("create temporary storage root on disk %d: %w", index+1, err), cleanupErr)
		}
		roots = append(roots, root)
	}
	return roots, nil
}

func renderWorkerConfig(disks []management.DriveConfig, roots []string, mountpoint, socketPath string) (string, error) {
	if len(disks) != len(roots) {
		return "", fmt.Errorf("disk count %d does not match temporary storage root count %d", len(disks), len(roots))
	}
	var builder strings.Builder
	builder.WriteString("disks:\n")
	for index, disk := range disks {
		for _, value := range []string{roots[index], disk.Mountpoint, disk.WWN} {
			if strings.ContainsAny(value, "\r\n") {
				return "", fmt.Errorf("disk %d configuration contains a newline", index+1)
			}
		}
		fmt.Fprintf(&builder, "  - root: %s\n", roots[index])
		fmt.Fprintf(&builder, "    mountpoint: %s\n", disk.Mountpoint)
		fmt.Fprintf(&builder, "    wwn: %s\n", disk.WWN)
	}
	if strings.ContainsAny(mountpoint+socketPath, "\r\n") {
		return "", errors.New("temporary Worker paths contain a newline")
	}
	fmt.Fprintf(&builder, "mountpoint: %s\n", mountpoint)
	fmt.Fprintf(&builder, "grpc_socket_path: %s\n", socketPath)
	builder.WriteString("grpc_enabled: false\n")
	return builder.String(), nil
}

func startWorker(mountpoint, configPath, logPath string) (*workerProcess, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve Worker executable: %w", err)
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("create temporary Worker log: %w", err)
	}
	command := exec.Command(executable, "--config", configPath)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("start temporary Worker: %w", err)
	}
	process := &workerProcess{
		command:    command,
		mountpoint: mountpoint,
		logPath:    logPath,
		done:       make(chan struct{}),
	}
	go func() {
		err := command.Wait()
		_ = logFile.Close()
		process.waitMu.Lock()
		process.waitErr = err
		process.waitMu.Unlock()
		close(process.done)
	}()
	return process, nil
}

func (worker *workerProcess) waitReady(timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if mounted, err := isFUSEMountpoint(worker.mountpoint); err == nil && mounted {
			if _, err := os.ReadDir(worker.mountpoint); err == nil {
				return nil
			}
		}
		select {
		case <-worker.done:
			return fmt.Errorf("temporary Worker exited before becoming ready: %s", worker.logContents())
		case <-deadline.C:
			return fmt.Errorf("temporary Worker did not become ready within %s: %s", timeout, worker.logContents())
		case <-ticker.C:
		}
	}
}

func (worker *workerProcess) stop() error {
	var cleanupErr error
	if mounted, err := isFUSEMountpoint(worker.mountpoint); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("inspect temporary Worker mount: %w", err))
	} else if mounted {
		if err := unmount(worker.mountpoint); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("unmount temporary Worker: %w", err))
			_ = worker.command.Process.Kill()
		}
	} else {
		select {
		case <-worker.done:
		default:
			_ = worker.command.Process.Signal(syscall.SIGTERM)
		}
	}

	timer := time.NewTimer(workerStopTimeout)
	defer timer.Stop()
	select {
	case <-worker.done:
	case <-timer.C:
		_ = worker.command.Process.Kill()
		<-worker.done
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("temporary Worker did not stop within %s", workerStopTimeout))
	}
	worker.waitMu.Lock()
	waitErr := worker.waitErr
	worker.waitMu.Unlock()
	if waitErr != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("temporary Worker exited with error: %w: %s", waitErr, worker.logContents()))
	}
	return cleanupErr
}

func (worker *workerProcess) logContents() string {
	data, err := os.ReadFile(worker.logPath)
	if err != nil {
		return fmt.Sprintf("unable to read Worker log: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func unmount(mountpoint string) error {
	commands := [][]string{
		{"fusermount3", "-u", mountpoint},
		{"umount", mountpoint},
		{"fusermount3", "-u", "-z", mountpoint},
		{"umount", "-l", mountpoint},
	}
	var unmountErr error
	for _, args := range commands {
		if err := exec.Command(args[0], args[1:]...).Run(); err == nil {
			return nil
		} else {
			unmountErr = errors.Join(unmountErr, fmt.Errorf("%s: %w", strings.Join(args, " "), err))
		}
	}
	return unmountErr
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
	fusePath, err := os.MkdirTemp(config.Mountpoint, ".flexraid-bench-")
	if err != nil {
		return nil, fmt.Errorf("create benchmark directory in temporary FUSE mount: %w", err)
	}
	return []string{fusePath}, nil
}

func removeBenchmarkFiles(paths []string) error {
	var cleanupErr error
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove benchmark file %s: %w", path, err))
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

func runCase(opts options, filesystem string, paths []string, blockSize int64, label, direction string, files []string, fileCount int) (sample, []string, error) {
	if err := dropCaches(); err != nil {
		return sample{}, nil, fmt.Errorf("drop filesystem caches before %s: %w", direction, err)
	}
	fmt.Printf("Cache cleared before %s %s (sync + /proc/sys/vm/drop_caches=3)\n", filesystem, direction)
	var bytes, operations int64
	var elapsed time.Duration
	var generated []string
	var err error
	switch direction {
	case "write":
		generated, bytes, operations, elapsed, err = createBenchmarkFiles(paths, blockSize, fileCount)
	case "overwrite":
		bytes, operations, elapsed, err = measureOverwrite(files, blockSize)
	case "read":
		bytes, operations, elapsed, err = measureColdRead(files, blockSize)
	default:
		return sample{}, nil, fmt.Errorf("unknown benchmark direction %q", direction)
	}
	if err != nil {
		return sample{}, nil, err
	}
	if elapsed <= 0 {
		return sample{}, nil, errors.New("measurement elapsed time is zero")
	}
	result := sample{Filesystem: filesystem, IOSize: blockSize, Label: label, Direction: direction, Bytes: bytes, Operations: operations, Elapsed: elapsed}
	result.BWBytes = float64(bytes) / elapsed.Seconds()
	result.IOPS = float64(operations) / elapsed.Seconds()
	measuredFileCount := len(generated)
	if direction != "write" {
		measuredFileCount = len(files)
	}
	fmt.Printf("Completed %-8s %-10s %-9s elapsed=%s operations=%d bytes=%d (%.2f MiB) bandwidth=%.2f MiB/s IOPS=%.2f files=%d\n", direction, filesystem, label, elapsed.Round(time.Millisecond), operations, bytes, float64(bytes)/(1024*1024), result.BWBytes/(1024*1024), result.IOPS, measuredFileCount)
	return result, generated, nil
}

func benchmarkFileCount(blockSize, referenceBytes int64, maxFiles int) int {
	count := int(referenceBytes / blockSize)
	if remainder := referenceBytes % blockSize; remainder > 0 && blockSize-remainder < blockSize/2 {
		count++
	}
	if count < 1 {
		count = 1
	}
	if count > maxFiles {
		count = maxFiles
	}
	return count
}

func createBenchmarkFiles(paths []string, blockSize int64, fileCount int) ([]string, int64, int64, time.Duration, error) {
	if len(paths) == 0 {
		return nil, 0, 0, 0, errors.New("no benchmark directories configured")
	}
	if fileCount <= 0 {
		return nil, 0, 0, 0, errors.New("file count must be positive")
	}
	payload := make([]byte, blockSize)
	if _, err := rand.Read(payload); err != nil {
		return nil, 0, 0, 0, fmt.Errorf("generate benchmark payload: %w", err)
	}
	start := time.Now()
	var files []string
	var totalBytesWritten, totalOperations int64
	for index := 0; index < fileCount; index++ {
		dir := paths[index%len(paths)]
		file, err := os.CreateTemp(dir, "bench-*.dat")
		if err != nil {
			return files, totalBytesWritten, totalOperations, time.Since(start), fmt.Errorf("create new benchmark file in %s: %w", dir, err)
		}
		path := file.Name()
		written, writeErr := writeFull(file, payload)
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr != nil {
			_ = os.Remove(path)
			return files, totalBytesWritten + int64(written), totalOperations, time.Since(start), errors.Join(fmt.Errorf("write new benchmark file %s: %w", path, writeErr), closeErr)
		}
		if closeErr != nil {
			_ = os.Remove(path)
			return files, totalBytesWritten, totalOperations, time.Since(start), fmt.Errorf("close new benchmark file %s: %w", path, closeErr)
		}
		files = append(files, path)
		totalBytesWritten += int64(written)
		totalOperations++
	}
	return files, totalBytesWritten, totalOperations, time.Since(start), nil
}

func writeFull(file *os.File, payload []byte) (int, error) {
	total := 0
	for total < len(payload) {
		n, err := file.Write(payload[total:])
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func measureOverwrite(paths []string, blockSize int64) (int64, int64, time.Duration, error) {
	if len(paths) == 0 {
		return 0, 0, 0, errors.New("overwrite phase has no files")
	}
	payload := make([]byte, blockSize)
	if _, err := rand.Read(payload); err != nil {
		return 0, 0, 0, fmt.Errorf("generate overwrite payload: %w", err)
	}
	start := time.Now()
	var totalBytes, totalOperations int64
	for _, path := range paths {
		file, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return totalBytes, totalOperations, time.Since(start), fmt.Errorf("open existing file %s for overwrite: %w", path, err)
		}
		written, writeErr := writeFull(file, payload)
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr != nil {
			return totalBytes + int64(written), totalOperations, time.Since(start), errors.Join(fmt.Errorf("overwrite %s: %w", path, writeErr), closeErr)
		}
		if closeErr != nil {
			return totalBytes + int64(written), totalOperations, time.Since(start), fmt.Errorf("close overwritten file %s: %w", path, closeErr)
		}
		totalBytes += int64(written)
		totalOperations++
	}
	return totalBytes, totalOperations, time.Since(start), nil
}

func measureColdRead(paths []string, blockSize int64) (int64, int64, time.Duration, error) {
	if len(paths) == 0 {
		return 0, 0, 0, errors.New("read phase has no files")
	}
	buffer := make([]byte, blockSize)
	start := time.Now()
	var totalBytes, totalOperations int64
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return totalBytes, totalOperations, time.Since(start), fmt.Errorf("open benchmark file %s for read: %w", path, err)
		}
		n, readErr := io.ReadFull(file, buffer)
		closeErr := file.Close()
		if readErr != nil {
			return totalBytes + int64(n), totalOperations, time.Since(start), fmt.Errorf("read benchmark file %s: %w", path, readErr)
		}
		if closeErr != nil {
			return totalBytes + int64(n), totalOperations, time.Since(start), fmt.Errorf("close benchmark file %s after read: %w", path, closeErr)
		}
		totalBytes += int64(n)
		totalOperations++
	}
	return totalBytes, totalOperations, time.Since(start), nil
}
func dropCaches() error {
	if err := exec.Command("sync").Run(); err != nil {
		return fmt.Errorf("sync written benchmark data: %w", err)
	}
	if err := os.WriteFile("/proc/sys/vm/drop_caches", []byte("3\n"), 0); err != nil {
		return fmt.Errorf("write /proc/sys/vm/drop_caches: %w", err)
	}
	return nil
}

func writeReports(outputDir string, samples []sample) error {
	csvFile, err := os.Create(filepath.Join(outputDir, "summary.csv"))
	if err != nil {
		return fmt.Errorf("create CSV summary: %w", err)
	}
	writer := csv.NewWriter(csvFile)
	if err := writer.Write([]string{"filesystem", "io_size_bytes", "label", "direction", "elapsed_seconds", "operations", "bytes", "bw_bytes_per_sec", "iops"}); err != nil {
		_ = csvFile.Close()
		return fmt.Errorf("write CSV header: %w", err)
	}
	for _, value := range samples {
		if err := writer.Write([]string{value.Filesystem, strconv.FormatInt(value.IOSize, 10), value.Label, value.Direction,
			strconv.FormatFloat(value.Elapsed.Seconds(), 'f', 3, 64), strconv.FormatInt(value.Operations, 10), strconv.FormatInt(value.Bytes, 10),
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
	markdown.WriteString("| Filesystem | I/O size | Direction | Elapsed | Operations | Bytes | Bandwidth |\n")
	markdown.WriteString("|---|---|---:|---:|---:|---:|---:|\n")
	for _, value := range samples {
		fmt.Fprintf(&markdown, "| %s | %s | %s | %s | %d | %d | %.2f MiB/s |\n",
			value.Filesystem, value.Label, value.Direction, value.Elapsed.Round(time.Millisecond), value.Operations, value.Bytes, value.BWBytes/(1024*1024))
	}
	if err := os.WriteFile(filepath.Join(outputDir, "summary.md"), []byte(markdown.String()), 0644); err != nil {
		return fmt.Errorf("write Markdown summary: %w", err)
	}
	fmt.Print(markdown.String())
	return nil
}

func invokeTraceRPC(socketPath, method string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	connection, err := grpc.NewClient("passthrough:///worker",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		}))
	if err != nil {
		return err
	}
	defer connection.Close()
	response := new(structpb.Struct)
	return connection.Invoke(ctx, "/flexraid.worker.v1.WorkerService/"+method, &emptypb.Empty{}, response)
}

func saveWorkerTrace(socketPath, outputDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := grpc.NewClient("passthrough:///worker",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		}))
	if err != nil {
		return err
	}
	defer connection.Close()
	stopResponse := new(structpb.Struct)
	if err := connection.Invoke(ctx, "/flexraid.worker.v1.WorkerService/StopTrace", &emptypb.Empty{}, stopResponse); err != nil {
		return err
	}
	traceResponse := new(structpb.Struct)
	if err := connection.Invoke(ctx, "/flexraid.worker.v1.WorkerService/GetTrace", &emptypb.Empty{}, traceResponse); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(traceResponse.AsMap(), "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "trace.json"), encoded, 0644); err != nil {
		return err
	}
	return nil
}
