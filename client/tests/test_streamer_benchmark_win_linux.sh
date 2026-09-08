#!/bin/bash
# Head-to-head streamer benchmark: Linux client (this machine) vs a real
# Windows USBridge Agent host, over the real LAN. Runs the same measurement
# window through both bundled game-streaming hosts (Sunshine, then RustShine)
# and collects client-reported fps, forced-reconnect / unrecoverable-frame
# events, and network RTT/jitter (ping) for each -- the Linux/Windows
# counterpart to test_streamer_benchmark.sh (Android client / Mac server).
#
# Unlike the Android/Mac script, this one does not need adb or a saved app
# connection: it drives the Windows agent's admin API remotely over SSH
# (curl --unix-socket against its local admin.sock, since that socket has
# no separate auth -- see agent/internal/adminapi/server.go) and launches
# the Linux client directly with a usbridge:// deeplink
# (immediate=true, see client/cmd -- deeplink_desktop.go).
#
# Content fairness: the Windows sshd service runs commands in Session 0
# (non-interactive), same isolation issue this codebase already worked
# around for Sunshine's own capture path (see sunshine_backend.go's
# useSunshineSessionBroker doc comment) -- so a plain `ssh host ffplay ...`
# would never actually appear on the real desktop DXGI/duplication captures.
# --with-pattern uses `schtasks /IT` (interactive-only-if-logged-on) to hop
# ffplay.exe into the real console session instead, playing an ffmpeg
# lavfi testsrc2 motion pattern (no video file needed, self-looping by
# construction). Without that flag, the pass just measures whatever is
# already on the real Windows desktop -- fine for a realistic stability
# read, not apples-to-apples content across passes.
#
# Usage: ./tests/test_streamer_benchmark_win_linux.sh <win_host> <master_key> \
#            [duration_seconds] [ssh_alias] [outdir] [--with-pattern]

set -u

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

WIN_HOST="${1:?win_host required (agent LAN IP)}"
MASTER_KEY="${2:?master_key required (see saved connection or USBridge Master QR Sync)}"
DURATION="${3:-180}"
SSH_ALIAS="${4:-rustshine-win}"
TS="$(date +%Y%m%d_%H%M%S)"
OUTDIR="${5:-$REPO_ROOT/tests/bench_output/$TS}"
WITH_PATTERN=0
for a in "$@"; do [ "$a" = "--with-pattern" ] && WITH_PATTERN=1; done

CLIENT_BIN="$REPO_ROOT/dist/linux/usbridge-client"
WIN_SOCK='C:\Users\bogom\AppData\Roaming\usbridge-agent\admin.sock'
WIN_FFPLAY='C:\Users\bogom\AppData\Local\Microsoft\WinGet\Packages\Gyan.FFmpeg_Microsoft.Winget.Source_8wekyb3d8bbwe\ffmpeg-8.0.1-full_build\bin\ffplay.exe'
PATTERN_TASK="usbridge_bench_pattern"

mkdir -p "$OUTDIR"
log() { echo "[$(date +%H:%M:%S)] $*"; }

# --- remote admin API (over SSH, against the agent's local unix socket) ---
win_admin_get() { ssh "$SSH_ALIAS" "curl -s --unix-socket \"$WIN_SOCK\" http://localhost$1" 2>/dev/null; }
win_admin_post() { ssh "$SSH_ALIAS" "curl -s --unix-socket \"$WIN_SOCK\" -X POST -d '$2' http://localhost$1" 2>/dev/null; }

switch_backend() {
    local kind="$1"
    log "Switching backend -> $kind"
    win_admin_post "/token/set-stream-backend" "{\"kind\":\"$kind\"}" >/dev/null
    for i in $(seq 1 30); do
        active=$(win_admin_get "/token/entitlement-status" | python3 -c "import sys,json;print(json.load(sys.stdin).get('active_backend',''))" 2>/dev/null)
        [ "$active" = "$kind" ] && { log "  backend live: $active"; return 0; }
        sleep 1
    done
    log "  ⚠️ backend switch to $kind not confirmed after 30s (continuing anyway)"
}

