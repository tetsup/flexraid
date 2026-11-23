package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type mode string

const (
	modeOff    mode = "off"
	modeIdle   mode = "on-idle"
	modeActive mode = "on-active"
)

type result struct {
	Mode                mode          `json:"mode"`
	Repetition          int           `json:"repetition"`
	Bytes               int64         `json:"bytes"`
	AverageWriteLatency time.Duration `json:"average_write_latency_ns"`
	AverageReadLatency  time.Duration `json:"average_read_latency_ns"`
	WriteMiBPerSec      float64       `json:"write_mib_per_sec"`
	ReadMiBPerSec       float64       `json:"read_mib_per_sec"`
	WriteSHA256         string        `json:"write_sha256"`
	ReadSHA256          string        `json:"read_sha256"`
	IntegrityVerified   bool          `json:"integrity_verified"`
	Duration            time.Duration `json:"duration_ns"`
	Workers             int           `json:"workers"`
	WriteOps            uint64        `json:"write_ops"`
	ReadOps             uint64        `json:"read_ops"`
	MetadataOps         uint64        `json:"metadata_ops"`
	SyncOps             uint64        `json:"sync_ops"`
	IOPS                float64       `json:"iops"`
	WriteLatencyP50     time.Duration `json:"write_latency_p50_ns"`
	WriteLatencyP95     time.Duration `json:"write_latency_p95_ns"`
	WriteLatencyP99     time.Duration `json:"write_latency_p99_ns"`
	ReadLatencyP50      time.Duration `json:"read_latency_p50_ns"`
	ReadLatencyP95      time.Duration `json:"read_latency_p95_ns"`
	ReadLatencyP99      time.Duration `json:"read_latency_p99_ns"`
	MetadataLatencyP50  time.Duration `json:"metadata_latency_p50_ns"`
	MetadataLatencyP95  time.Duration `json:"metadata_latency_p95_ns"`
	MetadataLatencyP99  time.Duration `json:"metadata_latency_p99_ns"`
	SyncLatencyP50      time.Duration `json:"sync_latency_p50_ns"`
	SyncLatencyP95      time.Duration `json:"sync_latency_p95_ns"`
	SyncLatencyP99      time.Duration `json:"sync_latency_p99_ns"`
	RPCCalls            uint64        `json:"rpc_calls"`
	RPCErrors           uint64        `json:"rpc_errors"`
	RPCSuccessRatePct   float64       `json:"rpc_success_rate_pct"`
	AverageRPCLatency   time.Duration `json:"average_rpc_latency_ns"`
	RPCLatencyP50       time.Duration `json:"rpc_latency_p50_ns"`
	RPCLatencyP95       time.Duration `json:"rpc_latency_p95_ns"`
	RPCLatencyP99       time.Duration `json:"rpc_latency_p99_ns"`
}

type options struct {
	configPath     string
	workerPath     string
	size           int64
	blockSize      int
	rpcEvery       time.Duration
	reportPath     string
	startup        time.Duration
	repetitions    int
	workers        int
	duration       time.Duration
	warmupDuration time.Duration
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "flexraid-stress:", err)
		os.Exit(1)
	}
}

