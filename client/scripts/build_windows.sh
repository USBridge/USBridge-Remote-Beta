#!/bin/bash
# Build USBridge Client for Windows: binary + dist folder with libraries
#
# Requirements:
#   Mandatory:  Go, mingw-w64, Fyne
#   For Moonlight (HW decode + WASAPI audio):
#     FFmpeg MinGW DLLs (avcodec, avutil, swscale, opus):
#       Download: https://github.com/BtbN/FFmpeg-Builds/releases (ffmpeg-master-latest-win64-gpl-shared.zip)
#       Extract and specify: export FFMPEG_ROOT="/path/to/ffmpeg-mingw"
#   Optional:   python3 (pip) -- fetches the local ui.parse/AI Vision ONNX
#               runtime DLL (see fetch_onnxruntime.sh); its absence only
#               disables that one feature, the rest of the build is
#               unaffected. Works whether this script runs on a Linux/macOS
#               host cross-compiling via mingw-w64, or natively under
#               MSYS2/Git Bash on Windows itself.
#
# GStreamer is not needed on Windows: Moonlight uses libavcodec (D3D11VA/SW)
# and the QR scanner uses Media Foundation directly.

set -e

SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "=> Building Moonlight Core..."
"$SCRIPTS_DIR/build_moonlight.sh" || { echo "❌ Failed to build Moonlight Core"; exit 1; }
REPO_ROOT="$(cd "$SCRIPTS_DIR/.." && pwd)"
cd "$REPO_ROOT"

if [ -z "${USBRIDGE_LOGGING_ACTIVE:-}" ]; then
    export USBRIDGE_LOGGING_ACTIVE=1
    LOG_DIR="$REPO_ROOT/logs"
    mkdir -p "$LOG_DIR"
    LOG_FILE="$LOG_DIR/$(basename "$0" .sh).log"
    exec > >(tee -a "$LOG_FILE") 2>&1
    echo "=== $(date '+%Y-%m-%d %H:%M:%S') [$0] ==="
fi

OUTPUT_NAME="USBridgeClient"
DIST_WIN="dist/windows"
DIST_WIN_DLLS="$DIST_WIN/bin"
DIST_WIN_BIN="$DIST_WIN/bin"
APP_EXE_NAME="USBridge_Client.exe"
LAUNCHER_EXE_NAME="USBridge_Client.exe"
BUILD_CACHE_ROOT="$REPO_ROOT/.cache/build/windows-amd64"
MISSING_DLLS_FOUND=0

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

download_file() {
    local url="$1"
    local dest="$2"

    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" -o "$dest"
        return
    fi
    if command -v wget >/dev/null 2>&1; then
        wget -q -O "$dest" "$url"
        return
    fi
    if command -v powershell >/dev/null 2>&1; then
        powershell -NoProfile -NonInteractive -Command \
            "[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; Invoke-WebRequest -Uri '$url' -OutFile '$dest'"
        return
    fi

    echo -e "${RED}❌ Downloader not found for $url${NC}"
    echo "   Need one of: curl, wget or powershell."
    exit 1
}

extract_zip() {
    local archive="$1"
    local dest="$2"

    mkdir -p "$dest"
    if command -v unzip >/dev/null 2>&1; then
        unzip -oq "$archive" -d "$dest"
        return
    fi
    if command -v bsdtar >/dev/null 2>&1; then
        bsdtar -xf "$archive" -C "$dest"
        return
    fi
    if command -v tar >/dev/null 2>&1; then
        tar -xf "$archive" -C "$dest"
        return
    fi
    if command -v powershell >/dev/null 2>&1; then
        powershell -NoProfile -NonInteractive -Command "Expand-Archive -Path '$archive' -DestinationPath '$dest' -Force"
        return
    fi

    echo -e "${RED}❌ Archiver not found for $archive${NC}"
    echo "   Need one of: unzip, bsdtar, tar or powershell."
    exit 1
}

extract_msi() {
    local msi_path="$1"
    local dest="$2"

    mkdir -p "$dest"
    if command -v msiexec.exe >/dev/null 2>&1 && command -v powershell >/dev/null 2>&1; then
        local msi_win dest_win exit_code
        msi_win="$(cygpath -w "$msi_path" 2>/dev/null || echo "$msi_path")"
        dest_win="$(cygpath -w "$dest" 2>/dev/null || echo "$dest")"
        # Administrative install (/a) unpacks the MSI payload without installing it.
        # Routed through PowerShell Start-Process (quiet, hidden window) since invoking
        # msiexec.exe directly from bash mangles the "TARGETDIR=..." argument quoting
        # and pops up the usage dialog instead of running silently.
        powershell -NoProfile -NonInteractive -Command \
            "\$p = Start-Process msiexec.exe -ArgumentList '/a \"$msi_win\" /qn TARGETDIR=\"$dest_win\"' -Wait -PassThru -WindowStyle Hidden; exit \$p.ExitCode"
        exit_code=$?
        [ "$exit_code" = "0" ] && return 0
        return 1
    fi

    echo -e "${RED}❌ msiexec/powershell not found for unpacking $msi_path${NC}"
    return 1
}

hash_file_sha256() {
    local file="$1"

    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$file" | awk '{print $1}'
        return
    fi
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$file" | awk '{print $1}'
        return
    fi
    if command -v powershell >/dev/null 2>&1; then
        powershell -NoProfile -NonInteractive -Command "(Get-FileHash -Algorithm SHA256 -LiteralPath '$file').Hash.ToLowerInvariant()"
        return
    fi

    echo "No SHA-256 tool available for $file" >&2
    return 1
}

build_cache_fingerprint() {
    local path=""
    local file=""
    local hash=""

    printf "BUILD_VARIANT=%s\n" "$BUILD_VARIANT"
    printf "BUILD_LDFLAGS=%s\n" "$BUILD_LDFLAGS"
    printf "GOOS=%s\n" "$GOOS"
    printf "GOARCH=%s\n" "$GOARCH"
    printf "CGO_ENABLED=%s\n" "${CGO_ENABLED:-}"
    printf "PKG_CONFIG=%s\n" "${PKG_CONFIG:-}"
    printf "DEBUG_CONSOLE=%s\n" "${DEBUG_CONSOLE:-0}"

    for path in "$@"; do
        if [ -d "$path" ]; then
            while IFS= read -r file; do
                [ -f "$file" ] || continue
                hash="$(hash_file_sha256 "$file")" || return 1
                printf "%s %s\n" "${file#$REPO_ROOT/}" "$hash"
            done < <(find "$path" -type f | LC_ALL=C sort)
        elif [ -f "$path" ]; then
            hash="$(hash_file_sha256 "$path")" || return 1
            printf "%s %s\n" "${path#$REPO_ROOT/}" "$hash"
        fi
    done
}

