package entitlement

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ProgressFunc reports cumulative bytes downloaded (not extracted) so far
// and the total, mirroring internal/update.ProgressFunc's exact contract —
// same nil-is-fine, same "0 total means unknown" convention, same
// UI-thread-hopping obligation on the caller. Kept as a separate type
// (not literally update.ProgressFunc) so this package has no import
// dependency on internal/update at all — different trust root, different
// URL source, nothing to gain from coupling the two.
type ProgressFunc func(downloaded, total int64)

const progressInterval = 100 * time.Millisecond
const downloadTimeout = 5 * time.Minute

// binaryName is bin/gamestream-server's build output name (its Cargo.toml
// package name), mirrored from streamhost.rustshineBackend's own
// unexported binaryName() -- duplicated rather than imported so this
// package stays self-contained (see its doc comment).
func binaryName() string {
	if runtime.GOOS == "windows" {
		return "gamestream-server.exe"
	}
	return "gamestream-server"
}

// StagePath is exactly what streamhost.rustshineBackend.BinaryPath()
// resolves to (see rustshine_backend.go) -- staging here means zero
// changes needed on that side once a download completes.
//
// Deliberately under stateDir (e.g. ~/Library/Application Support/
// usbridge-agent on macOS), never under exeDir/the app's own install
// directory: on macOS exeDir is inside the signed, notarized .app bundle
// (Contents/MacOS), and writing a new file into it post-install breaks the
// bundle's code signature seal (`codesign --verify` reports "a sealed
// resource is missing or invalid" the moment a staged binary lands there --
// confirmed live). A bundle with a broken seal is treated as tampered by
// macOS's TCC subsystem, which silently denies every permission check
// (Screen Recording, etc.) for it and everything it launches, no matter how
// many times the user grants access in System Settings -- this was the
// actual root cause behind RustShine's ScreenCaptureKit captures always
// failing, not a missing permission. stateDir is never part of any signed
// bundle on any platform, so this can't happen there. Same directory
// TokenFilePath already uses, for the same reason.
func StagePath(stateDir string) string {
	return filepath.Join(stateDir, "rustshine", binaryName())
}

// stagedVersionPath is a plain-text marker file recording which release
// (the backend's release tag, e.g. "gamestream-server-v0.2.2") was most
// recently staged at StagePath -- written by StageRustShine, read by
// CheckRustShineUpdate so a later check can tell whether a newer build
// exists without downloading anything just to find out.
func stagedVersionPath(stateDir string) string {
	return filepath.Join(filepath.Dir(StagePath(stateDir)), "VERSION")
}

