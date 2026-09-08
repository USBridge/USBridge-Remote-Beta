#!/usr/bin/env python3
"""Plot fps / ping-RTT / reconnect timelines from a benchmark run produced by
either test_streamer_benchmark.sh (Android/Mac, logcat) or
test_streamer_benchmark_win_linux.sh (Linux/Windows, app.log) -- whichever
per-backend subdirectories ("sunshine/", "rustshine/") are found under the
given output dir are compared side by side.

Usage: plot_streamer_benchmark.py <bench_output_dir> [out.png]
"""
import sys
import re
import os
from datetime import datetime

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

FPS_RE = re.compile(r"fps=([0-9.]+)")
# logrus TextFormatter (desktop client's app.log): time="2006-01-02 15:04:05" ...
LOGRUS_TS_RE = re.compile(r'time="([0-9-]+ [0-9:]+)"')
# Android logcat -v time: MM-DD HH:MM:SS.mmm
LOGCAT_TS_RE = re.compile(r"^(\d\d-\d\d \d\d:\d\d:\d\d\.\d\d\d)")
RECONNECT_RE = re.compile(r"forcing reconnect|Unrecoverable frame")
PING_RE = re.compile(r"time[=<]([0-9.]+)\s*ms")
PING_SEQ_RE = re.compile(r"icmp_seq=(\d+)")


def parse_ts(line):
    m = LOGRUS_TS_RE.search(line)
    if m:
        try:
            return datetime.strptime(m.group(1), "%Y-%m-%d %H:%M:%S")
        except ValueError:
            return None
    m = LOGCAT_TS_RE.search(line)
    if m:
        try:
            return datetime.strptime(m.group(1), "%m-%d %H:%M:%S.%f")
        except ValueError:
            return None
    return None


def find_log_file(pass_dir):
    for name in ("app.log", "logcat.txt"):
        p = os.path.join(pass_dir, name)
        if os.path.isfile(p):
            return p
    return None


def load_pass(pass_dir):
    log_path = find_log_file(pass_dir)
    fps_t, fps_v = [], []
    reconnect_t = []
    t0 = None
    if log_path:
        with open(log_path, errors="replace") as f:
            for line in f:
                ts = parse_ts(line)
                if ts and t0 is None:
                    t0 = ts
                m = FPS_RE.search(line)
                if m and ts:
                    fps_t.append((ts - t0).total_seconds() if t0 else 0)
                    fps_v.append(float(m.group(1)))
                if RECONNECT_RE.search(line) and ts:
                    reconnect_t.append((ts - t0).total_seconds() if t0 else 0)

    ping_path = os.path.join(pass_dir, "ping.txt")
    ping_t, ping_v = [], []
    if os.path.isfile(ping_path):
        with open(ping_path, errors="replace") as f:
            for line in f:
                mv = PING_RE.search(line)
                ms = PING_SEQ_RE.search(line)
                if mv and ms:
                    ping_t.append(int(ms.group(1)))
                    ping_v.append(float(mv.group(1)))

    return {
        "fps_t": fps_t, "fps_v": fps_v,
        "reconnect_t": reconnect_t,
        "ping_t": ping_t, "ping_v": ping_v,
    }


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(1)
    outdir = sys.argv[1]
    out_png = sys.argv[2] if len(sys.argv) > 2 else os.path.join(outdir, "benchmark.png")

    backends = [b for b in ("sunshine", "rustshine") if os.path.isdir(os.path.join(outdir, b))]
    if not backends:
        print(f"No sunshine/ or rustshine/ subdir found under {outdir}")
        sys.exit(1)

    data = {b: load_pass(os.path.join(outdir, b)) for b in backends}
    colors = {"sunshine": "#2ca02c", "rustshine": "#1f77b4"}

    fig, (ax_fps, ax_ping) = plt.subplots(2, 1, figsize=(12, 8), sharex=False)

    for b in backends:
        d = data[b]
        c = colors.get(b, None)
        if d["fps_t"]:
            avg = sum(d["fps_v"]) / len(d["fps_v"])
            ax_fps.plot(d["fps_t"], d["fps_v"], label=f"{b} (avg {avg:.1f})", color=c, marker="o", markersize=3)
        for rt in d["reconnect_t"]:
            ax_fps.axvline(rt, color=c, linestyle="--", alpha=0.5)
    ax_fps.set_title("Client-reported FPS over time (dashed = forced reconnect)")
    ax_fps.set_xlabel("seconds since stream start")
    ax_fps.set_ylabel("fps")
    ax_fps.legend()
    ax_fps.grid(True, alpha=0.3)

    for b in backends:
        d = data[b]
        c = colors.get(b, None)
        if d["ping_t"]:
            avg = sum(d["ping_v"]) / len(d["ping_v"])
            ax_ping.plot(d["ping_t"], d["ping_v"], label=f"{b} (avg {avg:.1f}ms)", color=c, marker=".", markersize=2)
    ax_ping.set_title("Network RTT (ping) over time")
    ax_ping.set_xlabel("icmp_seq (~seconds)")
    ax_ping.set_ylabel("ms")
    ax_ping.legend()
    ax_ping.grid(True, alpha=0.3)

    fig.suptitle(f"USBridge streamer benchmark — {os.path.basename(outdir)}")
    fig.tight_layout()
    fig.savefig(out_png, dpi=130)
    print(f"Wrote {out_png}")

    for b in backends:
        d = data[b]
        print(f"\n{b}:")
        if d["fps_v"]:
            print(f"  fps: avg={sum(d['fps_v'])/len(d['fps_v']):.1f} min={min(d['fps_v']):.1f} max={max(d['fps_v']):.1f} samples={len(d['fps_v'])}")
        else:
            print("  fps: no samples found")
        if d["ping_v"]:
            print(f"  ping: avg={sum(d['ping_v'])/len(d['ping_v']):.2f}ms max={max(d['ping_v']):.2f}ms samples={len(d['ping_v'])}")
        print(f"  reconnects: {len(d['reconnect_t'])}")


if __name__ == "__main__":
    main()
