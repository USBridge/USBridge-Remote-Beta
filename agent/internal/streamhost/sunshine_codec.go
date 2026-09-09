package streamhost

import (
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// sessionStartMarkers are the Sunshine log lines that mark the start of a
// real streaming session (as opposed to the capability-probe phase that runs
// before every negotiation and logs a "Creating encoder [...]" line for
// *every* codec it tests, not just the one actually chosen).
var sessionStartMarkers = []string{"CLIENT CONNECTED", "New streaming session started"}

// sessionEndMarker closes out a session opened by one of sessionStartMarkers.
const sessionEndMarker = "CLIENT DISCONNECTED"

// creatingEncoderCodec extracts the codec family from a Sunshine
// "Creating encoder [<name>]" log line, e.g. "h264_videotoolbox" -> "h264",
// "hevc_nvenc" -> "h265", "libx264" -> "h264", "libsvtav1" -> "av1". Returns
// ("", false) if the line isn't a "Creating encoder" line or the bracketed
// name doesn't match a known codec family. Matches by substring because
// Sunshine's encoder names vary per backend (nvenc/qsv/vaapi/amf/
// videotoolbox/software) but consistently embed the codec: see
// itsme228/Sunshine's video.cpp encoder table (h264_*, hevc_*, av1_*,
// libx264, libx265, libsvtav1).
func creatingEncoderCodec(line string) (string, bool) {
	const marker = "Creating encoder ["
	idx := strings.Index(line, marker)
	if idx < 0 {
		return "", false
	}
	rest := line[idx+len(marker):]
	end := strings.IndexByte(rest, ']')
	if end < 0 {
		return "", false
	}
	name := strings.ToLower(rest[:end])
	switch {
	case strings.Contains(name, "hevc") || strings.Contains(name, "265"):
		return "h265", true
	case strings.Contains(name, "av1"):
		return "av1", true
	case strings.Contains(name, "264"):
		return "h264", true
	default:
		return "", false
	}
}

// CurrentVideoCodec returns the codec Sunshine actually created an encoder
// for in the most recent streaming session, defaulting to "h264" if unable
// to determine. This is a best-effort, log-derived hint for the pre-launch
// UI only — once a Moonlight client is actually connected, the client's own
// negotiated-codec report (from moonlight-common-c's dr_setup) is the
// authoritative source, since it reflects exactly what the server accepted.
func (b *sunshineBackend) CurrentVideoCodec() string {
	b.mu.Lock()
	logFile := b.logPath
	b.mu.Unlock()

	if logFile == "" {
		return "h264"
	}
	f, err := os.Open(logFile)
	if err != nil {
		return "h264"
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return "h264"
	}
	size := stat.Size()
	if size == 0 {
		return "h264"
	}
	// Large enough to comfortably span one session's probe+connect sequence
	// (a real session on this fork is ~2-3KB) while staying bounded.
	readSize := int64(32 * 1024)
	if size < readSize {
		readSize = size
	}
	if _, err := f.Seek(-readSize, io.SeekEnd); err != nil {
		return "h264"
	}
	buf := make([]byte, readSize)
	n, _ := f.Read(buf)
	lines := strings.Split(string(buf[:n]), "\n")

	// Anchor on the most recent session-start marker so leftover
	// capability-probe lines from BEFORE that marker (logged for every
	// codec Sunshine tests, not just the one chosen) can't be mistaken for
	// the real, active pick — this was the root cause of the codec display
	// being effectively random/stale.
	startIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		for _, marker := range sessionStartMarkers {
			if strings.Contains(lines[i], marker) {
				startIdx = i
				break
			}
		}
		if startIdx >= 0 {
			break
		}
	}

	if startIdx >= 0 {
		// Also bound the search at that session's end (or EOF if it's still
		// running). Without this, a LATER, unrelated capability probe — e.g.
		// Sunshine re-testing encoders to answer a client's /serverinfo
		// query without actually starting a stream — would still be picked
		// up as if it were part of this session.
		endIdx := len(lines)
		for i := startIdx + 1; i < len(lines); i++ {
			if strings.Contains(lines[i], sessionEndMarker) {
				endIdx = i
				break
			}
		}
		for i := endIdx - 1; i >= startIdx; i-- {
			if codec, ok := creatingEncoderCodec(lines[i]); ok {
				log.Printf("[sunshine] detected active codec=%s (source=session-anchored, line=%q)", codec, strings.TrimSpace(lines[i]))
				return codec
			}
		}
		log.Printf("[sunshine] session found (lines %d-%d) but no encoder-creation line within it — defaulting to h264", startIdx, endIdx)
		return "h264"
	}

	// No session-start marker in the window yet (e.g. called before any
	// client has ever connected): fall back to the previous unanchored
	// best-effort scan so we still return something reasonable pre-launch.
	for i := len(lines) - 1; i >= 0; i-- {
		if codec, ok := creatingEncoderCodec(lines[i]); ok {
			log.Printf("[sunshine] detected codec=%s (source=unanchored-fallback, line=%q)", codec, strings.TrimSpace(lines[i]))
			return codec
		}
	}
	log.Printf("[sunshine] could not detect active codec from log tail, defaulting to h264")
	return "h264"
}

