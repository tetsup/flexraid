# flexraid benchmark

The Worker binary includes an isolated storage benchmark subcommand. It uses the configured physical drives but does not write into the configured production Storage Roots or require the production Worker to be running.

## Usage

```sh
go build -o ./bin/flexraid ./main
sudo ./bin/flexraid benchmark --config /etc/flexraid/config.yaml --size 8589934592
```

The configured physical filesystems must already be mounted, and each configured WWN must resolve through `/dev/disk/by-id/wwn-<WWN>`. The configured production FUSE mountpoint must be unmounted. The command refuses to run if it detects a FUSE mount there, preventing concurrent access by the production Worker.

## Isolation and cleanup

The command selects the four configured drives with the most available capacity (configuration order breaks ties). On each selected physical mountpoint it creates a uniquely named temporary Storage Root. It then launches a temporary Worker using a generated configuration that points only to those isolated roots and to a temporary FUSE mountpoint.

Raw ext4 tests write under separate random temporary directories on the physical filesystems. FlexRAID tests write through the temporary Worker mount. After the test, the FUSE test directory is removed, the temporary Worker is unmounted and stopped, and the temporary Storage Roots are removed directly from their physical filesystems. This removes all benchmark chunks even though FlexRAID normally retains deleted physical versions for asynchronous cleanup.

If the Worker cannot be unmounted cleanly, the command preserves its temporary Storage Roots and workspace rather than deleting storage that might still be mounted. It reports the paths for manual inspection. A forced process termination can also leave `.flexraid-bench-*` directories behind; inspect and remove only the exact benchmark directories manually.

The output directory must not overlap any configured Storage Root, physical mountpoint, or production FUSE mountpoint. Symlinks are resolved before this check.

## Measurement modes

- The four selected drives are ordered by available capacity, matching the placement preference for new stripes.
- `raw-1drive`: one sequential raw stream on the first selected drive.
- `raw-3drive`: three parallel raw streams across the first three selected drives.
- `raw-4drive`: four parallel raw streams across all four selected drives.
- `flexraid`: one sequential stream through the temporary FlexRAID FUSE mount.

Each mode measures reads and writes using 4 KiB, 4 KiB+1 B, 64 KiB, 6 MiB, 6 MiB+1 B, and 64 MiB requests. All modes use the same total logical data size. The default is 512 MiB per case; increase `--size` beyond available memory for sustained storage measurements. Before starting the temporary Worker, the command checks that every selected drive has enough free space for all six FlexRAID write cases, plus a safety margin. At the default size this is approximately 1.26 GiB per selected drive.

Raw three-drive parallel is the closest payload-parallel reference for a 3+1 parity layout. Raw four-drive parallel shows the aggregate bandwidth across four physical drives. Neither ratio is a pure parity-cost measurement.

fio uses buffered I/O for all cases so the unaligned `+1 B` request sizes remain comparable. Results include Linux page-cache effects. Each case processes the full configured data size.

## Output

The output directory contains per-case fio JSON and job files plus `summary.csv` and `summary.md`.
