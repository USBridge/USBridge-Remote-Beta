#!/bin/bash
# fetch_onnxruntime.sh <output_dir> [target_os]
#
# Downloads a redistributable ONNX Runtime shared library and drops it as
# <output_dir>/<lib name>, ready to be bundled directly into an app package
# (build_macos.sh's Contents/Frameworks/, build_linux.sh's AppImage AppDir,
# build_windows.sh's dist folder next to the .exe, ...).
#
# darwin/linux get the plain CPU-only "onnxruntime" wheel -- CoreML
# (darwin, always present in the OS) and OpenVINO (linux, opt-in via
# setup_localui.sh) are this package's accelerator paths there, and neither
# needs anything beyond the CPU wheel's onnxruntime lib itself.
#
# windows instead gets the "onnxruntime-directml" wheel: a build of the same
# onnxruntime.dll with the DirectML execution provider compiled in, plus the
# DirectML.dll it dispatches through. DirectML is a DirectX 12 EP -- vendor-
# agnostic (NVIDIA/AMD/Intel GPUs all go through the one D3D12 driver stack
# Windows already has), so unlike OpenVINO this needs no separate runtime
# install, just this one extra DLL sitting next to onnxruntime.dll (see
# internal/localui/onnx.go's acceleratorEP -- benchmarked live on an
# NVIDIA+AMD dual-GPU box, ~2.5x end-to-end win over CPU). Both DLLs come
# from the same wheel and ship in lockstep, so there's no version-skew risk
# fetching them together like this.
#
# Unlike the plain CPU "onnxruntime" wheel (mingw/self-contained, no extra
# runtime deps -- see the "Why the PyPI wheel" paragraph below),
# onnxruntime-directml's onnxruntime.dll is MSVC-built and dynamically
# links the Visual C++ redistributable (MSVCP140.dll, MSVCP140_1.dll,
# VCRUNTIME140.dll, VCRUNTIME140_1.dll) -- present on most real Windows
# boxes already (bundled with countless other apps/games) but NOT
# guaranteed, and confirmed absent from this repo's own clean MSYS2/
# mingw-w64 build environment via build_windows.sh's post-build dependency
# walk. Rather than requiring every user to separately install Microsoft's
# vc_redist.exe first, this fetches those 4 DLLs from the "msvc-runtime"
# PyPI wheel (a redistribution of Microsoft's own VC++ Redistributable
# package built specifically so Python wheels needing it can bundle it
# app-locally instead of depending on a system-wide install -- same
# "self-contained redistributable" reasoning as the onnxruntime wheel
# itself) and bundles them flat alongside onnxruntime.dll/DirectML.dll.
#
# target_os is one of "darwin", "linux", "windows" -- defaults to the build
# host's own OS (so `./fetch_onnxruntime.sh out` on a Mac fetches the macOS
# lib) but can be overridden for a cross build, e.g. build_windows.sh cross-
# compiles Windows binaries via mingw-w64 from a Linux or macOS host (see
# its own header comment), where `uname -s` would report the *host*, not
# the target the fetched .dll actually needs to run on.
#
# Why the PyPI wheel and not Homebrew/apt/a system package: those link
# against whatever version of protobuf/abseil/etc. happens to be installed
# on the *build* machine at that moment, which is exactly the trap this
# script exists to avoid -- `brew upgrade protobuf` on a dev box silently
# broke a Homebrew-linked libonnxruntime.dylib that used to work, because
# it was never protobuf-version-pinned in the first place (see git log for
# the incident this script was added to fix). PyPI's manylinux/macOS/
# Windows wheels are built to be self-contained redistributables: `otool
# -L`/`ldd` on the extracted macOS/Linux library shows only OS-provided
# system libraries as dependencies, nothing under /opt/homebrew or a
# distro package -- verified for the macOS arm64 wheel before wiring this
# into build_macos.sh.
#
# This intentionally does NOT install the OpenVINO execution provider
# (onnxruntime-openvino, Linux/Intel-iGPU only) -- that stays an opt-in
# developer enhancement via setup_localui.sh's existing flow (which needs
# real system access to pick the right Intel driver stack). DirectML on
# Windows is different: it's bundled unconditionally above because it needs
# no system-specific setup at all -- any Windows box's own D3D12 driver is
# enough -- so there's no reason to make it opt-in the way Linux's Intel-only
# OpenVINO has to be. This script's job is otherwise narrow: guarantee a
# bundled app has a *working baseline* ONNX Runtime with zero assumptions
# about what's installed on the machine it ends up running on, matching the
# ONNX models already committed at internal/localui/models/ (see that
# directory's README).
#
# Usage: ./scripts/fetch_onnxruntime.sh <output_dir> [darwin|linux|windows]
# Requires: python3 or python (with pip), unzip -- no docker, no network
# access beyond PyPI, no access to the private usbridge (device) repo.
set -euo pipefail

