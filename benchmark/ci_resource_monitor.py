#!/usr/bin/env python3
"""Sample Linux memory, swap, PSI, and block-device counters during CI benchmarks."""

import json
import signal
import sys
import time

if len(sys.argv) not in (2, 4):
    raise SystemExit("usage: ci_resource_monitor.py OUTPUT.jsonl [WORKER_PID_FILE BENCHMARK_PID_FILE]")

output_path = sys.argv[1]
pid_files = {"worker": sys.argv[2] if len(sys.argv) == 4 else None, "benchmark": sys.argv[3] if len(sys.argv) == 4 else None}
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


def read_diskstats(path="/proc/diskstats"):
    """Return Linux block-device counters, keyed by kernel device name."""
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

def read_process_memory(pid_file):
    if not pid_file:
        return None
    try:
        with open(pid_file, encoding="ascii") as stream:
            pid = int(stream.read().strip())
    except (OSError, ValueError):
        return None
    base = f"/proc/{pid}"
    status = read_key_values(f"{base}/status")
    rollup = read_key_values(f"{base}/smaps_rollup")
    if not status:
        return {"pid": pid, "available": False}
    keys = ("VmRSS", "RssAnon", "RssFile", "RssShmem", "VmSwap")
    smaps_keys = ("Pss", "Private_Clean", "Private_Dirty", "Shared_Clean", "Shared_Dirty", "Anonymous", "Swap")
    return {"pid": pid, "available": True, "status_kib": {key: status[key] for key in keys if key in status}, "smaps_rollup_kib": {key: rollup[key] for key in smaps_keys if key in rollup}}


def read_cgroup_memory():
    values = read_key_values("/sys/fs/cgroup/memory.stat")
    keys = ("anon", "file", "kernel", "kernel_stack", "pagetables", "sock", "shmem", "file_mapped", "file_dirty", "file_writeback", "slab", "slab_reclaimable", "slab_unreclaimable")
    return {key: values[key] for key in keys if key in values}


started = time.monotonic()
previous_vmstat = {}
previous_diskstats = {}
previous_sample_time = None
with open(output_path, "w", encoding="utf-8", buffering=1) as output:
    while not stopping:
        meminfo = read_key_values("/proc/meminfo")
        vmstat = read_key_values("/proc/vmstat")
        diskstats = read_diskstats()
        sample_time = time.monotonic()
        interval_seconds = (
            sample_time - previous_sample_time if previous_sample_time is not None else 0
        )
        sample = {
            "unix_time_ns": time.time_ns(),
            "elapsed_seconds": round(sample_time - started, 3),
            "memory_kib": {
                key: meminfo[key]
                for key in ("MemTotal", "MemAvailable", "SwapTotal", "SwapFree", "Cached", "Buffers", "SReclaimable", "SUnreclaim", "Active", "Inactive", "Active(anon)", "Inactive(anon)", "Active(file)", "Inactive(file)", "Dirty", "Writeback")
                if key in meminfo
            },
            "vmstat_pages": {
                key: vmstat[key]
                for key in ("pswpin", "pswpout", "pgmajfault", "oom_kill")
                if key in vmstat
            },
            "diskstats": diskstats,
            "process_memory": {name: read_process_memory(path) for name, path in pid_files.items()},
            "cgroup_memory_kib": read_cgroup_memory(),
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
        for process_name, process in sample["process_memory"].items():
            if process and process.get("available"):
                rss = process.get("status_kib", {}).get("VmRSS", 0) / 1024
                anon = process.get("status_kib", {}).get("RssAnon", 0) / 1024
                file_backed = process.get("status_kib", {}).get("RssFile", 0) / 1024
                print(f"[process] {process_name} pid={process['pid']} RSS={rss:.1f}MiB anon={anon:.1f}MiB file={file_backed:.1f}MiB", flush=True)

        if interval_seconds > 0:
            device_lines = []
            for name, current in diskstats.items():
                previous = previous_diskstats.get(name)
                if previous is None:
                    continue
                reads = current["reads_completed"] - previous["reads_completed"]
                writes = current["writes_completed"] - previous["writes_completed"]
                sectors_read = current["sectors_read"] - previous["sectors_read"]
                sectors_written = current["sectors_written"] - previous["sectors_written"]
                read_ms = current["read_ms"] - previous["read_ms"]
                write_ms = current["write_ms"] - previous["write_ms"]
                io_ms = current["io_ms"] - previous["io_ms"]
                weighted_ms = current["weighted_io_ms"] - previous["weighted_io_ms"]
                operations = reads + writes
                if not (operations or current["in_flight"]):
                    continue
                await_ms = (read_ms + write_ms) / operations if operations > 0 else 0
                util_percent = io_ms / (interval_seconds * 1000) * 100
                average_queue = weighted_ms / (interval_seconds * 1000)
                device_lines.append(
                    "{}:r={:.1f}/s,w={:.1f}/s,read={:.2f}MiB/s,write={:.2f}MiB/s,"
                    "await={:.2f}ms,aqu-sz={:.2f},util={:.1f}%".format(
                        name,
                        reads / interval_seconds,
                        writes / interval_seconds,
                        sectors_read * 512 / interval_seconds / (1024 * 1024),
                        sectors_written * 512 / interval_seconds / (1024 * 1024),
                        await_ms,
                        average_queue,
                        util_percent,
                    )
                )
            if device_lines:
                print("[disk] " + " | ".join(device_lines), flush=True)

        previous_vmstat = vmstat
        previous_diskstats = diskstats
        previous_sample_time = sample_time
        time.sleep(1)