echo -e "${GREEN}🪟 Building USBridge Client for Windows${NC}"

# 1. Check Go
find_dist_windows_processes() {
    local dist_dir="$1"

    if ! command -v powershell >/dev/null 2>&1; then
        return 0
    fi

    powershell -NoProfile -NonInteractive -Command "
        \$distDir = [System.IO.Path]::GetFullPath('$dist_dir').ToLowerInvariant()
        Get-CimInstance Win32_Process -Filter \"Name='USBridge_Client.exe'\" -ErrorAction SilentlyContinue |
            ForEach-Object {
                \$path = \$_.ExecutablePath
                if (-not \$path) { return }
                try {
                    \$resolved = [System.IO.Path]::GetFullPath(\$path).ToLowerInvariant()
                } catch {
                    \$resolved = \$path.ToLowerInvariant()
                }
                if (\$resolved.StartsWith(\$distDir)) {
                    '{0}|{1}' -f \$_.ProcessId, \$path
                }
            }
    "
}

if ! command -v go &> /dev/null; then
    echo -e "${RED}❌ Go not found! Install: https://golang.org/dl/${NC}"
    exit 1
fi
echo -e "${GREEN}✓${NC} Go: $(go version)"

# 2. Check mingw-w64
echo -e "\n${YELLOW}🛠️ Check mingw-w64...${NC}"
if ! command -v x86_64-w64-mingw32-gcc &> /dev/null; then
    echo -e "${RED}❌ mingw-w64 not found${NC}"
    echo "   For Windows cross-compilation on Linux you need a compiler:"
    echo "   sudo apt-get install -y mingw-w64"
    exit 1
else
    echo -e "${GREEN}✓${NC} mingw-w64 found"
    export CGO_ENABLED=1
    # Use native gcc for host tools, MinGW only for Windows target.
    export CC="${CC:-gcc}"
    export CXX="${CXX:-g++}"
    export CC_FOR_BUILD="${CC_FOR_BUILD:-gcc}"
    export CXX_FOR_BUILD="${CXX_FOR_BUILD:-g++}"
    export CC_FOR_TARGET=x86_64-w64-mingw32-gcc
    export CXX_FOR_TARGET=x86_64-w64-mingw32-g++
    export CC_FOR_windows_amd64=x86_64-w64-mingw32-gcc
    export CXX_FOR_windows_amd64=x86_64-w64-mingw32-g++
fi

# 2.5. pkg-config (target: Windows)
echo -e "\n${YELLOW}🧩 Check pkg-config (Windows target)...${NC}"
PKG_CONFIG_BIN=""
if command -v x86_64-w64-mingw32-pkg-config &>/dev/null; then
    PKG_CONFIG_BIN="x86_64-w64-mingw32-pkg-config"
elif command -v pkg-config &>/dev/null; then
    PKG_CONFIG_BIN="pkg-config"
fi

if [ -z "$PKG_CONFIG_BIN" ]; then
    echo -e "${RED}❌ pkg-config not found${NC}"
    echo "   Install: sudo apt-get install -y pkg-config"
    exit 1
fi

# Configure pkg-config paths from FFMPEG_ROOT.
PKG_CONFIG_EXTRA_DIRS=()

if [ -n "${FFMPEG_ROOT:-}" ] && [ -d "$FFMPEG_ROOT" ]; then
    if [ -d "$FFMPEG_ROOT/lib/pkgconfig" ]; then
        PKG_CONFIG_EXTRA_DIRS+=("$FFMPEG_ROOT/lib/pkgconfig")
        echo -e "${GREEN}✓${NC} FFMPEG_ROOT: $FFMPEG_ROOT"
    else
        echo -e "${YELLOW}⚠${NC} FFMPEG_ROOT is set, but no $FFMPEG_ROOT/lib/pkgconfig"
    fi
fi

if [ "${#PKG_CONFIG_EXTRA_DIRS[@]}" -gt 0 ]; then
    IFS=':' eval 'EXTRA_PC="${PKG_CONFIG_EXTRA_DIRS[*]}"'
    export PKG_CONFIG_LIBDIR="$EXTRA_PC"
    export PKG_CONFIG_PATH="$EXTRA_PC:${PKG_CONFIG_PATH:-}"
fi

if [ "$PKG_CONFIG_BIN" = "pkg-config" ] && [ -z "${PKG_CONFIG_LIBDIR:-}" ]; then
    echo -e "${RED}❌ For Windows cross-compilation you need PKG_CONFIG_LIBDIR or FFMPEG_ROOT${NC}"
    exit 1
fi

export PKG_CONFIG="$PKG_CONFIG_BIN"
echo -e "${GREEN}✓${NC} PKG_CONFIG: $PKG_CONFIG"

# Moonlight requires FFmpeg (libavcodec/libavutil/libswscale) + opus + openssl.
# GStreamer is not used on Windows at all — the QR scanner uses Media
# Foundation directly (mfcamera_impl_windows.c).
HAS_FFMPEG=0

if "$PKG_CONFIG" --exists libavcodec libavutil libswscale 2>/dev/null; then
    HAS_FFMPEG=1
    echo -e "${GREEN}✓${NC} FFmpeg libs found (Moonlight HW decode enabled)"
else
    echo -e "${YELLOW}⚠${NC} FFmpeg libs not found via pkg-config — Moonlight will use software decode fallback"
    echo "   Set FFMPEG_ROOT to enable HW decode: export FFMPEG_ROOT=/path/to/ffmpeg-mingw"
    echo "   Moonlight will fall back to software decode using bundled opus/openssl only"
fi

# 3. Check fyne
echo -e "\n${YELLOW}📦 Check fyne...${NC}"
FYNE_BIN=""
# In MSYS2 (UCRT64/MinGW64) go env GOPATH returns a Windows-style path.
# Normalise it to a POSIX path so bash [ -x ] checks work correctly.
_raw_gopath="$(go env GOPATH)"
if command -v cygpath >/dev/null 2>&1; then
    GOPATH_BIN="$(cygpath -u "$_raw_gopath")/bin"
else
    # Fallback: convert C:\foo\bar → /c/foo/bar manually
    GOPATH_BIN="$(printf '%s' "$_raw_gopath" | sed -e 's|\\|/|g' -e 's|^\([A-Za-z]\):|/\L\1|')/bin"
fi
for name in fyne fyne.exe; do
    if command -v "$name" &> /dev/null; then
        FYNE_BIN="$name"
        break
    fi
    if [ -x "$GOPATH_BIN/$name" ]; then
        FYNE_BIN="$GOPATH_BIN/$name"
        break
    fi