OUT_DIR="${1:?usage: fetch_onnxruntime.sh <output_dir> [darwin|linux|windows]}"
mkdir -p "$OUT_DIR"

# Normalizes uname -s to darwin/linux/windows -- MSYS2/Git Bash (the shell
# build_windows.sh itself can run under, natively on Windows) reports
# "MINGW64_NT-..."/"MSYS_NT-...", not "Windows".
normalize_os() {
    case "$1" in
        Darwin) echo darwin ;;
        Linux) echo linux ;;
        MINGW*|MSYS*|CYGWIN*) echo windows ;;
        *) echo "$1" ;;
    esac
}

HOST_OS="$(normalize_os "$(uname -s)")"
TARGET_OS="${2:-$HOST_OS}"

case "$TARGET_OS" in
    darwin)  LIB_NAME="libonnxruntime.dylib" ;;
    linux)   LIB_NAME="libonnxruntime.so" ;;
    windows) LIB_NAME="onnxruntime.dll" ;;
    *)
        echo "!! fetch_onnxruntime.sh: unsupported target OS '$TARGET_OS' -- add a case here" >&2
        exit 1
        ;;
esac

# Prefer python3, but fall back to plain "python" -- some environments
# (e.g. MSYS2's mingw-w64-ucrt-x86_64-python package, used by
# build_windows.sh's CI job) only provide the latter. Each candidate is
# verified to actually have a working `pip` before being accepted, not just
# to exist on PATH: MSYS2's own python3 (the first thing PATH resolves to
# inside its UCRT64 shell -- the shell build_windows.sh itself runs under
# on a native Windows dev box) ships with no pip and no MSYS2 package
# providing one for it either, while a separate real Windows Python install
# (python.org / Microsoft Store, common on any dev machine, just not on
# MSYS2's own PATH) usually does -- confirmed exactly this split on a real
# dev box: MSYS2 UCRT64's python3 -> "No module named pip", while
# AppData\Local\Programs\Python\Python3xx's python3 (reachable once PATH
# widens to include Windows' own PATH entries, e.g. this script invoked
# from plain Git Bash rather than the MSYS2 UCRT64 shell) worked fine. Only
# the have-a-working-pip candidate is usable, so probe rather than assume.
PYTHON_BIN=""
for _cand in python3 python; do
    if command -v "$_cand" >/dev/null 2>&1 && "$_cand" -m pip --version >/dev/null 2>&1; then
        PYTHON_BIN="$_cand"
        break
    fi
done
# Last resort: a real Windows Python install under the current user's
# AppData, even if it's not on this shell's PATH at all (MSYS2 shells don't
# inherit the Windows user PATH by default). Picks the newest version dir
# if more than one is installed.
if [ -z "$PYTHON_BIN" ]; then
    for _cand in $(ls -d /c/Users/*/AppData/Local/Programs/Python/Python3*/python.exe 2>/dev/null | sort -rV); do
        if "$_cand" -m pip --version >/dev/null 2>&1; then
            PYTHON_BIN="$_cand"
            break
        fi
    done
fi
if [ -z "$PYTHON_BIN" ]; then
    echo "!! fetch_onnxruntime.sh: no python3/python with a working pip found (checked PATH and AppData\\Local\\Programs\\Python)" >&2
    exit 1
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/wheel"

# windows fetches "onnxruntime-directml" instead of plain "onnxruntime" --
# see the header comment. Both are self-contained, no-deps wheels; only the
# package name differs.
case "$TARGET_OS" in
    windows) PKG="onnxruntime-directml" ;;
    *)       PKG="onnxruntime" ;;
esac

echo "==> Fetching $PKG (PyPI, self-contained) for $TARGET_OS..."

DOWNLOAD_ARGS=(--no-deps --only-binary=:all: -d "$WORK/wheel")
if [ "$TARGET_OS" != "$HOST_OS" ]; then
    # Cross-fetch: the target isn't what this script is running on (e.g.
    # Windows, cross-compiled via mingw-w64 from a Linux/macOS CI runner).
    # --python-version pins a CPython ABI that PyPI still ships a wheel
    # for -- bump it if a future onnxruntime release drops cp311.
    case "$TARGET_OS" in
        windows) DOWNLOAD_ARGS+=(--platform win_amd64 --python-version 311) ;;
        linux)   DOWNLOAD_ARGS+=(--platform manylinux_2_28_x86_64 --python-version 311) ;;
        darwin)  DOWNLOAD_ARGS+=(--platform macosx_12_0_arm64 --python-version 311) ;;
    esac
fi
# Native fetch (TARGET_OS == HOST_OS) intentionally passes no --platform/
# --python-version: pip resolves the running interpreter's own platform
# tag, which is simpler and more future-proof than hardcoding one.

"$PYTHON_BIN" -m pip download -q --disable-pip-version-check "${DOWNLOAD_ARGS[@]}" "$PKG"

