package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

type latencySamples struct {
	mu     sync.Mutex
	values []time.Duration
}

func (samples *latencySamples) add(value time.Duration) {
	samples.mu.Lock()
	samples.values = append(samples.values, value)
	samples.mu.Unlock()
}

func (samples *latencySamples) summary() (time.Duration, time.Duration, time.Duration, time.Duration) {
	samples.mu.Lock()
	values := append([]time.Duration(nil), samples.values...)
	samples.mu.Unlock()
	if len(values) == 0 {
		return 0, 0, 0, 0
	}
	sortDurations(values)
	var total time.Duration
	for _, value := range values {
		total += value
	}
	return total / time.Duration(len(values)), percentileDuration(values, .50), percentileDuration(values, .95), percentileDuration(values, .99)
}

func sortDurations(values []time.Duration) {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
}

func percentileDuration(sorted []time.Duration, percentile float64) time.Duration {
	index := int(float64(len(sorted)-1)*percentile + .999999)
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

type workloadMetrics struct {
	syncLatency     latencySamples
	syncOps         atomic.Uint64
	duration        time.Duration
	writeHash       string
	readHash        string
	writeLatency    latencySamples
	readLatency     latencySamples
	metadataLatency latencySamples
	rpcLatency      latencySamples
	writeBytes      atomic.Int64
	readBytes       atomic.Int64
	writeOps        atomic.Uint64
	readOps         atomic.Uint64
	metadataOps     atomic.Uint64
	rpcCalls        atomic.Uint64
	rpcErrors       atomic.Uint64
}

type workerExpected struct {
	blocks  [][]byte
	written []bool
}

type rpcPoller struct {
	method     string
	interval   time.Duration
	connection *grpc.ClientConn
}

func runConcurrentWorkload(mountpoint, socket string, opts options, measured *result, active bool) error {
	if opts.workers < 2 {
		return fmt.Errorf("workers must be at least 2")
	}
	if opts.blockSize <= 0 || opts.size < int64(opts.workers*opts.blockSize) {
		return fmt.Errorf("working-set size %d must be at least workers*block-size (%d)", opts.size, opts.workers*opts.blockSize)
	}
	dataPath := filepath.Join(mountpoint, "concurrent-shared.dat")
	file, err := os.OpenFile(dataPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create shared FUSE test file: %w", err)
	}
	if err := file.Truncate(opts.size); err != nil {
		_ = file.Close()
		return fmt.Errorf("size shared FUSE test file: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}

	if opts.warmupDuration > 0 {
		if err := runFilesystemPhase(mountpoint, dataPath, "", opts, opts.warmupDuration, false, nil); err != nil {
			return fmt.Errorf("warm-up FUSE workload: %w", err)
		}
	}
	file, err = os.OpenFile(dataPath, os.O_RDWR|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("reset shared FUSE test file: %w", err)
	}
	if err := file.Truncate(opts.size); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}

	metrics := &workloadMetrics{}
	if err := runFilesystemPhase(mountpoint, dataPath, socket, opts, opts.duration, active, metrics); err != nil {
		return err
	}
	elapsed := metrics.duration
	if elapsed <= 0 {
		return fmt.Errorf("invalid measured duration")
	}

	writeTotal, writeP50, writeP95, writeP99 := metrics.writeLatency.summary()
	readTotal, readP50, readP95, readP99 := metrics.readLatency.summary()
	_, metaP50, metaP95, metaP99 := metrics.metadataLatency.summary()
	_, syncP50, syncP95, syncP99 := metrics.syncLatency.summary()
	rpcTotal, rpcP50, rpcP95, rpcP99 := metrics.rpcLatency.summary()
	writeBytes, readBytes := metrics.writeBytes.Load(), metrics.readBytes.Load()
	writeOps, readOps, metaOps, syncOps := metrics.writeOps.Load(), metrics.readOps.Load(), metrics.metadataOps.Load(), metrics.syncOps.Load()
	rpcCalls, rpcErrors := metrics.rpcCalls.Load(), metrics.rpcErrors.Load()

	measured.Duration = elapsed
	measured.WriteSHA256 = metrics.writeHash
	measured.ReadSHA256 = metrics.readHash
	measured.IntegrityVerified = metrics.writeHash != "" && metrics.writeHash == metrics.readHash
	measured.Workers = opts.workers
	measured.WriteOps, measured.ReadOps, measured.MetadataOps = writeOps, readOps, metaOps
	measured.IOPS = float64(writeOps+readOps+metaOps+syncOps) / elapsed.Seconds()
	measured.WriteMiBPerSec = float64(writeBytes) / (1 << 20) / elapsed.Seconds()
	measured.ReadMiBPerSec = float64(readBytes) / (1 << 20) / elapsed.Seconds()
	measured.AverageWriteLatency, measured.AverageReadLatency = writeTotal, readTotal
	measured.WriteLatencyP50, measured.WriteLatencyP95, measured.WriteLatencyP99 = writeP50, writeP95, writeP99
	measured.ReadLatencyP50, measured.ReadLatencyP95, measured.ReadLatencyP99 = readP50, readP95, readP99
	measured.MetadataLatencyP50, measured.MetadataLatencyP95, measured.MetadataLatencyP99 = metaP50, metaP95, metaP99
	measured.SyncOps = syncOps
	measured.SyncLatencyP50, measured.SyncLatencyP95, measured.SyncLatencyP99 = syncP50, syncP95, syncP99
	measured.RPCCalls, measured.RPCErrors = rpcCalls, rpcErrors
	if rpcCalls > 0 {
		measured.RPCSuccessRatePct = float64(rpcCalls-rpcErrors) * 100 / float64(rpcCalls)
	}
	measured.AverageRPCLatency, measured.RPCLatencyP50, measured.RPCLatencyP95, measured.RPCLatencyP99 = rpcTotal, rpcP50, rpcP95, rpcP99
	if active && rpcCalls == 0 {
		return fmt.Errorf("active gRPC condition completed no RPCs")
	}
	if rpcErrors != 0 {
		return fmt.Errorf("gRPC monitoring had %d errors across %d calls", rpcErrors, rpcCalls)
	}
	if writeOps == 0 || readOps == 0 || metaOps == 0 {
		return fmt.Errorf("incomplete workload: writes=%d reads=%d metadata=%d", writeOps, readOps, metaOps)
	}
	return nil
}

func runFilesystemPhase(mountpoint, dataPath, socket string, opts options, duration time.Duration, active bool, metrics *workloadMetrics) error {
	var err error
	regionSize := (opts.size / int64(opts.workers) / int64(opts.blockSize)) * int64(opts.blockSize)
	slots := int(regionSize / int64(opts.blockSize))
	if slots < 1 {
		return fmt.Errorf("not enough space per worker for one block")
	}
	expected := make([]workerExpected, opts.workers)
	for index := range expected {
		expected[index] = workerExpected{blocks: make([][]byte, slots), written: make([]bool, slots)}
	}
	directories := make([]string, opts.workers)
	for index := range directories {
		directories[index] = filepath.Join(mountpoint, fmt.Sprintf("concurrent-worker-%02d", index))
		if err := os.Mkdir(directories[index], 0700); err != nil {
			for _, created := range directories[:index] {
				_ = os.RemoveAll(created)
			}
			return fmt.Errorf("create worker directory %d: %w", index, err)
		}
	}
	defer func() {
		for _, directory := range directories {
			_ = os.RemoveAll(directory)
		}
	}()

	// Exercise namespace mutations and truncate outside the timed data-I/O
	// phase. The benchmark duration must stop workers between operations, not
	// interrupt a metadata syscall and misreport its cancellation as EOPNOTSUPP.
	for workerID, directory := range directories {
		scratchPath := filepath.Join(directory, "truncate.dat")
		scratch, err := os.OpenFile(scratchPath, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return fmt.Errorf("create worker %d metadata scratch file: %w", workerID, err)
		}
		if err := scratch.Truncate(4096); err != nil {
			_ = scratch.Close()
			return fmt.Errorf("initialize worker %d metadata scratch file: %w", workerID, err)
		}
		if err := scratch.Close(); err != nil {
			return fmt.Errorf("close worker %d metadata scratch file: %w", workerID, err)
		}
		if err := runMetadataCycle(directory, workerID, 0, scratchPath, metrics); err != nil {
			return fmt.Errorf("worker %d metadata workload: %w", workerID, err)
		}
	}

	workerFiles := make([]*os.File, opts.workers)
	readerFiles := make([]*os.File, opts.workers)
	for workerID := range workerFiles {
		workerFiles[workerID], err = os.OpenFile(dataPath, os.O_RDWR, 0600)
		if err != nil {
			for _, opened := range workerFiles[:workerID] {
				_ = opened.Close()
			}
			return fmt.Errorf("open worker %d write handle: %w", workerID, err)
		}
		readerFiles[workerID], err = os.Open(dataPath)
		if err != nil {
			_ = workerFiles[workerID].Close()
			for _, opened := range workerFiles[:workerID] {
				_ = opened.Close()
			}
			for _, opened := range readerFiles[:workerID] {
				_ = opened.Close()
			}
			return fmt.Errorf("open worker %d read handle: %w", workerID, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := make(chan struct{})
	ready := make(chan struct{}, opts.workers+4)
	errCh := make(chan error, opts.workers)
	var workers sync.WaitGroup
	for workerID := 0; workerID < opts.workers; workerID++ {
		workerFile, readerFile := workerFiles[workerID], readerFiles[workerID]
		workers.Add(1)
		go func(id int, writeHandle, readHandle *os.File) {
			defer workers.Done()
			defer writeHandle.Close()
			defer readHandle.Close()
			ready <- struct{}{}
			<-start
			if workerErr := runFilesystemWorker(ctx, id, writeHandle, readHandle, directories[id], regionSize, slots, opts.blockSize, &expected[id], metrics); workerErr != nil {
				select {
				case errCh <- workerErr:
				default:
				}
				cancel()
			}
		}(workerID, workerFile, readerFile)
	}

	var connections []*grpc.ClientConn
	var pollers sync.WaitGroup
	if active {
		definitions := []struct {
			method   string
			interval time.Duration
		}{
			{"/flexraid.worker.v1.WorkerService/GetStatus", opts.rpcEvery},
			{"/flexraid.worker.v1.WorkerService/GetStatus", opts.rpcEvery},
			{"/flexraid.worker.v1.WorkerService/GetDetailedStatus", opts.rpcEvery * 2},
			{"/flexraid.worker.v1.WorkerService/Inspect", opts.rpcEvery * 5},
		}
		for _, definition := range definitions {
			connection, clientErr := grpc.NewClient(
				"passthrough:///flexraid-worker",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socket)
				}),
			)
			if clientErr != nil {
				cancel()
				close(start)
				workers.Wait()
				pollers.Wait()
				for _, opened := range connections {
					_ = opened.Close()
				}
				return fmt.Errorf("create gRPC client: %w", clientErr)
			}
			connections = append(connections, connection)
			pollers.Add(1)
			go runMonitoringPoller(ctx, start, ready, &pollers, connection, definition.method, definition.interval, metrics)
		}
	}
	defer func() {
		cancel()
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()

	for index := 0; index < opts.workers+len(connections); index++ {
		<-ready
	}
	close(start)
	phaseStarted := time.Now()
	timer := time.NewTimer(duration)
	select {
	case <-timer.C:
	case <-ctx.Done():
		timer.Stop()
	}
	if metrics != nil {
		metrics.duration = time.Since(phaseStarted)
	}
	cancel()
	workers.Wait()
	pollers.Wait()
	select {
	case workerErr := <-errCh:
		return workerErr
	default:
	}

	// Verify the final contents of every block written by every concurrent
	// worker. Workers own disjoint ranges of this same inode, not separate files.
	verifyFile, err := os.OpenFile(dataPath, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer verifyFile.Close()
	// Flush once after all concurrent workers have stopped. This makes the
	// final integrity pass observe durable data without contending multiple
	// simultaneous fsync requests on the same logical Object.
	syncStarted := time.Now()
	if err := syncRetry(verifyFile); err != nil {
		return fmt.Errorf("final FUSE fsync: %w", err)
	}
	if metrics != nil {
		metrics.syncLatency.add(time.Since(syncStarted))
		metrics.syncOps.Add(1)
		metrics.duration = time.Since(phaseStarted)
	}
	expectedHash, actualHash := sha256.New(), sha256.New()
	verifiedBlocks := 0
	for workerID := range expected {
		base := int64(workerID) * regionSize
		for slot, block := range expected[workerID].blocks {
			if !expected[workerID].written[slot] {
				continue
			}
			actual := make([]byte, opts.blockSize)
			offset := base + int64(slot*opts.blockSize)
			if _, err := readAtRetry(verifyFile, actual, offset); err != nil {
				return fmt.Errorf("final read worker %d slot %d: %w", workerID, slot, err)
			}
			if !bytes.Equal(actual, block) {
				return fmt.Errorf("final data integrity mismatch at worker %d slot %d", workerID, slot)
			}
			_, _ = expectedHash.Write(block)
			_, _ = actualHash.Write(actual)
			verifiedBlocks++
		}
	}
	if verifiedBlocks == 0 {
		return fmt.Errorf("no data blocks were verified")
	}
	if !bytes.Equal(expectedHash.Sum(nil), actualHash.Sum(nil)) {
		return fmt.Errorf("final SHA-256 mismatch: expected %s, got %s",
			hex.EncodeToString(expectedHash.Sum(nil)), hex.EncodeToString(actualHash.Sum(nil)))
	}
	if metrics != nil {
		metrics.writeHash = hex.EncodeToString(expectedHash.Sum(nil))
		metrics.readHash = hex.EncodeToString(actualHash.Sum(nil))
	}
	return nil
}

func runFilesystemWorker(ctx context.Context, workerID int, writeFile, readFile *os.File, directory string, regionSize int64, slots, blockSize int, expected *workerExpected, metrics *workloadMetrics) error {
	payload := make([]byte, blockSize)
	readback := make([]byte, blockSize)
	var sequence uint64
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		// Alternate sequential progression and a repeatable pseudo-random
		// permutation while keeping each worker's region disjoint.
		var slot int
		if sequence%2 == 0 {
			slot = int((sequence / 2) % uint64(slots))
		} else {
			slot = int(((sequence/2)*uint64(73) + uint64(workerID*19)) % uint64(slots))
		}
		for index := range payload {
			payload[index] = byte((workerID*37 + int(sequence%251)*13 + index*7) % 251)
		}
		offset := int64(workerID)*regionSize + int64(slot*blockSize)
		started := time.Now()
		n, writeErr := writeAtRetry(writeFile, payload, offset)
		if writeErr != nil {
			return fmt.Errorf("worker %d WriteAt: %w", workerID, writeErr)
		}
		if n != len(payload) {
			return fmt.Errorf("worker %d short WriteAt: %d/%d", workerID, n, len(payload))
		}
		if metrics != nil {
			metrics.writeLatency.add(time.Since(started))
			metrics.writeBytes.Add(int64(n))
			metrics.writeOps.Add(1)
		}
		expected.blocks[slot] = append(expected.blocks[slot][:0], payload...)
		expected.written[slot] = true

		// Keep concurrent FUSE reads/writes independent. Fsync is deliberately
		// batched: issuing one fsync per 64 KiB write makes the test primarily
		// measure serialized durability barriers rather than RPC interference.
		if sequence%16 == 15 {
			started = time.Now()
			if err := syncRetry(writeFile); err != nil {
				return fmt.Errorf("worker %d periodic Sync: %w", workerID, err)
			}
			if metrics != nil {
				metrics.syncLatency.add(time.Since(started))
				metrics.syncOps.Add(1)
			}
		}

		started = time.Now()
		n, readErr := readAtRetry(readFile, readback, offset)
		if readErr != nil {
			return fmt.Errorf("worker %d ReadAt: %w", workerID, readErr)
		}
		if n != len(readback) || !bytes.Equal(payload, readback) {
			return fmt.Errorf("worker %d immediate read verification failed at offset %d", workerID, offset)
		}
		if metrics != nil {
			metrics.readLatency.add(time.Since(started))
			metrics.readBytes.Add(int64(n))
			metrics.readOps.Add(1)
		}

		sequence++
	}
}

func runMetadataCycle(directory string, workerID int, sequence uint64, scratchPath string, metrics *workloadMetrics) error {
	measure := func(operation func() error) error {
		started := time.Now()
		err := operation()
		if metrics != nil {
			metrics.metadataLatency.add(time.Since(started))
			metrics.metadataOps.Add(1)
		}
		return err
	}
	base := filepath.Join(directory, fmt.Sprintf("entry-%d", sequence))
	temporary, renamed := base+".tmp", base+".renamed"
	if err := measure(func() error {
		file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write([]byte(fmt.Sprintf("worker=%d sequence=%d", workerID, sequence)))
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}); err != nil {
		return fmt.Errorf("metadata create/write: %w", err)
	}
	if err := measure(func() error { return os.Rename(temporary, renamed) }); err != nil {
		return fmt.Errorf("metadata rename: %w", err)
	}
	if err := measure(func() error { _, err := os.Stat(renamed); return err }); err != nil {
		return fmt.Errorf("metadata stat: %w", err)
	}
	if err := measure(func() error { return os.Remove(renamed) }); err != nil {
		return fmt.Errorf("metadata unlink: %w", err)
	}
	subdirectory := base + ".dir"
	if err := measure(func() error { return os.Mkdir(subdirectory, 0700) }); err != nil {
		return fmt.Errorf("metadata mkdir: %w", err)
	}
	if err := measure(func() error { return os.Remove(subdirectory) }); err != nil {
		return fmt.Errorf("metadata rmdir: %w", err)
	}
	if err := measure(func() error {
		file, err := os.OpenFile(scratchPath, os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		truncateErr := file.Truncate(int64(sequence%3) * 4096)
		closeErr := file.Close()
		if truncateErr != nil {
			return truncateErr
		}
		return closeErr
	}); err != nil {
		return fmt.Errorf("metadata truncate: %w", err)
	}
	return nil
}

func writeAtRetry(file *os.File, data []byte, offset int64) (int, error) {
	for {
		n, err := file.WriteAt(data, offset)
		if !errors.Is(err, syscall.EINTR) {
			return n, err
		}
	}
}

func readAtRetry(file *os.File, data []byte, offset int64) (int, error) {
	for {
		n, err := file.ReadAt(data, offset)
		if !errors.Is(err, syscall.EINTR) {
			return n, err
		}
	}
}

func syncRetry(file *os.File) error {
	for {
		err := file.Sync()
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func runMonitoringPoller(ctx context.Context, start <-chan struct{}, ready chan<- struct{}, wait *sync.WaitGroup, connection *grpc.ClientConn, method string, interval time.Duration, metrics *workloadMetrics) {
	defer wait.Done()
	ready <- struct{}{}
	<-start
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		started := time.Now()
		response := new(structpb.Struct)
		err := connection.Invoke(callCtx, method, &emptypb.Empty{}, response)
		cancel()
		if ctx.Err() == nil {
			metrics.rpcCalls.Add(1)
			metrics.rpcLatency.add(time.Since(started))
			if err != nil {
				metrics.rpcErrors.Add(1)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