done
if [ -z "$FYNE_BIN" ]; then
    echo -e "${YELLOW}⚠${NC} fyne not found, installing..."
    go install fyne.io/tools/cmd/fyne@latest
    for name in fyne.exe fyne; do
        if [ -x "$GOPATH_BIN/$name" ]; then
            FYNE_BIN="$GOPATH_BIN/$name"
            break
        fi
    done
    [ -z "$FYNE_BIN" ] && FYNE_BIN="$GOPATH_BIN/fyne"
fi
echo -e "${GREEN}✓${NC} fyne: $FYNE_BIN"

ICON_PATH="$REPO_ROOT/Icon.png"
if [ ! -f "$ICON_PATH" ]; then
    echo -e "${RED}❌ Icon not found: $ICON_PATH${NC}"
    exit 1
fi
echo -e "${GREEN}✓${NC} Icon: $ICON_PATH"

echo -e "\n${YELLOW}🔨 Compilation...${NC}"
cd "$REPO_ROOT/cmd"
export GOOS=windows
export GOARCH=amd64
export GOMAXPROCS=12
export GOCACHE="${GOCACHE:-$REPO_ROOT/.cache/go-build/windows-amd64}"
export GOMODCACHE="${GOMODCACHE:-$REPO_ROOT/.cache/go-mod}"
mkdir -p "$GOCACHE" "$GOMODCACHE"
export GOFLAGS="${GOFLAGS:-} -buildvcs=false"
# Keep cmd/VERSION (go:embed'd by cmd/main.go as a fallback for build tools
# without -ldflags passthrough) in sync with the repo-root VERSION file.
cp "$REPO_ROOT/VERSION" "$REPO_ROOT/cmd/VERSION" 2>/dev/null || true
VERSION=$(cat "$REPO_ROOT/VERSION" 2>/dev/null || echo "1.0.0")
BUILD_LDFLAGS="-H=windowsgui -X main.version=$VERSION -extldflags '-static-libgcc -static-libstdc++ -Wl,-Bstatic -lwinpthread -Wl,-Bdynamic'"
BUILD_VARIANT="release"
if [ "${DEBUG_CONSOLE:-0}" = "1" ]; then
    BUILD_LDFLAGS="-H=console -X main.version=$VERSION"
    BUILD_VARIANT="console"
    echo -e "${YELLOW}⚠${NC} DEBUG_CONSOLE=1: building console version"
fi
BUILD_CACHE_DIR="$BUILD_CACHE_ROOT/$BUILD_VARIANT"
BUILD_CACHE_APP_EXE="$BUILD_CACHE_DIR/$APP_EXE_NAME"
BUILD_CACHE_FINGERPRINT="$BUILD_CACHE_DIR/.build-inputs.sha256"
BUILD_CACHE_FINGERPRINT_TMP="$BUILD_CACHE_DIR/.build-inputs.current"
mkdir -p "$BUILD_CACHE_DIR"
export PATH="$GOPATH_BIN:$PATH"

REBUILD_WINDOWS_EXE=0
REBUILD_WINDOWS_REASON=""
if ! build_cache_fingerprint \
    "$REPO_ROOT/cmd" \
    "$REPO_ROOT/internal" \
    "$REPO_ROOT/go.mod" \
    "$REPO_ROOT/go.sum" \
    "$REPO_ROOT/FyneApp.toml" \
    "$SCRIPTS_DIR/build_windows.sh" \
    "$ICON_PATH" > "$BUILD_CACHE_FINGERPRINT_TMP"; then
    echo -e "${RED}Fingerprint generation for build cache failed${NC}"
    exit 1
fi

if [ "${FORCE_REBUILD:-0}" = "1" ]; then
    REBUILD_WINDOWS_EXE=1
    REBUILD_WINDOWS_REASON="FORCE_REBUILD=1"
elif [ ! -f "$BUILD_CACHE_APP_EXE" ]; then
    REBUILD_WINDOWS_EXE=1
    REBUILD_WINDOWS_REASON="cached exe is missing"
elif [ ! -f "$BUILD_CACHE_FINGERPRINT" ]; then
    REBUILD_WINDOWS_EXE=1
    REBUILD_WINDOWS_REASON="build fingerprint is missing"
elif command -v cmp >/dev/null 2>&1; then
    if ! cmp -s "$BUILD_CACHE_FINGERPRINT_TMP" "$BUILD_CACHE_FINGERPRINT"; then
        REBUILD_WINDOWS_EXE=1
        REBUILD_WINDOWS_REASON="build inputs changed"
    fi
elif ! diff -q "$BUILD_CACHE_FINGERPRINT_TMP" "$BUILD_CACHE_FINGERPRINT" >/dev/null 2>&1; then
    REBUILD_WINDOWS_EXE=1
    REBUILD_WINDOWS_REASON="build inputs changed"
fi

# Find or install go-winres
GOWINRES_BIN=""
for _n in go-winres go-winres.exe; do
    _full="$(command -v "$_n" 2>/dev/null || true)"
    if [ -n "$_full" ]; then GOWINRES_BIN="$_full"; break; fi
    if [ -x "$GOPATH_BIN/$_n" ]; then GOWINRES_BIN="$GOPATH_BIN/$_n"; break; fi
done
if [ -z "$GOWINRES_BIN" ]; then
    echo -e "${YELLOW}⚠${NC} go-winres not found, installing..."
    GOOS="" GOARCH="" go install github.com/tc-hib/go-winres@latest
    for _n in go-winres.exe go-winres; do
        if [ -x "$GOPATH_BIN/$_n" ]; then GOWINRES_BIN="$GOPATH_BIN/$_n"; break; fi
        _full="$(command -v "$_n" 2>/dev/null || true)"
        if [ -n "$_full" ]; then GOWINRES_BIN="$_full"; break; fi
    done
fi

# 5a. Main app
APP_SYSO="$REPO_ROOT/cmd/rsrc_windows_amd64.syso"
if [ "$REBUILD_WINDOWS_EXE" = "1" ]; then
    echo -e "${YELLOW}🧱 Building main app (Go cache: $GOCACHE)...${NC}"
    echo "   Reason: $REBUILD_WINDOWS_REASON"
    APP_SYSO="$REPO_ROOT/cmd/rsrc_windows_amd64.syso"
    if [ -n "$GOWINRES_BIN" ] && [ -x "$GOWINRES_BIN" ]; then
        rm -f "$APP_SYSO"
        if (cd "$REPO_ROOT/cmd" && \
            GOOS=windows GOARCH=amd64 \
            "$GOWINRES_BIN" simply --icon "$ICON_PATH" 2>&1); then
            [ -f "$APP_SYSO" ] && echo -e "${GREEN}✓${NC} Icon embedded into main app: $APP_SYSO"
        fi
    else
        echo -e "${YELLOW}⚠${NC} go-winres unavailable - main app will be without icon"
    fi
    go build -trimpath -ldflags="$BUILD_LDFLAGS" -o "$BUILD_CACHE_APP_EXE" "$REPO_ROOT/cmd"
    rm -f "$APP_SYSO"
    mv "$BUILD_CACHE_FINGERPRINT_TMP" "$BUILD_CACHE_FINGERPRINT"