WHEEL="$(find "$WORK/wheel" -maxdepth 1 -name '*.whl' -print -quit)"
if [ -z "$WHEEL" ]; then
    echo "!! fetch_onnxruntime.sh: pip download produced no wheel" >&2
    exit 1
fi
unzip -q "$WHEEL" 'onnxruntime/capi/*' -d "$WORK/pkg"

case "$TARGET_OS" in
    darwin)  FIND_PATTERN="libonnxruntime.*.dylib" ;; # e.g. libonnxruntime.1.29.0.dylib
    linux)   FIND_PATTERN="libonnxruntime.so*" ;;      # e.g. libonnxruntime.so.1.29.0
    windows) FIND_PATTERN="onnxruntime.dll" ;;
esac
SRC="$(find "$WORK/pkg/onnxruntime/capi" -maxdepth 1 -name "$FIND_PATTERN" -print -quit)"
if [ -z "$SRC" ]; then
    echo "!! fetch_onnxruntime.sh: no $FIND_PATTERN found under the onnxruntime wheel's capi/ dir" >&2
    exit 1
fi

cp "$SRC" "$OUT_DIR/$LIB_NAME"
chmod 755 "$OUT_DIR/$LIB_NAME"
echo "    -> $OUT_DIR/$LIB_NAME ($(du -h "$OUT_DIR/$LIB_NAME" | cut -f1), from $(basename "$SRC"))"

# DirectML.dll: onnxruntime.dll dlopen's this at the moment
# AppendExecutionProviderDirectML actually gets called (internal/localui/
# onnx.go), not at process load -- so it has to sit next to onnxruntime.dll
# in the same bundled output dir, same as onnxruntime_providers_shared.dll
# does for the OpenVINO EP on Linux (setup_localui.sh's runtime dir).
if [ "$TARGET_OS" = "windows" ]; then
    DML_SRC="$(find "$WORK/pkg/onnxruntime/capi" -maxdepth 1 -name "DirectML.dll" -print -quit)"
    if [ -z "$DML_SRC" ]; then
        echo "!! fetch_onnxruntime.sh: onnxruntime-directml wheel had no DirectML.dll -- DirectML EP will silently fail to init at runtime" >&2
        exit 1
    fi
    cp "$DML_SRC" "$OUT_DIR/DirectML.dll"
    chmod 755 "$OUT_DIR/DirectML.dll"
    echo "    -> $OUT_DIR/DirectML.dll ($(du -h "$OUT_DIR/DirectML.dll" | cut -f1))"

    # VC++ redistributable DLLs onnxruntime.dll needs at load time -- see
    # the header comment. Downloaded separately since they're not part of
    # the onnxruntime-directml wheel itself.
    echo "==> Fetching msvc-runtime (PyPI, VC++ redistributable DLLs onnxruntime.dll needs)..."
    MSVC_DOWNLOAD_ARGS=(--no-deps --only-binary=:all: -d "$WORK/msvc_wheel")
    if [ "$TARGET_OS" != "$HOST_OS" ]; then
        MSVC_DOWNLOAD_ARGS+=(--platform win_amd64 --python-version 311)
    fi
    mkdir -p "$WORK/msvc_wheel"
    "$PYTHON_BIN" -m pip download -q --disable-pip-version-check "${MSVC_DOWNLOAD_ARGS[@]}" msvc-runtime
    MSVC_WHEEL="$(find "$WORK/msvc_wheel" -maxdepth 1 -name '*.whl' -print -quit)"
    if [ -z "$MSVC_WHEEL" ]; then
        echo "!! fetch_onnxruntime.sh: pip download produced no msvc-runtime wheel -- onnxruntime.dll may fail to load on a machine without the VC++ redistributable already installed" >&2
    else
        unzip -q "$MSVC_WHEEL" '*.data/data/Scripts/*.dll' -d "$WORK/msvc_pkg"
        MSVC_SCRIPTS_DIR="$(find "$WORK/msvc_pkg" -type d -name Scripts -print -quit)"
        for dll in msvcp140.dll msvcp140_1.dll vcruntime140.dll vcruntime140_1.dll; do
            SRC_DLL="$(find "$MSVC_SCRIPTS_DIR" -maxdepth 1 -iname "$dll" -print -quit)"
            if [ -z "$SRC_DLL" ]; then
                echo "!! fetch_onnxruntime.sh: msvc-runtime wheel had no $dll" >&2
                continue
            fi
            DEST_NAME="$(basename "$SRC_DLL")"
            cp "$SRC_DLL" "$OUT_DIR/$DEST_NAME"
            chmod 755 "$OUT_DIR/$DEST_NAME"
        done
        echo "    -> $OUT_DIR/{msvcp140,msvcp140_1,vcruntime140,vcruntime140_1}.dll"
    fi
fi