// StagedVersion returns whichever version string StageRustShine last
// recorded, or "" if RustShine has never been staged, or was staged by a
// build of this agent that predated version tracking.
func StagedVersion(stateDir string) string {
	b, err := os.ReadFile(stagedVersionPath(stateDir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// CheckRustShineUpdate asks the backend for the latest RustShine version
// (the same cheap metadata call StageRustShine itself makes first, without
// downloading the actual multi-MB archive) and reports whether it's newer
// than what's already staged. A no-op (false, "", nil) when nothing is
// staged yet -- that first download is the explicit "Download RustShine"
// button's job (see ui/window.go), not this background check's.
//
// This exists so a fix that only lives in a newer RustShine build (like the
// NV_KEYBOARD_PACKET modifiers-byte fix this was added alongside) actually
// reaches an agent that already staged RustShine before that fix shipped --
// previously nothing ever re-checked after the first download, so an old
// binary would sit there indefinitely regardless of how many new releases
// were published. See App.checkRustShineUpdate (agent/internal/app) for the
// periodic caller.
func CheckRustShineUpdate(ctx context.Context, stateDir, entitlementToken string) (needsUpdate bool, latestVersion string, err error) {
	if _, statErr := os.Stat(StagePath(stateDir)); statErr != nil {
		return false, "", nil
	}
	platform := Platform()
	if platform == "" {
		return false, "", nil
	}
	info, err := ResolveDownload(ctx, entitlementToken, platform)
	if err != nil {
		return false, "", fmt.Errorf("entitlement: resolve download: %w", err)
	}
	if info.Version == "" {
		// Backend couldn't determine a version (e.g. GitHub release lookup
		// failed transiently) -- don't force an update off degraded
		// metadata, just wait for the next check.
		return false, "", nil
	}
	if info.Version == StagedVersion(stateDir) {
		return false, info.Version, nil
	}
	return true, info.Version, nil
}

// StageRustShine resolves, downloads, verifies, and extracts the RustShine
// build for this platform, atomically replacing whatever's already staged
// at StagePath(stateDir). entitlementToken must already have passed Verify
// locally; ResolveDownload also independently re-checks it against the
// backend, which is the actual authority (a token can be locally
// well-formed and unexpired but the backend may still refuse to serve a
// download for other reasons).
func StageRustShine(ctx context.Context, stateDir, entitlementToken string, onProgress ProgressFunc) error {
	platform := Platform()
	if platform == "" {
		return fmt.Errorf("entitlement: no RustShine build for this platform (%s/%s)", runtime.GOOS, runtime.GOARCH)
	}

	info, err := ResolveDownload(ctx, entitlementToken, platform)
	if err != nil {
		return fmt.Errorf("entitlement: resolve download: %w", err)
	}

	dlCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	archivePath, err := downloadArchive(dlCtx, info.URL, info.SHA256, onProgress)
	if err != nil {
		return err
	}
	defer os.Remove(archivePath)

	dest := StagePath(stateDir)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("entitlement: create rustshine dir: %w", err)
	}
	if runtime.GOOS == "windows" {
		err = extractFromZip(archivePath, binaryName(), dest)
	} else {
		err = extractFromTarGz(archivePath, binaryName(), dest)
	}
	if err != nil {
		return err
	}
	if info.Version != "" {
		// Best-effort -- a failure to record the version just means the
		// next CheckRustShineUpdate can't tell this build apart from an
		// older one and treats it as needing another update, which costs
		// one redundant re-download, not a wrong or stuck state.
		_ = os.WriteFile(stagedVersionPath(stateDir), []byte(info.Version), 0o644)
	}
	return nil
}

func downloadArchive(ctx context.Context, url, wantSHA256Hex string, onProgress ProgressFunc) (path string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "usbridge-agent-entitlement")

	resp, err := httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("entitlement: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("entitlement: download: unexpected status %d", resp.StatusCode)
	}

	f, err := os.CreateTemp("", "usbridge-rustshine-*")
	if err != nil {
		return "", err
	}
	tmpPath := f.Name()
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(tmpPath)
		}
	}()

	h := sha256.New()
	dst := io.MultiWriter(f, h)
	if onProgress != nil {
		pw := &progressWriter{total: resp.ContentLength, onProgress: onProgress}
		onProgress(0, resp.ContentLength)
		dst = io.MultiWriter(dst, pw)
	}
	if _, err = io.Copy(dst, resp.Body); err != nil {
		return "", fmt.Errorf("entitlement: download: %w", err)
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if onProgress != nil && resp.ContentLength > 0 {
		onProgress(resp.ContentLength, resp.ContentLength)
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != wantSHA256Hex {
		err = fmt.Errorf("entitlement: downloaded archive hash mismatch (got %s, backend says %s) — refusing to stage, this download may have been tampered with", got, wantSHA256Hex)
		return "", err
	}
	return tmpPath, nil
}

type progressWriter struct {
	total      int64
	written    int64
	lastReport time.Time
	onProgress ProgressFunc
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.written += int64(len(p))
	if now := time.Now(); now.Sub(w.lastReport) >= progressInterval {
		w.lastReport = now
		w.onProgress(w.written, w.total)
	}
	return len(p), nil
}

// extractFromTarGz installs every regular file entry from a .tar.gz next to
// dest (flattened by filepath.Base, ignoring whatever directory prefix the
// CI packaging step wrapped it all in -- see rust-shine's
// release-gamestream-server.yml, which always wraps the binary in a
// same-named directory before archiving): the entry named wantBase becomes
// dest itself (the binary streamhost.rustshineBackend.BinaryPath() expects),
// and every other entry (e.g. a bundled opus.dll the Windows build needs at
// runtime -- see gamestream-server's own "failed to load libopus:
// LoadLibraryExW failed" if it's missing) is staged alongside it in the same
// directory, since LoadLibraryExW resolves a bare DLL name by first
// searching the calling process's own directory. Previously this discarded
// every entry except wantBase, so a sibling DLL shipped in the release
// archive never survived staging even though the archive had it all along.
func extractFromTarGz(archivePath, wantBase, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("entitlement: open gzip: %w", err)
	}
	defer gz.Close()

	destDir := filepath.Dir(dest)
	foundBinary := false
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("entitlement: read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(hdr.Name)
		if base == wantBase {
			if err := writeAtomic(dest, tr, 0o755); err != nil {
				return err
			}
			foundBinary = true
			continue
		}
		if err := writeAtomic(filepath.Join(destDir, base), tr, 0o644); err != nil {
			return fmt.Errorf("entitlement: stage sibling file %q: %w", base, err)
		}
	}
	if !foundBinary {
		return fmt.Errorf("entitlement: archive has no entry named %q", wantBase)
	}
	return nil
}