else
    rm -f "$BUILD_CACHE_FINGERPRINT_TMP"
    echo -e "${GREEN}✓${NC} Using ready app exe from cache: $BUILD_CACHE_APP_EXE"
fi


# 6. Create dist folder
echo -e "\n${YELLOW}📁 Creating dist folder...${NC}"
cd "$REPO_ROOT"
mkdir -p "$DIST_WIN"

running_dist_processes=()
while IFS= read -r proc; do
    [ -n "$proc" ] && running_dist_processes+=("$proc")
done < <(find_dist_windows_processes "$DIST_WIN")

if [ "${#running_dist_processes[@]}" -gt 0 ]; then
    echo -e "${RED}❌ Cannot clean $DIST_WIN while USBridge is running from that folder${NC}"
    exit 1
fi

cleanup_err="${TMPDIR:-/tmp}/usbridge_dist_cleanup.err"
cleanup_failed=0
while IFS= read -r existing; do
    if ! rm -rf -- "$existing" 2>>"$cleanup_err"; then
        cleanup_failed=1
    fi
done < <(find "$DIST_WIN" -mindepth 1 -maxdepth 1 -print 2>>"$cleanup_err")

if [ "$cleanup_failed" != "0" ]; then
    echo -e "${RED}❌ Failed to clean $DIST_WIN${NC}"
    rm -f "$cleanup_err"
    exit 1
fi
rm -f "$cleanup_err"
mkdir -p "$DIST_WIN_DLLS" "$DIST_WIN_BIN"


cp "$BUILD_CACHE_APP_EXE" "$DIST_WIN_BIN/$APP_EXE_NAME"

# local ui.parse ONNX offload (internal/localui, AI Vision's detector): the
# runtime lib is dlopen'd at runtime (via onnxruntime_go), not link-time
# linked, so the DLL dependency walk below (which only follows what the
# .exe actually imports) can never see or bundle it -- it needs its own
# explicit step, same as build_macos.sh's equivalent (see
# fetch_onnxruntime.sh's doc comment for why a PyPI wheel and not a manual
# download). Placed flat next to the .exe in $DIST_WIN_BIN, matching
# local_ui_init.go's resolveLocalUIPath flat-layout candidate. Both
# failures are non-fatal (warn and continue): local ui.parse/AI Vision is
# an optional accelerator, never a hard dependency of the build.
echo -e "${YELLOW}Bundling local ui.parse (ONNX Runtime + models) for AI Vision...${NC}"
ORT_CACHE_DIR="$REPO_ROOT/.build-cache/onnxruntime-windows"
# Also re-fetch if DirectML.dll is missing -- guards a stale local cache dir
# from before fetch_onnxruntime.sh started bundling it (CPU-only
# onnxruntime.dll present, DirectML.dll never fetched, so the plain
# onnxruntime.dll-only check below would otherwise skip re-fetching forever
# on a dev machine that built this before that change).
if [ ! -f "$ORT_CACHE_DIR/onnxruntime.dll" ] || [ ! -f "$ORT_CACHE_DIR/DirectML.dll" ]; then
    "$SCRIPTS_DIR/fetch_onnxruntime.sh" "$ORT_CACHE_DIR" windows || true
fi
if [ -f "$ORT_CACHE_DIR/onnxruntime.dll" ]; then
    cp -L "$ORT_CACHE_DIR/onnxruntime.dll" "$DIST_WIN_BIN/onnxruntime.dll"
    echo -e "${GREEN}✓${NC} bin/onnxruntime.dll"
else
    echo -e "${YELLOW}⚠${NC} Could not fetch onnxruntime.dll -- local ui.parse/AI Vision will stay unavailable in this build"
fi
# DirectML.dll (GPU execution provider, NVIDIA/AMD/Intel alike -- see
# internal/localui/onnx.go's acceleratorEP): fetch_onnxruntime.sh drops this
# next to onnxruntime.dll on windows; without it, onnx.go's
# AppendExecutionProviderDirectML call fails at runtime and everything
# silently falls back to CPU, same "degrade, don't hard-fail" pattern as a
# missing onnxruntime.dll above -- so this is non-fatal too, just a weaker
# build.
if [ -f "$ORT_CACHE_DIR/DirectML.dll" ]; then
    cp -L "$ORT_CACHE_DIR/DirectML.dll" "$DIST_WIN_BIN/DirectML.dll"
    echo -e "${GREEN}✓${NC} bin/DirectML.dll"
else
    echo -e "${YELLOW}⚠${NC} Could not fetch DirectML.dll -- local ui.parse/AI Vision will run CPU-only (no GPU accel) in this build"
