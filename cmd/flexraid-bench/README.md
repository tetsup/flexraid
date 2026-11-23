# flexraid-bench

`flexraid-bench` measures sequential I/O throughput through an already-running FlexRAID Worker and directly against its configured physical storage roots.

## Usage

```sh
go build -o ./bin/flexraid-bench ./cmd/flexraid-bench
sudo ./bin/flexraid-bench --config /etc/flexraid/config.yaml
```

The Worker must already be mounted and Ready. The command does not start, stop, or reconfigure a Worker.

## Measurement modes

- `raw-1drive`: one sequential stream on the first configured physical storage root.
- `raw-3drive`: three parallel raw streams, with the same aggregate logical byte count.
- `raw-4drive`: four parallel raw streams, showing the four-device aggregate reference.
- `flexraid`: one sequential stream through the configured FUSE mount.

Each mode measures reads and writes using 4 KiB, 4 KiB+1 B, 64 KiB, 6 MiB, 6 MiB+1 B, and 64 MiB requests. Each case writes and reads its own file. The default data size is 512 MiB per case; use `--size` to change it. `--runtime` optionally changes fio to a time-based run. `--direct` requests O_DIRECT and should only be enabled when supported by both the physical filesystems and the FUSE mount.

## Safety

The command creates uniquely named temporary directories under each configured `disks[].root` and under the configured FUSE mountpoint. It writes only files inside those directories and removes those directories on normal exit. Do not run it while another benchmark or maintenance operation is using the same storage. A forced process termination can leave a `.flexraid-bench-*` directory behind; inspect and remove only that exact benchmark directory manually.

The raw modes are references, not a pure parity-cost measurement. The three-drive raw mode is the closest payload-parallel comparison for a 3+1 parity layout; the four-drive mode shows the aggregate bandwidth available across all four devices. Buffered I/O is used by default, so results include Linux page-cache effects. Increase `--size` beyond available memory or use `--direct` where supported for storage-throughput measurements.

## Output

The output directory contains per-case fio JSON and job files plus `summary.csv` and `summary.md`.