// extractFromZip is extractFromTarGz's .zip counterpart -- same
// stage-everything-not-just-the-binary behavior, same reasoning (see its
// doc comment).
func extractFromZip(archivePath, wantBase, dest string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("entitlement: open zip: %w", err)
	}
	defer zr.Close()

	destDir := filepath.Dir(dest)
	foundBinary := false
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(zf.Name)
		rc, err := zf.Open()
		if err != nil {
			return fmt.Errorf("entitlement: open zip entry: %w", err)
		}
		if base == wantBase {
			err = writeAtomic(dest, rc, 0o755)
			rc.Close()
			if err != nil {
				return err
			}
			foundBinary = true
			continue
		}
		err = writeAtomic(filepath.Join(destDir, base), rc, 0o644)
		rc.Close()
		if err != nil {
			return fmt.Errorf("entitlement: stage sibling file %q: %w", base, err)
		}
	}
	if !foundBinary {
		return fmt.Errorf("entitlement: archive has no entry named %q", wantBase)
	}
	return nil
}

// writeAtomic streams src into a temp file next to dest, then renames it
// into place -- so a crash or concurrent read mid-write never leaves a
// truncated binary at dest (same-directory rename is atomic on every OS
// this agent ships for, as long as the temp file and dest share a
// filesystem, which they do here).
func writeAtomic(dest string, src io.Reader, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".rustshine-download-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		tmp.Close()
		if !ok {
			os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmp, src); err != nil {
		return fmt.Errorf("entitlement: write staged binary: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := renameWithRetry(tmpPath, dest); err != nil {
		return fmt.Errorf("entitlement: install staged binary: %w", err)
	}
	ok = true
	return nil
}

// renameWithRetry retries os.Rename on failure. On Windows, replacing a
// just-killed process's own .exe can still fail with "Access is denied"
// well after the process is gone -- the OS doesn't necessarily release the
// image section's file lock the instant the process exits (confirmed live:
// a caller-side 500ms wait after Stop() before staging still wasn't always
// enough), and a forcibly-killed (not gracefully exited) process makes this
// worse, not better: TerminateProcess doesn't wait for the image section's
// last reference to actually drop, and Windows Defender's real-time
// protection scanning the freshly-renamed executable is a well-documented
// separate cause of the exact same error that can hold a lock for several
// seconds on its own -- confirmed live as the actual bound here: the
// original 10*300ms (3s) budget was NOT always enough and a real update
// failed with this exact error after exhausting it. 40*500ms (~20s) is a
// no-op extra cost everywhere else: the first attempt succeeds immediately
// on Linux/macOS (rename-over-open-file is always allowed there) and in the
// normal Windows case too (the file usually IS free within the old 3s
// window) -- this only spins longer when the first attempt actually failed
// AND a real lock is still held past where the old budget gave up.
func renameWithRetry(oldpath, newpath string) error {
	const attempts = 40
	const delay = 500 * time.Millisecond
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			time.Sleep(delay)
		}
		if err = os.Rename(oldpath, newpath); err == nil {
			return nil
		}
	}
	return err
}