fi
LOCALUI_MODELS_SRC="$REPO_ROOT/internal/localui/models"
if [ -f "$LOCALUI_MODELS_SRC/icon_detect.onnx" ]; then
    mkdir -p "$DIST_WIN_BIN/localui/models"
    cp "$LOCALUI_MODELS_SRC"/*.onnx "$DIST_WIN_BIN/localui/models/"
    echo -e "${GREEN}✓${NC} bin/localui/models/ ($(du -sh "$DIST_WIN_BIN/localui/models" | cut -f1))"
else
    echo -e "${YELLOW}⚠${NC} $LOCALUI_MODELS_SRC has no .onnx files -- local ui.parse/AI Vision will stay unavailable in this build"
fi

# Create a relative shortcut using explorer.exe
echo "Creating shortcut..."
cat << 'VBS_EOF' > "$DIST_WIN/make_shortcut.vbs"
Set oWS = WScript.CreateObject("WScript.Shell")
sLinkFile = WScript.Arguments(0)
Set oLink = oWS.CreateShortcut(sLinkFile)
oLink.TargetPath = "explorer.exe"
oLink.Arguments = "bin\" & WScript.Arguments(1)
oLink.IconLocation = oWS.CurrentDirectory & "\bin\" & WScript.Arguments(1) & ", 0"
oLink.WindowStyle = 1
oLink.Save
VBS_EOF
cscript //nologo "$DIST_WIN/make_shortcut.vbs" "$(cygpath -w "$DIST_WIN/USBridge_Client.lnk")" "$APP_EXE_NAME"
rm -f "$DIST_WIN/make_shortcut.vbs"

[ -f config.yaml ] && cp config.yaml "$DIST_WIN/" && echo -e "${GREEN}✓${NC} config.yaml"

# 7a. Copying FFmpeg DLLs (for Moonlight HW decode)
echo -e "\n${YELLOW}📚 Copying FFmpeg DLLs (Moonlight HW decode)...${NC}"

# ── DLL utility functions (used by 7a, 7b, 7c) ────────────────────────────────
OBJDUMP_BIN="${OBJDUMP_BIN:-x86_64-w64-mingw32-objdump}"
if ! command -v "$OBJDUMP_BIN" >/dev/null 2>&1; then
    if command -v objdump >/dev/null 2>&1; then
        OBJDUMP_BIN="objdump"
    elif [ -n "$FFMPEG_BIN_DIR" ] && [ -f "$FFMPEG_BIN_DIR/objdump.exe" ]; then
        OBJDUMP_BIN="$FFMPEG_BIN_DIR/objdump.exe"
    elif [ -f "/ucrt64/bin/objdump.exe" ]; then
        OBJDUMP_BIN="/ucrt64/bin/objdump.exe"
    elif [ -f "/mingw64/bin/objdump.exe" ]; then
        OBJDUMP_BIN="/mingw64/bin/objdump.exe"
    fi
fi
echo "=> Using objdump: $OBJDUMP_BIN"

# MSYS2 prefix dirs
DLL_EXTRA_DIRS=()
for _pfx in "/ucrt64" "/mingw64" "/clang64" "/c/msys64/ucrt64" "/c/msys64/mingw64" "C:/msys64/ucrt64" "C:/msys64/mingw64"; do
    if [ -d "$_pfx/bin" ]; then
        DLL_EXTRA_DIRS+=("$_pfx/bin")
    elif [ -d "$_pfx" ] && ls "$_pfx"/*.dll &>/dev/null 2>&1; then
        DLL_EXTRA_DIRS+=("$_pfx")
    fi
done

is_system_dll() {
    local name="${1,,}"
    case "$name" in
        api-ms-win-*.dll|ext-ms-win-*.dll|\
        kernel32.dll|user32.dll|gdi32.dll|advapi32.dll|shell32.dll|\
        ole32.dll|oleaut32.dll|comdlg32.dll|comctl32.dll|imm32.dll|\
        setupapi.dll|version.dll|winmm.dll|ws2_32.dll|secur32.dll|\
        rpcrt4.dll|crypt32.dll|bcrypt.dll|ntdll.dll|shlwapi.dll|\
        msvcrt.dll|ucrtbase.dll|dwmapi.dll|dxgi.dll|d3d11.dll|\
        d3dcompiler_47.dll|opengl32.dll|mf.dll|mfplat.dll|mfuuid.dll|\
        uuid.dll|wininet.dll|netapi32.dll|iphlpapi.dll|\
        msimg32.dll|userenv.dll|bcryptprimitives.dll|ncrypt.dll|\
        wsock32.dll|wldap32.dll|gdiplus.dll|dnsapi.dll|\
        dwrite.dll|usp10.dll|cfgmgr32.dll|\
        d3d12.dll|d2d1.dll|ksuser.dll|mfreadwrite.dll|\
        mfmediaengine.dll|mfcore.dll|mfsensorgroup.dll|\
        d3d9.dll|d3d10.dll|dxcore.dll|dcomp.dll|\
        dbghelp.dll|psapi.dll|pdh.dll|wtsapi32.dll|\
        authz.dll|wintrust.dll|aclui.dll|cabinet.dll|\
        ndfapi.dll|devobj.dll|hid.dll|hidparse.dll|\
        ksproxy.ax|avrt.dll|wmcodecdspuuid.dll|\
        vulkan-1.dll|wintun.dll|tap-windows6.dll)
            return 0 ;;
    esac
    return 1
}

_collect_dll_deps() {
    local file="$1"
    [ -f "$file" ] || return
    "$OBJDUMP_BIN" -p "$file" \
        | grep -i 'DLL Name:' \
        | awk '{print $NF}' \
        | tr -d '\r'
}

_resolve_dll() {
    local name="$1"
    # Search: FFMPEG_BIN_DIR → FFMPEG_ROOT → DLL_EXTRA_DIRS
    local _d _found

    # Pre-process search paths to ensure they are MSYS2-friendly if possible
    local _search_paths=(
        "${FFMPEG_BIN_DIR:-}"
        "${FFMPEG_ROOT:-}/bin"
        "${DLL_EXTRA_DIRS[@]}"
    )

    for _d in "${_search_paths[@]}"; do
        [ -z "$_d" ] || [ ! -d "$_d" ] && continue
        
        # Try direct match
        if [ -f "$_d/$name" ]; then 
            printf "%s\n" "$_d/$name"
            return
        fi
        
        # Try bash wildcard expansion
        for _found in "$_d"/$name; do
            if [ -f "$_found" ]; then
                printf "%s\n" "$_found"
                return
            fi
        done

        # Try case-insensitive find
        if command -v find >/dev/null 2>&1; then
            _found="$(find "$_d" -maxdepth 1 -iname "$name" 2>/dev/null | head -1)"
            if [ -n "$_found" ] && [ -f "$_found" ]; then
                printf "%s\n" "$_found"
                return
            fi
        fi
    done
}

_copy_dll() {
    local name="$1" target_dir="$2"
    local resolved
    resolved="$(_resolve_dll "$name")"
    [ -n "$resolved" ] && [ -f "$resolved" ] || return
    cp -L "$resolved" "$target_dir/" || true
    printf "%s\n" "$(basename "$resolved")"
}

_walk_deps() {
    # collect_recursive_deps_into <target_dir> <file>...
    local target_dir="$1"; shift
    local queue=("$@") 
    local idx=0
    local visited_deps=()
    
    # Pre-populate visited with what's already in queue (basenames)
    for f in "${queue[@]}"; do
        visited_deps+=("$(basename "$f" | tr '[:upper:]' '[:lower:]')")
    done

    echo "=> Starting recursive dependency walk in $target_dir"
    while [ $idx -lt ${#queue[@]} ]; do
        local file="${queue[$idx]}"
        idx=$((idx+1))
        
        [ -f "$file" ] || continue
        echo "   ... walking deps of $(basename "$file")"
        
        local deps_tmp
        deps_tmp="$(mktemp)"
        _collect_dll_deps "$file" > "$deps_tmp" 2>/dev/null || { rm -f "$deps_tmp"; continue; }
        
        while IFS= read -r dep; do
            [ -z "$dep" ] && continue
            is_system_dll "$dep" && continue
            
            local dep_lower="${dep,,}"
            local already_visited=0
            for v in "${visited_deps[@]}"; do
                if [ "$v" = "$dep_lower" ]; then already_visited=1; break; fi
            done
            [ "$already_visited" = "1" ] && continue
            
            # Even if it exists, we add it to visited and queue to walk its own deps
            # but we only copy if it doesn't exist
            local _existing
            _existing="$(find "$target_dir" -maxdepth 1 -iname "$dep" 2>/dev/null | head -1)"
            
            if [ -z "$_existing" ]; then
                local copied
                copied="$(_copy_dll "$dep" "$target_dir")" || true
                if [ -n "$copied" ]; then
                    echo -e "   ${GREEN}✓${NC} $copied (dep of $(basename "$file"))" >&2
                    visited_deps+=("${copied,,}")
                    queue+=("$target_dir/$copied")
                else
                    echo -e "   ${RED}❌ ERROR: Dependency not found $dep (for $(basename "$file"))${NC}" >&2
                    MISSING_DLLS_FOUND=1
                    # Add to visited anyway to avoid spamming the same error
                    visited_deps+=("$dep_lower")
                fi
            else
                # Already exists, but we haven't "walked" it yet in this loop 
                # (otherwise it would be in visited_deps already)
                visited_deps+=("$dep_lower")
                queue+=("$_existing")
            fi
        done < "$deps_tmp"
        rm -f "$deps_tmp"
    done
}
# ── end DLL utilities ──────────────────────────────────────────────────────────

# Auto-detect FFMPEG_ROOT from pkg-config when not explicitly set
if [ -z "${FFMPEG_ROOT:-}" ] && [ "$HAS_FFMPEG" = "1" ]; then
    _pc_prefix="$("$PKG_CONFIG" --variable=prefix libavcodec 2>/dev/null || true)"
    _pc_libdir="$("$PKG_CONFIG" --variable=libdir  libavcodec 2>/dev/null || true)"
    for _d in "$_pc_prefix/bin" "$_pc_libdir/../bin" "$_pc_libdir" "$_pc_prefix"; do
        [ -z "$_d" ] && continue
        if ls "$_d"/avutil-*.dll &>/dev/null 2>&1 || ls "$_d"/avcodec-*.dll &>/dev/null 2>&1; then
            FFMPEG_ROOT="$(realpath "$(dirname "$_d")" 2>/dev/null || echo "$(dirname "$_d")")"
            echo -e "${GREEN}✓${NC} FFMPEG_ROOT auto-detected from pkg-config: $FFMPEG_ROOT"
            break
        fi
    done
fi

FFMPEG_DLLS=(
    "avcodec-*.dll"    # H.264 decoder
    "avutil-*.dll"     # utilities
    "swscale-*.dll"    # pixel format conversion
    "swresample-*.dll" # audio resampling (avcodec dep)
    "avformat-*.dll"   # format/container (avcodec dep on some builds)
    "postproc-*.dll"   # postprocessing (dep on some GPL builds)
    "libjxl*.dll"      # JPEG XL (needed for some FFmpeg builds)
    "libhwy*.dll"      # Highway (jxl dep)
    "libbrotli*.dll"   # Brotli (jxl dep)
    "liblcms2-*.dll"   # Little CMS (jxl dep)
    "libogg-*.dll"     # Ogg (vorbis dep)
    "libvorbis*.dll"   # Vorbis
    "libopus-*.dll"    # Opus
    "libsoxr*.dll"     # SoX resampler (swresample dep)
    "libsharpyuv-*.dll" # Sharp YUV (webp dep)
    "libshaderc*.dll"  # Shaderc (swscale/vulkan dep)
    "libgomp-*.dll"    # OpenMP runtime (aom/dav1d dep)
)
# Moonlight runtime DLLs: openssl + MinGW C runtime + additional deps
MOONLIGHT_RUNTIME_DLLS=(
    "libcrypto-*.dll"
    "libssl-*.dll"
    "libgcc_s_*.dll"
    "libwinpthread-*.dll"
    "libstdc++-*.dll"
    "libzstd.dll"
    "libdeflate.dll"
)
FFMPEG_COPIED=0
FFMPEG_BIN_DIR=""

if [ -n "${FFMPEG_ROOT:-}" ] && [ -d "$FFMPEG_ROOT" ]; then
    for d in "$FFMPEG_ROOT/bin" "$FFMPEG_ROOT"; do
        if ls "$d"/*.dll &>/dev/null 2>&1; then FFMPEG_BIN_DIR="$d"; break; fi
    done
    # Add to DLL_EXTRA_DIRS so the final dependency walk can find FFmpeg deps
    [ -n "$FFMPEG_BIN_DIR" ] && DLL_EXTRA_DIRS=("$FFMPEG_BIN_DIR" "${DLL_EXTRA_DIRS[@]}")

    if [ -n "$FFMPEG_BIN_DIR" ]; then
        echo "=> Copying libraries from $FFMPEG_BIN_DIR..."
        for pattern in "${FFMPEG_DLLS[@]}"; do
            while IFS= read -r dll; do
                [ -f "$dll" ] || continue
                base_dll="$(basename "$dll")"
                if [ ! -f "$DIST_WIN_DLLS/$base_dll" ]; then
                    cp -L "$dll" "$DIST_WIN_DLLS/"
                    echo -e "   ${GREEN}✓${NC} $base_dll"
                    FFMPEG_COPIED=$((FFMPEG_COPIED + 1))
                fi
            done < <(find "$FFMPEG_BIN_DIR" -maxdepth 1 -iname "$pattern" 2>/dev/null)
        done
        echo -e "${GREEN}✓${NC} FFmpeg/Dependencies: $FFMPEG_COPIED base DLLs copied"

        # Walk recursive deps of all copied DLLs in lib/
        mapfile -t _all_copied < <(find "$DIST_WIN_DLLS" -maxdepth 1 -name "*.dll" 2>/dev/null)
        [ "${#_all_copied[@]}" -gt 0 ] && _walk_deps "$DIST_WIN_DLLS" "${_all_copied[@]}"
    else
        :
    fi
else
    :
fi

# Copy Moonlight-specific runtime DLLs (opus, openssl, MinGW runtime)
echo -e "\n${YELLOW}📚 Copying Moonlight runtime DLLs (opus, openssl, MinGW)...${NC}"
MOONLIGHT_COPIED=0
for _dll_name in "${MOONLIGHT_RUNTIME_DLLS[@]}"; do
    _copied="$(_copy_dll "$_dll_name" "$DIST_WIN_DLLS")"
    if [ -n "$_copied" ]; then
        echo -e "   ${GREEN}✓${NC} $_copied"
        MOONLIGHT_COPIED=$((MOONLIGHT_COPIED + 1))
    else
        # We don't fail for wildcard patterns that might legitimately match 0 files,
        # but for explicit exact DLL names, we print an error.
        if [[ "$_dll_name" != *"*"* ]]; then
            echo -e "   ${RED}❌ ERROR: Base library not found $_dll_name${NC}" >&2
            MISSING_DLLS_FOUND=1
        fi
    fi
done
if [ "$MOONLIGHT_COPIED" -gt 0 ]; then
    echo -e "${GREEN}✓${NC} Moonlight runtime: $MOONLIGHT_COPIED DLLs copied"
else
    echo -e "${YELLOW}⚠${NC} Moonlight runtime DLLs not found (opus/openssl may be statically linked or missing)"
fi

echo -e "\n${YELLOW}📀 Copying QEMU (qemu-nbd, qemu-img)...${NC}"

QEMU_BIN_DIR=""
for _qd in \
    "/ucrt64/bin" \
    "/mingw64/bin" \
    "/c/msys64/ucrt64/bin" \
    "/c/msys64/mingw64/bin" \
    "C:/msys64/ucrt64/bin" \
    "C:/msys64/mingw64/bin" \
    "${QEMU_ROOT:-}/bin" \
    "${QEMU_ROOT:-}"
do
    [ -z "$_qd" ] && continue
    if [ -f "$_qd/qemu-nbd.exe" ]; then
        QEMU_BIN_DIR="$_qd"
        break
    fi
done

if [ -n "$QEMU_BIN_DIR" ]; then
    echo "=> QEMU found: $QEMU_BIN_DIR"
    QEMU_COPIED=0
    for _qtool in qemu-nbd.exe qemu-img.exe; do
        if [ -f "$QEMU_BIN_DIR/$_qtool" ]; then
            cp -L "$QEMU_BIN_DIR/$_qtool" "$DIST_WIN_BIN/"
            echo -e "   ${GREEN}✓${NC} bin/$_qtool"
            QEMU_COPIED=$((QEMU_COPIED + 1))
        else
            echo -e "   ${YELLOW}⚠${NC} $_qtool not found in $QEMU_BIN_DIR"
        fi
    done
    # Add QEMU_BIN_DIR to DLL search pool for _walk_deps
    DLL_EXTRA_DIRS=("$QEMU_BIN_DIR" "${DLL_EXTRA_DIRS[@]}")
    mapfile -t _qemu_bins < <(find "$DIST_WIN_BIN" -maxdepth 1 \( -name "qemu-nbd.exe" -o -name "qemu-img.exe" \) 2>/dev/null)
    [ "${#_qemu_bins[@]}" -gt 0 ] && _walk_deps "$DIST_WIN_DLLS" "${_qemu_bins[@]}"
else
    echo "   Install: pacman -S mingw-w64-ucrt-x86_64-qemu"
fi

if command -v "$OBJDUMP_BIN" >/dev/null 2>&1; then
    mapfile -t _initial_queue < <(
        find "$DIST_WIN" -maxdepth 1 -name "*.exe" 2>/dev/null
        find "$DIST_WIN_BIN" -maxdepth 1 -name "*.exe" 2>/dev/null
        find "$DIST_WIN_DLLS" -maxdepth 1 -name "*.dll" 2>/dev/null
    )
    if [ "${#_initial_queue[@]}" -gt 0 ]; then
        _walk_deps "$DIST_WIN_DLLS" "${_initial_queue[@]}"
    fi
else
    :
fi

# 7e. Sanity check for known problematic DLLs
FORCE_DLLS=(
    "libhwy.dll" "libogg-0.dll" "libbrotlienc.dll" "libjxl_cms.dll"
    "libsoxr.dll" "libsharpyuv-0.dll" "libshaderc_shared.dll" "liblcms2-2.dll"
    "libgomp-1.dll" "libstdc++-6.dll" "libpng16-16.dll"
)
for _fdll in "${FORCE_DLLS[@]}"; do
    _existing="$(find "$DIST_WIN_DLLS" -maxdepth 1 -iname "$_fdll" 2>/dev/null | head -1)"
    if [ -z "$_existing" ]; then
        _copied="$(_copy_dll "$_fdll" "$DIST_WIN_DLLS")"
        if [ -n "$_copied" ]; then
            _walk_deps "$DIST_WIN_DLLS" "$DIST_WIN_DLLS/$_copied"
        else
            MISSING_DLLS_FOUND=1
        fi
    else
        :
    fi
done

# 7f. Bundle Tailscale CLI (tailscale.exe + tailscaled.exe)
# Enables system-level Tailscale routing so C sockets (Moonlight RTSP/RTP)
# can reach Tailscale 100.x IPs without the Go tsnet proxy overhead.
# The app also works without this via the built-in tsnet proxy, but having
# tailscale.exe / tailscaled.exe in bin/ lets users run a system TS daemon.
echo -e "\n${YELLOW}🔗 Bundling Tailscale CLI (tailscale.exe + tailscaled.exe)...${NC}"

TAILSCALE_CACHE_DIR="$REPO_ROOT/.cache/tailscale-windows"
TAILSCALE_BIN_DIR=""

# 1. Check PATH / TAILSCALE_ROOT / MSYS2 prefixes
for _ts_candidate in \
    "${TAILSCALE_ROOT:-}" \
    "${TAILSCALE_ROOT:-}/bin" \
    "/ucrt64/bin" \
    "/mingw64/bin" \
    "/clang64/bin"
do
    [ -z "$_ts_candidate" ] && continue
    if [ -f "$_ts_candidate/tailscale.exe" ] || [ -f "$_ts_candidate/tailscaled.exe" ]; then
        TAILSCALE_BIN_DIR="$_ts_candidate"
        echo -e "${GREEN}✓${NC} Tailscale found locally: $TAILSCALE_BIN_DIR"
        break
    fi
done

# 2. Check disk cache from a previous download
if [ -z "$TAILSCALE_BIN_DIR" ] && [ -f "$TAILSCALE_CACHE_DIR/tailscale.exe" ]; then
    TAILSCALE_BIN_DIR="$TAILSCALE_CACHE_DIR"
    echo -e "${GREEN}✓${NC} Tailscale found in download cache: $TAILSCALE_CACHE_DIR"
fi

# 3. Download from pkgs.tailscale.com / GitHub releases
if [ -z "$TAILSCALE_BIN_DIR" ] && [ "${SKIP_TAILSCALE_DOWNLOAD:-0}" != "1" ]; then
    echo -e "${YELLOW}⚠${NC} Tailscale not found locally, downloading latest stable..."
    mkdir -p "$TAILSCALE_CACHE_DIR"

    # Resolve latest stable version tag
    TS_VERSION=""
    if command -v curl >/dev/null 2>&1; then
        TS_VERSION=$(curl -fsSL \
            "https://api.github.com/repos/tailscale/tailscale/releases/latest" \
            2>/dev/null \
            | grep '"tag_name"' | head -1 \
            | sed 's/.*"tag_name": *"v\([^"]*\)".*/\1/')
    fi
    [ -z "$TS_VERSION" ] && TS_VERSION="1.98.8"
    echo "   Tailscale version: $TS_VERSION"

    # pkgs.tailscale.com no longer publishes a raw windows_amd64.zip — only the
    # NSIS installer exe and the MSI. The MSI can be unpacked without installing
    # anything via an administrative install (msiexec /a), which yields the plain
    # tailscale.exe / tailscaled.exe / wintun.dll payload.
    TS_MSI="$TAILSCALE_CACHE_DIR/tailscale-setup-${TS_VERSION}-amd64.msi"
    TS_URL="https://pkgs.tailscale.com/stable/tailscale-setup-${TS_VERSION}-amd64.msi"

    echo "   Downloading: $TS_URL"
    if download_file "$TS_URL" "$TS_MSI" 2>/dev/null; then
        TS_EXTRACT_DIR="$TAILSCALE_CACHE_DIR/extracted"
        rm -rf "$TS_EXTRACT_DIR"
        if extract_msi "$TS_MSI" "$TS_EXTRACT_DIR"; then
            _ts_exe=$(find "$TS_EXTRACT_DIR" -name "tailscale.exe" 2>/dev/null | head -1)
            if [ -n "$_ts_exe" ]; then
                _ts_dir="$(dirname "$_ts_exe")"
                # Flatten to cache root
                for _f in tailscale.exe tailscaled.exe wintun.dll; do
                    _fp="$_ts_dir/$_f"
                    [ -f "$_fp" ] && cp -L "$_fp" "$TAILSCALE_CACHE_DIR/"
                done
                TAILSCALE_BIN_DIR="$TAILSCALE_CACHE_DIR"
                echo -e "${GREEN}✓${NC} Tailscale extracted from MSI to $TAILSCALE_CACHE_DIR"
            else
                echo -e "${RED}❌ tailscale.exe not found in extracted MSI${NC}"
            fi
        else
            echo -e "${RED}❌ Failed to extract MSI: $TS_MSI${NC}"
        fi
    else
        echo -e "${RED}❌ Download failed: $TS_URL${NC}"
        echo "   Set TAILSCALE_ROOT=/path/to/dir/with/tailscale.exe to provide it manually"
        echo "   Or: set SKIP_TAILSCALE_DOWNLOAD=1 to skip bundling"
    fi