// Limelight.h SCM_* codec-mode-support bit flags (see moonlight-common-c's
// Limelight.h, shared by every Moonlight-protocol client and server). Sunshine
// reports these in /serverinfo's ServerCodecModeSupport field, computed live
// per request by actually probing the host's hardware encoders — this is the
// one source of truth for "which codecs can this machine actually encode
// right now," as opposed to a client's request or a log-scraped guess.
const (
	scmH264          = 0x00000001
	scmHEVC          = 0x00000100
	scmHEVCMain10    = 0x00000200
	scmAV1Main8      = 0x00010000
	scmAV1Main10     = 0x00020000
	scmH264High8444  = 0x00040000
	scmHEVCRext8444  = 0x00080000
	scmHEVCRext10444 = 0x00100000
	scmAV1High8444   = 0x00200000
	scmAV1High10444  = 0x00400000

	scmMaskH264 = scmH264 | scmH264High8444
	scmMaskHEVC = scmHEVC | scmHEVCMain10 | scmHEVCRext8444 | scmHEVCRext10444
	scmMaskAV1  = scmAV1Main8 | scmAV1Main10 | scmAV1High8444 | scmAV1High10444
)

type serverInfoXML struct {
	XMLName                xml.Name `xml:"root"`
	ServerCodecModeSupport int      `xml:"ServerCodecModeSupport"`
}

// SupportedVideoCodecs queries Sunshine's own /serverinfo endpoint — the
// standard, unauthenticated GameStream discovery response every Moonlight
// client already reads ServerCodecModeSupport from — and decodes it into
// which of h264/h265/av1 this host's hardware encoder can actually produce
// right now. h264 is always included, both because Sunshine's protocol
// always advertises SCM_H264 and as the safe fallback if the query fails
// (Sunshine not up yet, port mismatch, etc.) — never silently offer h265/av1
// as "supported" when we simply couldn't check.
//
// This is not just an optimization: Sunshine's /serverinfo does a LIVE
// hardware probe on every single call (get_codec_mode_flags() actually
// creates, tests, and tears down h264/hevc/av1 VideoToolbox/NVENC/etc.
// encoder sessions each time — see the "Trying encoder"/"Creating
// encoder"/"Couldn't open" sequence it logs), not a cached readout.
// Confirmed live: with multiple clients polling /api/video/info roughly
// every 10s each, a short cache here meant re-hitting /serverinfo about that
// often, which measurably interfered with a real client's session
// negotiation -- one connection attempt stalled for ~2.5 minutes,
// repeatedly re-probing instead of ever reaching "New streaming session
// started", until the probing happened to let a launch through. Since
// hardware encoder capability cannot change while Sunshine keeps running,
// there is no reason to re-probe more than very rarely.
// Color444Status: Sunshine (opensource) never offers the RustShine Pro
// color upgrade -- it's a RustShine-only feature, see CodecProbe's doc
// comment.
func (b *sunshineBackend) Color444Status() (active bool, available bool) {
	return false, false
}

func (b *sunshineBackend) SupportedVideoCodecs(adminPort int) []string {
	b.supportedCodecsCache.mu.Lock()
	if !b.supportedCodecsCache.fetchedAt.IsZero() && time.Since(b.supportedCodecsCache.fetchedAt) < supportedCodecsCacheTTL {
		cached := b.supportedCodecsCache.codecs
		b.supportedCodecsCache.mu.Unlock()
		return cached
	}
	b.supportedCodecsCache.mu.Unlock()

	codecs := fetchSupportedVideoCodecs(adminPort)

	b.supportedCodecsCache.mu.Lock()
	b.supportedCodecsCache.codecs = codecs
	b.supportedCodecsCache.fetchedAt = time.Now()
	b.supportedCodecsCache.mu.Unlock()
	return codecs
}

// serverinfoHTTPClient is shared across every fetchSupportedVideoCodecs
// call (both backends use this function) rather than a fresh
// `&http.Client{...}` per call -- see rustshineAdminHTTPClient's doc
// comment in rustshine_codec.go for why a throwaway Client/Transport per
// call leaks a persistent connection instead of reusing one. Lower-impact
// here (this call is cached for supportedCodecsCacheTTL, 30 minutes, not
// polled every 2 seconds like CurrentVideoCodec), but the same fix applies.
var serverinfoHTTPClient = &http.Client{Timeout: 2 * time.Second}

func fetchSupportedVideoCodecs(adminPort int) []string {
	fallback := []string{"h264"}
	if adminPort <= 0 {
		adminPort = 47990
	}
	// The plain NvHTTP port (unauthenticated /serverinfo, same one every
	// Moonlight client falls back to) is the admin/web-UI port minus 1 — see
	// UpdateSunshineStreamAddr's "webPort := streamPort + 1" in app.go.
	nvhttpPort := adminPort - 1

	resp, err := serverinfoHTTPClient.Get(fmt.Sprintf("http://127.0.0.1:%d/serverinfo", nvhttpPort))
	if err != nil {
		log.Printf("[sunshine] serverinfo query failed (%v) — reporting h264-only", err)
		return fallback
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[sunshine] serverinfo read failed (%v) — reporting h264-only", err)
		return fallback
	}

	var info serverInfoXML
	if err := xml.Unmarshal(body, &info); err != nil {
		log.Printf("[sunshine] serverinfo parse failed (%v) — reporting h264-only", err)
		return fallback
	}

	flags := info.ServerCodecModeSupport
	codecs := []string{"h264"}
	if flags&scmMaskHEVC != 0 {
		codecs = append(codecs, "h265")
	}
	if flags&scmMaskAV1 != 0 {
		codecs = append(codecs, "av1")
	}
	log.Printf("[sunshine] serverinfo codec support: flags=0x%08X -> %v", flags, codecs)
	return codecs
}
