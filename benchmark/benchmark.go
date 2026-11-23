package benchmark

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tetsup/flexraid/management"
)

const (
	fuseSuperMagic       = 0x65735546
	workerStartupTimeout = 90 * time.Second
	workerStopTimeout    = 10 * time.Second
	defaultCaseDuration  = 20 * time.Second
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
	duration   time.Duration
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

type workerProcess struct {
	command    *exec.Cmd
	mountpoint string
	logPath    string
	done       chan struct{}
	waitErr    error
	waitMu     sync.Mutex
}

// Run measures raw physical storage and an isolated temporary FlexRAID Worker.
func Run(args []string) (runErr error) {
	var opts options
	flags := flag.NewFlagSet("flexraid benchmark", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&opts.configPath, "config", "", "Worker YAML configuration")
	flags.StringVar(&opts.outputDir, "output", "./flexraid-bench-results", "result directory")
	flags.StringVar(&opts.fioPath, "fio", "fio", "fio executable path")
	flags.Int64Var(&opts.totalBytes, "size", 512<<20, "total logical bytes per test case")
	var durationSeconds int
	flags.IntVar(&durationSeconds, "duration-seconds", int(defaultCaseDuration/time.Second), "maximum duration of each I/O measurement in seconds")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if opts.configPath == "" {
		return errors.New("--config is required")
	}
	if durationSeconds <= 0 {
		return errors.New("--duration-seconds must be positive")
	}
	opts.duration = time.Duration(durationSeconds) * time.Second
	if opts.totalBytes <= 0 {
		return errors.New("--size must be positive")
	}
	minimumSize := ioSizes[len(ioSizes)-1].bytes * 4
	if opts.totalBytes < minimumSize {
		return fmt.Errorf("--size must be at least %d bytes so every raw parallel job can issue a full 64 MiB request", minimumSize)
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
	if err := validateBenchmarkCapacity(selected, opts.totalBytes); err != nil {
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

	benchmarkConfig := config
	benchmarkConfig.Disks = selected
	benchmarkConfig.Mountpoint = workerMountpoint
	workDirs, err = createWorkDirs(benchmarkConfig)
	if err != nil {
		return err
	}
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
		if opts.totalBytes < block.bytes*4 {
			return fmt.Errorf("test size %d is too small for %s across four raw drives", opts.totalBytes, block.label)
		}
		for _, mode := range modes {
			for _, direction := range []string{"write", "overwrite", "read"} {
				fmt.Printf("Running %-10s %-5s %s ...\n", mode.name, direction, block.label)
				measured, err := runCase(opts, mode.name, mode.paths, block.bytes, block.label, direction)
				if err != nil {
					return fmt.Errorf("%s %s %s: %w", mode.name, direction, block.label, err)
				}
				samples = append(samples, measured)
			}
			if err := removeModeFiles(mode.paths); err != nil {
				return fmt.Errorf("remove %s benchmark files: %w", mode.name, err)
			}
		}
	}
	if err := writeReports(opts.outputDir, samples); err != nil {
		return err
	}
	fmt.Printf("Results: %s\n", opts.outputDir)
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

func validateBenchmarkCapacity(disks []management.DriveConfig, totalBytes int64) error {
	// Only one test dataset per mode is retained at a time. The largest
	// physical allocation is the raw single-drive dataset, plus headroom.
	maxUint := ^uint64(0)
	size := uint64(totalBytes)
	margin := uint64(64 << 20)
	if size > maxUint-margin {
		return fmt.Errorf("benchmark size %d is too large for capacity calculation", totalBytes)
	}
	required := size + margin
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
	paths := make([]string, 0, len(config.Disks)+1)
	for i, disk := range config.Disks {
		path, err := os.MkdirTemp(disk.Mountpoint, ".flexraid-bench-raw-")
		if err != nil {
			cleanupErr := removeWorkDirs(paths)
			return nil, errors.Join(fmt.Errorf("create raw benchmark directory on disk %d: %w", i+1, err), cleanupErr)
		}
		paths = append(paths, path)
	}
	fusePath, err := os.MkdirTemp(config.Mountpoint, ".flexraid-bench-")
	if err != nil {
		cleanupErr := removeWorkDirs(paths)
		return nil, errors.Join(fmt.Errorf("create benchmark directory in temporary FUSE mount: %w", err), cleanupErr)
	}
	paths = append(paths, fusePath)
	return paths, nil
}

func removeModeFiles(paths []string) error {
	var cleanupErr error
	for _, dir := range paths {
		path := filepath.Join(dir, "data.bin")
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove %s: %w", path, err))
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
	if direction == "read" || direction == "overwrite" {
		if err := dropCaches(); err != nil {
			return sample{}, fmt.Errorf("drop filesystem caches before cold read: %w", err)
		}
		var bw, iops float64
		var err error
		if direction == "overwrite" {
			bw, iops, err = measureOverwrite(paths, blockSize, opts.duration)
		} else {
			bw, iops, err = measureColdRead(paths, blockSize, opts.duration)
		}
		if err != nil {
			return sample{}, err
		}
		return sample{Filesystem: filesystem, IOSize: blockSize, Label: label, Direction: direction, BWBytes: bw, IOPS: iops}, nil
	}

	jobPath := filepath.Join(opts.outputDir, filesystem+"-"+label+"-"+direction+".fio")
	resultPath := filepath.Join(opts.outputDir, filesystem+"-"+label+"-"+direction+".json")
	jobConfig := buildFioJob(paths, blockSize, perJob, direction, opts.duration)
	if err := os.WriteFile(jobPath, []byte(jobConfig), 0600); err != nil {
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
		totalBW += job.Write.BWBytes
		totalIOPS += job.Write.IOPS
	}
	return sample{Filesystem: filesystem, IOSize: blockSize, Label: label, Direction: direction, BWBytes: totalBW, IOPS: totalIOPS}, nil
}

func buildFioJob(paths []string, blockSize, perJob int64, direction string, duration time.Duration) string {
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
	builder.WriteString("runtime=" + strconv.Itoa(int(duration/time.Second)) + "\n")
	if direction == "write" {
		// Keep writing for the full measurement window, reusing this bounded
		// file region if necessary. The subsequent read is deliberately one pass.
		builder.WriteString("time_based=1\n")
	}
	builder.WriteString("size=" + strconv.FormatInt(perJob, 10) + "\n")
	for i, dir := range paths {
		fmt.Fprintf(&builder, "[job-%d]\nfilename=%s\n", i+1, filepath.Join(dir, "data.bin"))
	}
	return builder.String()
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

func measureColdRead(paths []string, blockSize int64, limit time.Duration) (float64, float64, error) {
	start := time.Now()
	deadline := start.Add(limit)
	var totalBytes atomic.Int64
	var totalOps atomic.Int64
	var wg sync.WaitGroup
	errCh := make(chan error, len(paths))
	for _, dir := range paths {
		path := filepath.Join(dir, "data.bin")
		wg.Add(1)
		go func() {
			defer wg.Done()
			file, err := os.Open(path)
			if err != nil {
				errCh <- fmt.Errorf("open %s: %w", path, err)
				return
			}
			defer file.Close()
			buffer := make([]byte, blockSize)
			for time.Now().Before(deadline) {
				n, readErr := file.Read(buffer)
				if n > 0 {
					totalBytes.Add(int64(n))
					totalOps.Add(1)
				}
				if readErr != nil {
					if errors.Is(readErr, io.EOF) {
						return
					}
					errCh <- fmt.Errorf("read %s: %w", path, readErr)
					return
				}
				if n == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return 0, 0, err
		}
	}
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		return 0, 0, errors.New("cold-read measurement elapsed time is zero")
	}
	return float64(totalBytes.Load()) / elapsed, float64(totalOps.Load()) / elapsed, nil
}

func measureOverwrite(paths []string, blockSize int64, limit time.Duration) (float64, float64, error) {
	start := time.Now()
	deadline := start.Add(limit)
	var totalBytes atomic.Int64
	var totalOps atomic.Int64
	var wg sync.WaitGroup
	errCh := make(chan error, len(paths))
	for _, dir := range paths {
		path := filepath.Join(dir, "data.bin")
		wg.Add(1)
		go func() {
			defer wg.Done()
			file, err := os.OpenFile(path, os.O_WRONLY, 0)
			if err != nil {
				errCh <- fmt.Errorf("open existing file %s for overwrite: %w", path, err)
				return
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil {
				errCh <- fmt.Errorf("stat existing file %s: %w", path, err)
				return
			}
			buffer := make([]byte, blockSize)
			var offset int64
			for offset < info.Size() && time.Now().Before(deadline) {
				remaining := info.Size() - offset
				chunk := buffer
				if remaining < int64(len(chunk)) {
					chunk = chunk[:remaining]
				}
				n, writeErr := file.Write(chunk)
				if n > 0 {
					offset += int64(n)
					totalBytes.Add(int64(n))
					totalOps.Add(1)
				}
				if writeErr != nil {
					errCh <- fmt.Errorf("overwrite %s: %w", path, writeErr)
					return
				}
				if n == 0 {
					errCh <- fmt.Errorf("overwrite %s: %w", path, io.ErrShortWrite)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return 0, 0, err
		}
	}
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		return 0, 0, errors.New("overwrite measurement elapsed time is zero")
	}
	return float64(totalBytes.Load()) / elapsed, float64(totalOps.Load()) / elapsed, nil
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
		for _, direction := range []string{"read", "write", "overwrite"} {
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
