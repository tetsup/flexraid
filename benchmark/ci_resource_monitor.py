#!/usr/bin/env python3
"""Sample Linux memory, swap, PSI, and block-device counters during CI benchmarks."""

import json
import signal
import sys
import time

if len(sys.argv) != 2:
    raise SystemExit("usage: ci_resource_monitor.py OUTPUT.jsonl")

output_path = sys.argv[1]
stopping = False


def stop(_signum, _frame):
    global stopping
    stopping = True


signal.signal(signal.SIGTERM, stop)
signal.signal(signal.SIGINT, stop)


def read_key_values(path):
    values = {}
    try:
        with open(path, encoding="ascii") as stream:
            for line in stream:
                fields = line.split()
                if len(fields) >= 2:
                    try:
                        values[fields[0].rstrip(":")] = int(fields[1])
                    except ValueError:
                        continue
    except OSError:
        pass
    return values


def read_diskstats(path="/proc/diskstats"):
    devices = {}
    try:
        with open(path, encoding="ascii") as stream:
            for line in stream:
                fields = line.split()
                if len(fields) < 14:
                    continue
                try:
                    major, minor = int(fields[0]), int(fields[1])
                    values = [int(value) for value in fields[3:14]]
                except ValueError:
                    continue
                devices[fields[2]] = {
                    "major": major,
                    "minor": minor,
                    "reads_completed": values[0],
                    "sectors_read": values[2],
                    "read_ms": values[3],
                    "writes_completed": values[4],
                    "sectors_written": values[6],
                    "write_ms": values[7],
                    "in_flight": values[8],
                    "io_ms": values[9],
                    "weighted_io_ms": values[10],
                }
    except OSError:
        pass
    return devices


def read_pressure(path):
    result = {}
    try:
        with open(path, encoding="ascii") as stream:
            for line in stream:
                fields = line.split()
                if not fields:
                    continue
                values = {}
                for field in fields[1:]:
                    key, separator, value = field.partition("=")
                    if separator:
                        values[key] = float(value) if key.startswith("avg") else int(value)
                result[fields[0]] = values
    except OSError:
        pass
    return result


started = time.monotonic()
previous_vmstat = {}\nprevious_diskstats = {}\nprevious_sample_time = None
with open(output_path, "w", encoding="utf-8", buffering=1) as output:
    while not stopping:
        meminfo = read_key_values("/proc/meminfo")
        vmstat = read_key_values("/proc/vmstat")
        diskstats = read_diskstats()\n        sample_time = time.monotonic()\n        interval_seconds = (sample_time - previous_sample_time) if previous_sample_time is not None else 0\n        sample = {
            "unix_time_ns": time.time_ns(),
            "elapsed_seconds": round(time.monotonic() - started, 3),
            "memory_kib": {
                key: meminfo[key]
                for key in ("MemTotal", "MemAvailable", "SwapTotal", "SwapFree", "Dirty", "Writeback")
                if key in meminfo
            },
            "diskstats": diskstats,\n            "vmstat_pages": {
                key: vmstat[key]
                for key in ("pswpin", "pswpout", "pgmajfault", "oom_kill")
                if key in vmstat
            },
            "pressure": {
                resource: read_pressure(f"/proc/pressure/{resource}")
                for resource in ("memory", "io")
            },
        }
        output.write(json.dumps(sample, separators=(",", ":")) + "\n")

        available_mib = meminfo.get("MemAvailable", 0) / 1024
        swap_total = meminfo.get("SwapTotal", 0)
        swap_used = swap_total - meminfo.get("SwapFree", swap_total)
        swap_in_delta = vmstat.get("pswpin", 0) - previous_vmstat.get("pswpin", vmstat.get("pswpin", 0))
        swap_out_delta = vmstat.get("pswpout", 0) - previous_vmstat.get("pswpout", vmstat.get("pswpout", 0))
        memory_psi = sample["pressure"]["memory"].get("some", {}).get("avg10", 0)
        io_psi = sample["pressure"]["io"].get("some", {}).get("avg10", 0)
        print(
            "[resource] t={:.0f}s MemAvailable={:.0f}MiB SwapUsed={:.0f}/{:.0f}MiB ".format(
                sample["elapsed_seconds"], available_mib, swap_used / 1024, swap_total / 1024
            )
            + "swap-in={}pages/s swap-out={}pages/s PSI(memory/io)={:.2f}%/{:.2f}%".format(
                swap_in_delta, swap_out_delta, memory_psi, io_psi
            ),
            flush=True,
        )
        previous_vmstat = vmstat
        time.sleep(1)
