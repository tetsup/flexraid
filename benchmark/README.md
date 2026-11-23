# flexraid benchmark

Two independent benchmarks are provided:

- `flexraid benchmark` measures I/O through an isolated temporary FlexRAID Worker and FUSE mount.
- `flexraid benchmark raw` measures a directory on an arbitrary mounted filesystem. It does not load FlexRAID configuration, resolve device IDs, start a Worker, or mount FUSE.

Both commands report write, overwrite, and read measurements for the same I/O request sizes. Every phase processes a fixed number of files; elapsed time is measured, not used as a stopping condition.

## FlexRAID benchmark

```sh
go build -o ./bin/flexraid ./main
sudo ./bin/flexraid benchmark --config /etc/flexraid/config.yaml --size 8589934592
```

The configured physical filesystems must already be mounted, configured device identifiers must resolve, and the production FUSE mountpoint must be unmounted. The command creates temporary Storage Roots and starts a temporary Worker using those roots. It cleans up the temporary mount and storage after measurement. If unmounting fails, it preserves the storage roots and workspace for manual inspection.

The benchmark selects four configured drives with the most available capacity for the temporary FlexRAID Worker. This selection is only part of setting up the FlexRAID test; it does not create raw-disk comparison workloads.

## Raw directory benchmark

```sh
sudo ./bin/flexraid benchmark raw \
  --directory /mnt/disk/benchmark \
  --reference-bytes 4294967296 \
  --max-files 1024 \
  --output ./raw-bench-results
```

The directory must already exist and be writable. The command creates a uniquely named temporary subdirectory there, creates random-named test files, and removes the temporary subdirectory afterward. Existing files in the target directory are left untouched. It does not inspect or modify any FlexRAID configuration or storage roots. Run it separately for each directory you want to measure.

## Measurements

The I/O request sizes are 4 KiB, 4 KiB+1 B, 64 KiB, 6 MiB, 6 MiB+1 B, and 64 MiB. `--reference-bytes` sets the logical-byte target used to derive each file count, and `--max-files` caps the count for every I/O size. The defaults are 4 GiB and 1024 files. Thus 64 MiB uses 64 files, 6 MiB uses 682 files, and small requests are capped at 1024 files. The actual logical bytes vary by request size; they are intentionally not normalized to a fixed total.

For each size:

- **Write** creates exactly the derived number of new randomly named files, writes one request-sized payload to each, and calls `fsync()`. Files are never reused during this phase.
- **Overwrite** clears the filesystem cache, then visits every generated file exactly once in creation order and overwrites its complete payload, including `fsync()` and `close()` for every file.
- **Read** clears the filesystem cache, then reads every generated file exactly once in creation order. It never loops back to already-read files, so the result cannot turn into a page-cache benchmark.

Before every measurement (including immediately before the read phase), the benchmark runs `sync` and clears Linux page caches through `/proc/sys/vm/drop_caches`; it logs this action and fails the measurement if either operation fails. Each file operation includes open/create, data transfer, and close; writes and overwrites also include `fsync()`. Every phase reports actual elapsed time, file-operation count, total bytes, bandwidth, and IOPS. Write, overwrite, and read therefore have identical operation counts and byte totals for each I/O size. Both benchmark modes use buffered I/O (no `O_DIRECT`), keeping unaligned +1 B request sizes valid and comparable. Results therefore measure normal POSIX buffered-I/O behavior while ensuring writes are timed through durable flush completion.

## Output

Each command writes `summary.csv`, `summary.md` to its output directory. Both summaries use the same columns, including a filesystem identifier, so CI or other external tooling can combine and compare independent runs. The commands do not launch each other or perform comparisons automatically.