func run() error {
	var opts options
	flag.StringVar(&opts.configPath, "config", "", "isolated test Worker config template")
	flag.StringVar(&opts.workerPath, "worker", "./bin/flexraid", "path to the flexraid Worker executable")
	flag.Int64Var(&opts.size, "size", 256<<20, "shared FUSE working-set size in bytes")
	flag.IntVar(&opts.blockSize, "block-size", 64<<10, "random-offset I/O block size in bytes")
	flag.DurationVar(&opts.rpcEvery, "rpc-interval", 100*time.Millisecond, "base interval for concurrent monitoring RPCs")
	flag.StringVar(&opts.reportPath, "report", "flexraid-stress-report.json", "JSON report path")
	flag.DurationVar(&opts.startup, "startup-timeout", 90*time.Second, "Worker startup timeout")
	flag.IntVar(&opts.repetitions, "repetitions", 2, "number of alternating comparison rounds")
	flag.IntVar(&opts.workers, "workers", 4, "number of concurrent FUSE file clients")
	flag.DurationVar(&opts.duration, "duration", 10*time.Second, "measured concurrent workload duration")
	flag.DurationVar(&opts.warmupDuration, "warmup", 2*time.Second, "warm-up duration per comparison condition")
	flag.Parse()

	if opts.configPath == "" {
		return errors.New("--config is required; use a dedicated test configuration with test-only storage roots")
	}
	if opts.size <= 0 || opts.blockSize <= 0 || opts.rpcEvery <= 0 || opts.repetitions <= 0 ||
		opts.workers < 2 || opts.duration <= 0 || opts.warmupDuration < 0 || opts.size < int64(opts.workers*opts.blockSize) {
		return errors.New("size, block-size, rpc-interval, duration, and repetitions must be positive; workers >= 2; warmup >= 0; size >= workers*block-size")
	}
	if _, err := os.Stat(opts.workerPath); err != nil {
		return fmt.Errorf("Worker executable: %w", err)
	}
	template, err := os.ReadFile(opts.configPath)
	if err != nil {
		return fmt.Errorf("read config template: %w", err)
	}

	var samples []result
	for repetition := 1; repetition <= opts.repetitions; repetition++ {
		order := []mode{modeOff, modeIdle, modeActive}
		if repetition%2 == 0 {
			order = []mode{modeActive, modeIdle, modeOff}
		}
		for _, selected := range order {
			fmt.Printf("Running %s (round %d/%d)...\n", selected, repetition, opts.repetitions)
			measured, err := runMode(opts, string(template), selected)
			if err != nil {
				return fmt.Errorf("%s round %d: %w", selected, repetition, err)
			}
			measured.Repetition = repetition
			samples = append(samples, measured)
			printResult(measured)
		}
	}
	summary := summarizeResults(samples)
	report := struct {
		CreatedAt time.Time `json:"created_at"`
		Samples   []result  `json:"samples"`
		Summary   []result  `json:"median_summary"`
	}{CreatedAt: time.Now().UTC(), Samples: samples, Summary: summary}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	if err := os.WriteFile(opts.reportPath, append(encoded, '\n'), 0600); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	fmt.Printf("\n%s\n", markdownReport(summary))
	fmt.Printf("JSON report: %s\n", opts.reportPath)
	return nil
}

func runMode(opts options, template string, selected mode) (_ result, runErr error) {
	result := result{Mode: selected, Bytes: opts.size}
	workDir, err := os.MkdirTemp("", "flexraid-stress-"+string(selected)+"-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(workDir)

	configPath := filepath.Join(workDir, "worker.yaml")
	mountpoint := filepath.Join(workDir, "mount")
	socketPath := filepath.Join(workDir, "worker.sock")
	roots, err := createStorageRoots(template)
	if err != nil {
		return result, err
	}
	storageCleanupSafe := true
	defer func() {
		if !storageCleanupSafe {
			return
		}
		for _, root := range roots {
			if err := os.RemoveAll(root); err != nil && runErr == nil {
				runErr = fmt.Errorf("remove isolated storage root %s: %w", root, err)
			}
		}
	}()

	generated, err := renderConfig(template, roots, mountpoint, socketPath, selected != modeOff)
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(configPath, []byte(generated), 0600); err != nil {
		return result, fmt.Errorf("write generated config: %w", err)
	}
	if err := os.MkdirAll(mountpoint, 0700); err != nil {
		return result, err
	}

	logFile, err := os.Create(filepath.Join(workDir, "worker.log"))
	if err != nil {
		return result, err
	}
	defer logFile.Close()
	command := exec.Command(opts.workerPath, "--config", configPath)
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		return result, fmt.Errorf("start Worker: %w", err)
	}
	waitDone := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(waitDone)
	}()
	defer func() {
		if isMounted(mountpoint) {
			if err := unmount(mountpoint); err != nil {
				storageCleanupSafe = false
				if runErr == nil {
					runErr = fmt.Errorf("unmount test filesystem: %w", err)
				}
			}
		}
		select {
		case <-waitDone:
		case <-time.After(10 * time.Second):
			_ = command.Process.Kill()
			<-waitDone
		}
		if runErr != nil {
			_, _ = logFile.Seek(0, io.SeekStart)
			logs, _ := io.ReadAll(logFile)
			runErr = fmt.Errorf("%w (Worker log: %s)", runErr, strings.TrimSpace(string(logs)))
		}
	}()

	if err := waitForWorker(command, waitDone, mountpoint, socketPath, selected != modeOff, opts.startup); err != nil {
		return result, err
	}
	if err := runConcurrentWorkload(mountpoint, socketPath, opts, &result, selected == modeActive); err != nil {
		return result, err
	}
	return result, nil
}