fi

if [ -n "$TAILSCALE_BIN_DIR" ]; then
    TS_COPIED=0
    for _ts_bin in tailscale.exe tailscaled.exe wintun.dll; do
        _src="$TAILSCALE_BIN_DIR/$_ts_bin"
        if [ -f "$_src" ]; then
            cp -L "$_src" "$DIST_WIN_BIN/"
            echo -e "   ${GREEN}✓${NC} bin/$_ts_bin"
            TS_COPIED=$((TS_COPIED + 1))
        elif [ "$_ts_bin" != "wintun.dll" ]; then
            echo -e "   ${YELLOW}⚠${NC} $_ts_bin not found in $TAILSCALE_BIN_DIR"
        fi
    done
    # Walk DLL deps of tailscale binaries
    mapfile -t _ts_bins < <(
        find "$DIST_WIN_BIN" -maxdepth 1 \( -name "tailscale.exe" -o -name "tailscaled.exe" \) 2>/dev/null
    )
    [ "${#_ts_bins[@]}" -gt 0 ] && _walk_deps "$DIST_WIN_DLLS" "${_ts_bins[@]}"
else
    echo -e "${YELLOW}⚠${NC} Tailscale binaries not bundled"
    echo "   Video still works via built-in tsnet proxy (no system Tailscale needed)"
    echo "   To bundle: export TAILSCALE_ROOT=/path/to/tailscale && rebuild"
