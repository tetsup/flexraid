# flexraid benchmark

The Worker binary includes a benchmark subcommand. It runs against an already-mounted and Ready Worker; it does not start, stop, or reconfigure the Worker.

```sh
go build -o ./bin/flexraid ./main
sudo ./bin/flexraid benchmark --config /etc/flexraid/config.yaml
```

Options:

| Option | Default | Description |
|---|---:|---|
| `--config` | required | Worker YAML configuration |
| `--size` | 512 MiB | Total logical bytes per test case and mode |
| `--runtime` | 0 | Optional time-based fio run; zero processes the full configured size |
| `--fio` | `fio` | fio executable |
| `--output` | `./flexraid-bench-results` | Result directory, which must be outside all storage roots and the FUSE mount |

The command validates the configured WWN-to-device mappings and verifies that the configured mountpoint is a mounted FUSE filesystem before creating test data. It creates a random `.flexraid-bench-*` directory under each configured storage root and one under the FUSE mount, then removes only those directories on exit. A forced termination can leave these directories behind; inspect and remove only the exact benchmark directories manually.

Each I/O size is measured sequentially for writes and reads: 4 KiB, 4 KiB+1 B, 64 KiB, 6 MiB, 6 MiB+1 B, and 64 MiB. The same total logical data size is used for each mode:

- `raw-1drive`: one raw stream on the first configured drive.
- `raw-3drive`: three parallel raw streams across three drives.
- `raw-4drive`: four parallel raw streams across four drives.
- `flexraid`: one stream through the FlexRAID FUSE mount.

The raw three-drive mode is the closest payload-parallel reference for a 3+1 parity layout. The four-drive mode shows the aggregate bandwidth across four physical drives. Neither ratio is a pure parity-cost measurement.

fio uses buffered I/O for all cases so the unaligned `+1 B` request sizes remain comparable. Results therefore include Linux page-cache effects; increase `--size` beyond available memory to measure sustained storage throughput. O_DIRECT is intentionally not used because the unaligned cases cannot satisfy its alignment requirements. The command writes per-case fio JSON and job files plus CSV and Markdown summaries.
