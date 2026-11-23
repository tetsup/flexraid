# flexraid benchmark

Two independent benchmarks are provided:

- `flexraid benchmark` measures I/O through an isolated temporary FlexRAID Worker and FUSE mount.
- `flexraid benchmark raw` measures a directory on an arbitrary mounted filesystem. It does not load FlexRAID configuration, resolve device IDs, start a Worker, or mount FUSE.

Both commands report write, overwrite, and read measurements for the same I/O request sizes. Each measurement runs for at most 20 seconds by default; use `--duration-seconds` to change the limit.

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
  --size 8589934592 \
  --duration-seconds 20 \
  --output ./raw-bench-results
```

The directory must already exist and be writable. The command creates a uniquely named temporary subdirectory there, uses its own `data.bin` test file, and removes the temporary subdirectory afterward. Existing files in the target directory are left untouched. It does not inspect or modify any FlexRAID configuration or storage roots. Run it separately for each directory you want to measure.

## Measurements

The I/O request sizes are 4 KiB, 4 KiB+1 B, 64 KiB, 6 MiB, 6 MiB+1 B, and 64 MiB.

For each size:

- **Write** creates/writes a bounded data file for the configured time limit and includes fio's final `fsync()`.
- **Overwrite** clears the filesystem cache, then measures `open()`, writes from the beginning until the file is fully overwritten or the time limit expires, `fsync()`, and `close()`. It does not truncate the file.
- **Read** clears the filesystem cache and measures `open()`, one sequential read of the generated file, and `close()`. It stops at EOF or the time limit and does not loop over the same file to count page-cache hits.

Before every measurement, the benchmark runs `sync` and clears Linux page caches through `/proc/sys/vm/drop_caches`. Write and overwrite elapsed time includes the complete open/write/fsync/close lifecycle; read elapsed time includes open/read/close. Both benchmark modes use buffered I/O (no `O_DIRECT`), keeping unaligned +1 B request sizes valid and comparable. Results therefore measure normal POSIX buffered-I/O behavior while ensuring writes are timed through durable flush completion.

## Output

Each command writes `summary.csv`, `summary.md`, and per-case fio job/JSON files to its output directory. Both summaries use the same columns, including a filesystem identifier, so CI or other external tooling can combine and compare independent runs. The commands do not launch each other or perform comparisons automatically.