func createStorageRoots(template string) ([]string, error) {
	var roots []string
	// Root directories must remain under their configured physical mountpoints.
	lines := strings.Split(template, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "- root:") {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			trimmed := strings.TrimSpace(lines[j])
			if strings.HasPrefix(trimmed, "- root:") {
				break
			}
			if strings.HasPrefix(trimmed, "mountpoint:") {
				physicalMount := unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "mountpoint:")))
				if physicalMount == "" {
					return nil, errors.New("empty physical disk mountpoint in config")
				}
				if !isMounted(physicalMount) {
					for _, created := range roots {
						_ = os.RemoveAll(created)
					}
					return nil, fmt.Errorf("physical mountpoint %s is not mounted; refusing to create benchmark data on the host filesystem", physicalMount)
				}
				root, err := os.MkdirTemp(physicalMount, ".flexraid-stress-")
				if err != nil {
					for _, created := range roots {
						_ = os.RemoveAll(created)
					}
					return nil, fmt.Errorf("create isolated storage root under %s: %w", physicalMount, err)
				}
				roots = append(roots, root)
				break
			}
		}
	}
	if len(roots) < 4 {
		for _, root := range roots {
			_ = os.RemoveAll(root)
		}
		return nil, fmt.Errorf("config has %d disk roots; at least 4 are required", len(roots))
	}
	return roots, nil
}

func renderConfig(template string, roots []string, mountpoint, socket string, grpcEnabled bool) (string, error) {
	lines := strings.Split(template, "\n")
	rootIndex := 0
	foundMount, foundGRPC := false, false
	var output []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		switch {
		case strings.HasPrefix(trimmed, "- root:"):
			if rootIndex >= len(roots) {
				return "", errors.New("config changed while generating test roots")
			}
			output = append(output, indent+"- root: "+roots[rootIndex])
			rootIndex++
		case len(indent) == 0 && strings.HasPrefix(trimmed, "mountpoint:"):
			output = append(output, "mountpoint: "+mountpoint)
			foundMount = true
		case len(indent) == 0 && strings.HasPrefix(trimmed, "grpc_socket_path:"):
			foundGRPC = true
			output = append(output, "grpc_socket_path: "+socket)
		default:
			output = append(output, line)
		}
	}
	if rootIndex != len(roots) || !foundMount {
		return "", errors.New("config must contain disk roots and a top-level mountpoint")
	}
	if !foundGRPC {
		output = append(output, "grpc_socket_path: "+socket)
	}
	output = append(output, "grpc_enabled: "+fmt.Sprint(grpcEnabled))
	return strings.Join(output, "\n"), nil
}

func waitForWorker(command *exec.Cmd, done <-chan struct{}, mountpoint, socket string, grpcEnabled bool, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if isMounted(mountpoint) && fuseReady(mountpoint) && (!grpcEnabled || socketReady(socket)) {
			return nil
		}
		select {
		case <-done:
			return fmt.Errorf("Worker exited before becoming ready: %v", command.ProcessState)
		case <-deadline.C:
			return fmt.Errorf("Worker did not become ready within %s (mount=%t, FUSE-ready=%t, socket=%t)", timeout, isMounted(mountpoint), fuseReady(mountpoint), socketReady(socket))
		case <-ticker.C:
		}
	}
}

func isMounted(mountpoint string) bool {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 4 && fields[4] == mountpoint {
			return true
		}
	}
	return false
}

func fuseReady(mountpoint string) bool {
	// A mount is visible before Management finishes initialization. Readdir
	// goes through the real FUSE callback and remains EBUSY until LifecycleReady.
	_, err := os.ReadDir(mountpoint)
	return err == nil
}

