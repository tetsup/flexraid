package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

type mode string

const (
	modeOff    mode = "off"
	modeIdle   mode = "on-idle"
	modeActive mode = "on-active"
)

type result struct {
	Mode              mode          `json:"mode"`
	Bytes             int64         `json:"bytes"`
	WriteDuration     time.Duration `json:"write_duration_ns"`
	ReadDuration      time.Duration `json:"read_duration_ns"`
	WriteMiBPerSec    float64       `json:"write_mib_per_sec"`
	ReadMiBPerSec     float64       `json:"read_mib_per_sec"`
	WriteSHA256       string        `json:"write_sha256"`
	ReadSHA256        string        `json:"read_sha256"`
	RPCCalls          uint64        `json:"rpc_calls"`
	RPCErrors         uint64        `json:"rpc_errors"`
	AverageRPCLatency time.Duration `json:"average_rpc_latency_ns"`
}

type options struct {
	configPath string
	workerPath string
	size       int64
	warmupSize int64
	blockSize  int
	rpcEvery   time.Duration
	reportPath string
	startup    time.Duration
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
	flag.Int64Var(&opts.size, "size", 256<<20, "measured file size in bytes")
	flag.Int64Var(&opts.warmupSize, "warmup-size", 16<<20, "warm-up file size in bytes")
	flag.IntVar(&opts.blockSize, "block-size", 1<<20, "sequential I/O block size in bytes")
	flag.DurationVar(&opts.rpcEvery, "rpc-interval", 250*time.Millisecond, "on-active GetStatus interval")
	flag.StringVar(&opts.reportPath, "report", "flexraid-stress-report.json", "JSON report path")
	flag.DurationVar(&opts.startup, "startup-timeout", 90*time.Second, "Worker startup timeout")
	flag.Parse()

	if opts.configPath == "" {
		return errors.New("--config is required; use a dedicated test configuration with test-only storage roots")
	}
	if opts.size <= 0 || opts.warmupSize < 0 || opts.blockSize <= 0 || opts.rpcEvery <= 0 {
		return errors.New("size, block-size, and rpc-interval must be positive; warmup-size must not be negative")
	}
	if _, err := os.Stat(opts.workerPath); err != nil {
		return fmt.Errorf("Worker executable: %w", err)
	}
	template, err := os.ReadFile(opts.configPath)
	if err != nil {
		return fmt.Errorf("read config template: %w", err)
	}

	var results []result
	for _, selected := range []mode{modeOff, modeIdle, modeActive} {
		fmt.Printf("Running %s...\n", selected)
		measured, err := runMode(opts, string(template), selected)
		if err != nil {
			return fmt.Errorf("%s: %w", selected, err)
		}
		results = append(results, measured)
		printResult(measured)
	}

	report := struct {
		CreatedAt time.Time `json:"created_at"`
		Results   []result  `json:"results"`
	}{CreatedAt: time.Now().UTC(), Results: results}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	if err := os.WriteFile(opts.reportPath, append(encoded, '\n'), 0600); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	fmt.Printf("\n%s\n", markdownReport(results))
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
	if selected == modeActive {
		if err := runBenchmarkWithMonitoring(mountpoint, socketPath, opts, &result); err != nil {
			return result, err
		}
	} else {
		if err := runBenchmark(mountpoint, opts, &result); err != nil {
			return result, err
		}
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
		if isMounted(mountpoint) && (!grpcEnabled || socketReady(socket)) {
			return nil
		}
		select {
		case <-done:
			return fmt.Errorf("Worker exited before becoming ready: %v", command.ProcessState)
		case <-deadline.C:
			return fmt.Errorf("Worker did not become ready within %s (mount=%t, socket=%t)", timeout, isMounted(mountpoint), socketReady(socket))
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

func runBenchmark(mountpoint string, opts options, measured *result) error {
	if opts.warmupSize > 0 {
		if err := transferFile(mountpoint, "warmup.dat", opts.warmupSize, opts.blockSize, false); err != nil {
			return fmt.Errorf("warm-up: %w", err)
		}
		if err := os.Remove(filepath.Join(mountpoint, "warmup.dat")); err != nil {
			return err
		}
	}
	writeDuration, readDuration, writeHash, readHash, err := transferFileMeasured(mountpoint, "benchmark.dat", opts.size, opts.blockSize)
	if err != nil {
		return err
	}
	measured.WriteDuration, measured.ReadDuration = writeDuration, readDuration
	measured.WriteSHA256, measured.ReadSHA256 = writeHash, readHash
	measured.WriteMiBPerSec = float64(opts.size) / (1 << 20) / writeDuration.Seconds()
	measured.ReadMiBPerSec = float64(opts.size) / (1 << 20) / readDuration.Seconds()
	return nil
}

func transferFile(mountpoint, name string, size int64, blockSize int, _ bool) error {
	_, _, _, _, err := transferFileMeasured(mountpoint, name, size, blockSize)
	return err
}

func transferFileMeasured(mountpoint, name string, size int64, blockSize int) (time.Duration, time.Duration, string, string, error) {
	path := filepath.Join(mountpoint, name)
	buffer := make([]byte, blockSize)
	for i := range buffer {
		buffer[i] = byte((i*31 + 17) % 251)
	}
	file, err := os.Create(path)
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("create benchmark file: %w", err)
	}
	writeHash := sha256.New()
	started := time.Now()
	var written int64
	for written < size {
		length := int64(len(buffer))
		if remaining := size - written; remaining < length {
			length = remaining
		}
		chunk := buffer[:int(length)]
		n, writeErr := file.Write(chunk)
		if writeErr != nil {
			_ = file.Close()
			return 0, 0, "", "", fmt.Errorf("write benchmark file: %w", writeErr)
		}
		if n != len(chunk) {
			_ = file.Close()
			return 0, 0, "", "", io.ErrShortWrite
		}
		_, _ = writeHash.Write(chunk)
		written += int64(n)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return 0, 0, "", "", fmt.Errorf("sync benchmark file: %w", err)
	}
	writeDuration := time.Since(started)
	if err := file.Close(); err != nil {
		return 0, 0, "", "", err
	}

	readFile, err := os.Open(path)
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("open benchmark file for read: %w", err)
	}
	readHash := sha256.New()
	readStarted := time.Now()
	if _, err := io.CopyBuffer(readHash, readFile, make([]byte, blockSize)); err != nil {
		_ = readFile.Close()
		return 0, 0, "", "", fmt.Errorf("read benchmark file: %w", err)
	}
	readDuration := time.Since(readStarted)
	if err := readFile.Close(); err != nil {
		return 0, 0, "", "", err
	}
	writeDigest := hex.EncodeToString(writeHash.Sum(nil))
	readDigest := hex.EncodeToString(readHash.Sum(nil))
	if writeDigest != readDigest {
		return 0, 0, "", "", fmt.Errorf("data verification failed: write sha256 %s, read sha256 %s", writeDigest, readDigest)
	}
	return writeDuration, readDuration, writeDigest, readDigest, nil
}

func runBenchmarkWithMonitoring(mountpoint, socket string, opts options, measured *result) error {
	connection, err := grpc.NewClient(
		"passthrough:///flexraid-worker",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}),
	)
	if err != nil {
		return fmt.Errorf("create gRPC client: %w", err)
	}
	defer connection.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var totalLatency time.Duration
	go func() {
		defer close(done)
		ticker := time.NewTicker(opts.rpcEvery)
		defer ticker.Stop()
		for {
			started := time.Now()
			callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
			response := new(structpb.Struct)
			err := connection.Invoke(callCtx, "/flexraid.worker.v1.WorkerService/GetStatus", &emptypb.Empty{}, response)
			callCancel()
			measured.RPCCalls++
			totalLatency += time.Since(started)
			if err != nil {
				measured.RPCErrors++
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	err = runBenchmark(mountpoint, opts, measured)
	cancel()
	<-done
	if measured.RPCCalls > 0 {
		measured.AverageRPCLatency = totalLatency / time.Duration(measured.RPCCalls)
	}
	return err
}

func printResult(value result) {
	fmt.Printf("  write %.2f MiB/s, read %.2f MiB/s, RPC %d/%d errors\n",
		value.WriteMiBPerSec, value.ReadMiBPerSec, value.RPCErrors, value.RPCCalls)
}

func markdownReport(results []result) string {
	var builder strings.Builder
	builder.WriteString("| gRPC mode | Write MiB/s | Read MiB/s | RPC calls | RPC errors | Avg RPC latency |\n")
	builder.WriteString("|---|---:|---:|---:|---:|---:|\n")
	for _, value := range results {
		fmt.Fprintf(&builder, "| %s | %.2f | %.2f | %d | %d | %s |\n",
			value.Mode, value.WriteMiBPerSec, value.ReadMiBPerSec, value.RPCCalls,
			value.RPCErrors, value.AverageRPCLatency.Round(time.Microsecond))
	}
	if len(results) == 3 {
		for _, index := range []int{1, 2} {
			writeDelta := percentage(results[0].WriteMiBPerSec, results[index].WriteMiBPerSec)
			readDelta := percentage(results[0].ReadMiBPerSec, results[index].ReadMiBPerSec)
			fmt.Fprintf(&builder, "\n- %s vs off: write %+.2f%%, read %+.2f%%\n", results[index].Mode, writeDelta, readDelta)
		}
	}
	return builder.String()
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