start_pattern() {
    [ "$WITH_PATTERN" = "1" ] || return 0
    log "  starting motion pattern on Windows desktop (interactive session)..."
    ssh "$SSH_ALIAS" "schtasks /delete /tn $PATTERN_TASK /f >nul 2>&1 & schtasks /create /tn $PATTERN_TASK /tr \"'$WIN_FFPLAY' -fs -an -loglevel quiet -f lavfi -i testsrc2=size=1920x1080:rate=60\" /sc onstart /ru bogom /it /f >nul & schtasks /run /tn $PATTERN_TASK" >/dev/null 2>&1
    sleep 3
}

stop_pattern() {
    [ "$WITH_PATTERN" = "1" ] || return 0
    ssh "$SSH_ALIAS" "taskkill /F /IM ffplay.exe >nul 2>&1 & schtasks /delete /tn $PATTERN_TASK /f >nul 2>&1" >/dev/null 2>&1
}

run_pass() {
    local kind="$1"
    local pass_dir="$OUTDIR/$kind"
    local log_file="$pass_dir/app.log"
    mkdir -p "$pass_dir"
    log "=================================================="
    log " PASS: $kind"
    log "=================================================="

    switch_backend "$kind"
    start_pattern

    pkill -f "$CLIENT_BIN" 2>/dev/null || true
    sleep 1
    : > "${USBRIDGE_LOG_DIR:-$pass_dir}/app.log" 2>/dev/null || true

    T0=$(date +%s.%N)
    USBRIDGE_LOG_DIR="$pass_dir" "$CLIENT_BIN" \
        "usbridge://connect?internal_host=${WIN_HOST}&master_key=${MASTER_KEY}&protocol=direct&immediate=true" \
        >/dev/null 2>&1 &
    CLIENT_PID=$!
    log "launched client pid=$CLIENT_PID"

    connected=false
    for i in $(seq 1 60); do
        if grep -q "✅ Connected to USBridge via" "$log_file" 2>/dev/null; then
            connected=true
            break
        fi
        if ! kill -0 $CLIENT_PID 2>/dev/null; then
            log "  ❌ client exited before connecting"
            break
        fi
        sleep 0.5
    done
    if ! $connected; then
        log "  ❌ video never started for $kind — aborting this pass"
        kill $CLIENT_PID 2>/dev/null
        echo "FAILED_TO_CONNECT" > "$pass_dir/status.txt"
        stop_pattern
        return 1
    fi
    CONNECT_DELTA=$(python3 -c "print(f'{$(date +%s.%N) - $T0:.1f}')")
    log "  ✅ video live ${CONNECT_DELTA}s after launch"

    log "  Running ${DURATION}s window: sampling network RTT + watching stream..."
    ping -c "$DURATION" -i 1 "$WIN_HOST" > "$pass_dir/ping.txt" 2>&1 &
    PING_PID=$!

    for elapsed in $(seq 10 10 "$DURATION"); do
        sleep 10
        fps_now=$(grep -oE "fps=[0-9.]+" "$log_file" 2>/dev/null | tail -1)
        rc=$(grep -c "forcing reconnect" "$log_file" 2>/dev/null); rc=${rc:-0}
        log "  [${elapsed}s/${DURATION}s] latest ${fps_now:-fps=?}  reconnects_so_far=$rc"
        if ! kill -0 $CLIENT_PID 2>/dev/null; then
            log "  ⚠️ client exited mid-window"
            break
        fi
    done
    wait $PING_PID 2>/dev/null

    kill $CLIENT_PID 2>/dev/null
    wait $CLIENT_PID 2>/dev/null
    stop_pattern

    echo "OK connect_delay=${CONNECT_DELTA}s" > "$pass_dir/status.txt"
    log "  pass complete -> $pass_dir"
}

if [ ! -x "$CLIENT_BIN" ]; then
    echo "❌ Linux client not built at $CLIENT_BIN — run scripts/build_linux.sh first" >&2
    exit 1
fi

echo "=================================================="
echo " USBridge streamer benchmark: Sunshine vs RustShine"
echo " Linux client (this machine)  <->  Windows agent: $WIN_HOST (ssh: $SSH_ALIAS)"
echo " Duration per backend: ${DURATION}s   pattern-injection: $([ "$WITH_PATTERN" = "1" ] && echo on || echo off)"
echo " Output: $OUTDIR"
echo "=================================================="

run_pass "sunshine"
sleep 5
run_pass "rustshine"

echo ""
echo "=================================================="
echo " Benchmark complete. Raw data in: $OUTDIR"
echo " Graph it: python3 $REPO_ROOT/tests/plot_streamer_benchmark.py $OUTDIR"
echo "=================================================="