fi

# 8. README

cat > "$DIST_WIN/README.txt" << 'README'
USBridge Client for Windows
===========================
Double-click USBridge_Client.exe to launch.

Folder structure:
  USBridge_Client.exe          —   (  )
  bin\USBridge_Client_app.exe  —   ( )
  bin\tailscale.exe            — Tailscale CLI (system VPN, for C-socket routing)
  bin\tailscaled.exe           — Tailscale daemon (run as service for best performance)
  bin\qemu-nbd.exe             — QEMU NBD (for   VMDK/QCOW2/VDI )
  bin\qemu-img.exe             — QEMU image tool
  lib\                         — runtime DLLs (FFmpeg, OpenSSL, MinGW runtime, etc.)

Tailscale / networking:
  The app uses built-in (embedded) Tailscale for all Go-level connections.
  Moonlight video/audio RTSP and RTP are automatically proxied via the embedded
  tsnet stack — no system Tailscale installation required.

  If you install system Tailscale (tailscaled.exe as a service or via the official
  installer at https://tailscale.com/download/windows), the bundled bin\tailscale.exe
  and bin\tailscaled.exe can be used to manage it from the command line.

  To run the bundled daemon manually (one-time, no install):
    bin\tailscaled.exe --state=tailscale.state
    bin\tailscale.exe up

Video modes:
  Moonlight streaming — libavcodec (D3D11VA hardware decode) + WASAPI audio.
    Requires: lib\avcodec-*.dll, lib\avutil-*.dll, lib\swscale-*.dll
    (bundled if FFMPEG_ROOT was set at build time).
    Falls back to software decode if DLLs are missing.

  QR code scanning (device pairing) uses Media Foundation directly —
  no GStreamer or extra DLLs required.

Hardware acceleration:
  D3D11VA: automatic on all modern Windows (DirectX 11 capable GPU).
  Software fallback: used when D3D11VA is unavailable.

Configuration:
  config.yaml next to the exe, or %APPDATA%\usbridge-client\
README

echo -e "\n${YELLOW}📦 Creating archive...${NC}"
cd "$DIST_WIN"
if command -v zip >/dev/null 2>&1; then
    zip -rq "../USBridgeClient-Windows.zip" ./*
elif command -v powershell >/dev/null 2>&1; then
    powershell -NoProfile -NonInteractive -Command "Compress-Archive -Path '.\*' -DestinationPath '..\USBridgeClient-Windows.zip' -Force"
else
    :
fi
cd "$REPO_ROOT"

if [ "$MISSING_DLLS_FOUND" = "1" ]; then
    echo -e "\n${RED}❌ Build completed with warnings!${NC}"
    echo -e "${RED}Some required DLLs were not found.${NC}"
    echo -e "   pacman -S mingw-w64-ucrt-x86_64-brotli mingw-w64-ucrt-x86_64-libjxl mingw-w64-ucrt-x86_64-libogg"
    exit 1
fi

echo -e "\n${GREEN}✅ Build completed!${NC}"
echo -e "   Archive:     dist/USBridgeClient-Windows.zip"
