#!/usr/bin/env python3
"""Sample Linux memory, swap, and PSI counters while a CI benchmark runs."""

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
with open(output_path, "w", encoding="utf-8", buffering=1) as output:
    while not stopping:
        meminfo = read_key_values("/proc/meminfo")
        vmstat = read_key_values("/proc/vmstat")
        sample = {
            "unix_time_ns": time.time_ns(),
            "elapsed_seconds": round(time.monotonic() - started, 3),
            "memory_kib": {
                key: meminfo[key]
                for key in ("MemTotal", "MemAvailable", "SwapTotal", "SwapFree", "Dirty", "Writeback")
                if key in meminfo
            },
            "vmstat_pages": {
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
        time.sleep(1)