func socketReady(socket string) bool {
	info, err := os.Stat(socket)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

func unmount(mountpoint string) error {
	if err := exec.Command("fusermount3", "-u", mountpoint).Run(); err == nil {
		return nil
	}
	return exec.Command("umount", mountpoint).Run()
}

func printResult(value result) {
	fmt.Printf("  %d FUSE clients, write %.2f MiB/s, read %.2f MiB/s, %.0f IOPS, verified=%t, RPC %d/%d errors\n",
		value.Workers, value.WriteMiBPerSec, value.ReadMiBPerSec, value.IOPS, value.IntegrityVerified, value.RPCErrors, value.RPCCalls)
}

func summarizeResults(samples []result) []result {
	byMode := map[mode][]result{}
	for _, sample := range samples {
		byMode[sample.Mode] = append(byMode[sample.Mode], sample)
	}
	summary := make([]result, 0, 3)
	for _, selected := range []mode{modeOff, modeIdle, modeActive} {
		values := byMode[selected]
		if len(values) == 0 {
			continue
		}
		writeRates, readRates, iops := make([]float64, 0, len(values)), make([]float64, 0, len(values)), make([]float64, 0, len(values))
		collectDuration := func(pick func(result) time.Duration) []int64 {
			items := make([]int64, 0, len(values))
			for _, value := range values {
				items = append(items, int64(pick(value)))
			}
			return items
		}
		rpcCounts, rpcErrors, rpcSuccess := make([]int64, 0, len(values)), make([]int64, 0, len(values)), make([]float64, 0, len(values))
		for _, value := range values {
			writeRates = append(writeRates, value.WriteMiBPerSec)
			readRates = append(readRates, value.ReadMiBPerSec)
			iops = append(iops, value.IOPS)
			rpcCounts = append(rpcCounts, int64(value.RPCCalls))
			rpcErrors = append(rpcErrors, int64(value.RPCErrors))
			rpcSuccess = append(rpcSuccess, value.RPCSuccessRatePct)
		}
		summary = append(summary, result{
			Mode: selected, Bytes: values[0].Bytes, Workers: values[0].Workers,
			IntegrityVerified: allIntegrityVerified(values),
			WriteMiBPerSec:    medianFloat64(writeRates), ReadMiBPerSec: medianFloat64(readRates), IOPS: medianFloat64(iops),
			WriteLatencyP50:     time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.WriteLatencyP50 }))),
			WriteLatencyP95:     time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.WriteLatencyP95 }))),
			WriteLatencyP99:     time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.WriteLatencyP99 }))),
			ReadLatencyP50:      time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.ReadLatencyP50 }))),
			ReadLatencyP95:      time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.ReadLatencyP95 }))),
			ReadLatencyP99:      time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.ReadLatencyP99 }))),
			AverageWriteLatency: time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.AverageWriteLatency }))),
			AverageReadLatency:  time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.AverageReadLatency }))),
			MetadataLatencyP50:  time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.MetadataLatencyP50 }))),
			MetadataLatencyP95:  time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.MetadataLatencyP95 }))),
			MetadataLatencyP99:  time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.MetadataLatencyP99 }))),
			SyncOps:             uint64(medianInt64(collectDuration(func(v result) time.Duration { return time.Duration(v.SyncOps) }))),
			SyncLatencyP50:      time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.SyncLatencyP50 }))),
			SyncLatencyP95:      time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.SyncLatencyP95 }))),
			SyncLatencyP99:      time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.SyncLatencyP99 }))),
			RPCCalls:            uint64(medianInt64(rpcCounts)), RPCErrors: uint64(medianInt64(rpcErrors)), RPCSuccessRatePct: medianFloat64(rpcSuccess),
			AverageRPCLatency: time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.AverageRPCLatency }))),
			RPCLatencyP50:     time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.RPCLatencyP50 }))),
			RPCLatencyP95:     time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.RPCLatencyP95 }))),
			RPCLatencyP99:     time.Duration(medianInt64(collectDuration(func(v result) time.Duration { return v.RPCLatencyP99 }))),
		})
	}
	return summary
}

func allIntegrityVerified(values []result) bool {
	for _, value := range values {
		if !value.IntegrityVerified {
			return false
		}
	}
	return true
}

func medianFloat64(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

func medianInt64(values []int64) int64 {
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

func markdownReport(results []result) string {
	var builder strings.Builder
	builder.WriteString("| gRPC mode | Write MiB/s | Read MiB/s | IOPS | Write p95 | Read p95 | Metadata p95 | Sync p95 | RPC calls/errors | RPC success | RPC p95 | Integrity |\n")
	builder.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|\n")
	for _, value := range results {
		fmt.Fprintf(&builder, "| %s | %.2f | %.2f | %.0f | %s | %s | %s | %s | %d/%d | %s | %s | %t |\n",
			value.Mode, value.WriteMiBPerSec, value.ReadMiBPerSec, value.IOPS,
			value.WriteLatencyP95.Round(time.Microsecond), value.ReadLatencyP95.Round(time.Microsecond),
			value.MetadataLatencyP95.Round(time.Microsecond), value.SyncLatencyP95.Round(time.Microsecond),
			value.RPCCalls, value.RPCErrors, rpcSuccessLabel(value), value.RPCLatencyP95.Round(time.Microsecond), value.IntegrityVerified)
	}
	if len(results) == 3 {
		for _, index := range []int{1, 2} {
			writeDelta := percentage(results[0].WriteMiBPerSec, results[index].WriteMiBPerSec)
			readDelta := percentage(results[0].ReadMiBPerSec, results[index].ReadMiBPerSec)
			iopsDelta := percentage(results[0].IOPS, results[index].IOPS)
			fmt.Fprintf(&builder, "\n- %s vs off: write %+.2f%%, read %+.2f%%, IOPS %+.2f%%\n",
				results[index].Mode, writeDelta, readDelta, iopsDelta)
		}
	}
	return builder.String()
}

func rpcSuccessLabel(value result) string {
	if value.RPCCalls == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.2f%%", value.RPCSuccessRatePct)
}

func percentage(baseline, current float64) float64 {
	if baseline == 0 {
		return 0
	}
	return (current/baseline - 1) * 100
}

func unquote(value string) string {
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
		(value[0] == '\'' && value[len(value)-1] == '\'')) {
		return value[1 : len(value)-1]
	}
	return value
}
