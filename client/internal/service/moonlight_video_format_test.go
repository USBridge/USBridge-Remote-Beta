//go:build (darwin || ios || linux) && !android && cgo

package service

import (
	"testing"

	"usbridge-client/internal/models"
)

// TestVideoFormatRoundTrip pins the VIDEO_FORMAT_* bitmask mapping used on
// both sides of the negotiated-codec pipeline: moonlightVideoFormat() picks
// the bitmask the client requests when starting a connection, and
// videoFormatCodecName() decodes the bitmask moonlight-common-c reports back
// via dr_setup's NegotiatedVideoFormat. If these ever drift out of sync, the
// client would ask for one codec but report a different one as "negotiated".
func TestVideoFormatRoundTrip(t *testing.T) {
	cases := []struct {
		mode  string
		codec string
	}{
		{models.VideoModeH264, "h264"},
		{models.VideoModeH265, "h265"},
		{models.VideoModeAV1, "av1"},
	}

	for _, c := range cases {
		format := moonlightVideoFormat(c.mode, false)
		gotCodec, ok := videoFormatCodecName(int32(format))
		if !ok {
			t.Errorf("videoFormatCodecName(moonlightVideoFormat(%q)=0x%04X) reported no codec", c.mode, format)
			continue
		}
		if gotCodec != c.codec {
			t.Errorf("mode %q -> format 0x%04X -> codec %q, want %q", c.mode, format, gotCodec, c.codec)
		}
	}
}

func TestVideoFormatCodecNameUnknownAndUnset(t *testing.T) {
	if _, ok := videoFormatCodecName(-1); ok {
		t.Error("videoFormatCodecName(-1) should report no codec (sentinel for \"no session yet\")")
	}
	if _, ok := videoFormatCodecName(0); ok {
		t.Error("videoFormatCodecName(0) should report no codec (no format bits set)")
	}
}

func TestMoonlightVideoFormatDefaultsToH264(t *testing.T) {
	if got := moonlightVideoFormat("bogus-mode", false); got != 0x0001 {
		t.Errorf("moonlightVideoFormat(bogus) = 0x%04X, want VIDEO_FORMAT_H264 (0x0001)", got)
	}
}

// TestMoonlightVideoFormatColor444 pins the RustShine Pro color upgrade's
// bitmask mapping: h265+color444 must request VIDEO_FORMAT_H265_REXT8_444
// (0x0400, still classified as "h265" by videoFormatCodecName's 0x0F00
// mask), and color444 must be silently ignored for every other mode since
// this project's hardware encode path has no H.264/AV1 4:4:4 profile.
func TestMoonlightVideoFormatColor444(t *testing.T) {
	if got := moonlightVideoFormat(models.VideoModeH265, true); got != 0x0400 {
		t.Errorf("moonlightVideoFormat(h265, color444=true) = 0x%04X, want VIDEO_FORMAT_H265_REXT8_444 (0x0400)", got)
	}
	if codec, ok := videoFormatCodecName(0x0400); !ok || codec != models.VideoModeH265 {
		t.Errorf("videoFormatCodecName(0x0400) = (%q, %v), want (%q, true)", codec, ok, models.VideoModeH265)
	}
	if got := moonlightVideoFormat(models.VideoModeH264, true); got != 0x0001 {
		t.Errorf("moonlightVideoFormat(h264, color444=true) = 0x%04X, want plain VIDEO_FORMAT_H264 (0x0001) -- color444 has no H264 profile", got)
	}
	if got := moonlightVideoFormat(models.VideoModeAV1, true); got != 0x1000 {
		t.Errorf("moonlightVideoFormat(av1, color444=true) = 0x%04X, want plain VIDEO_FORMAT_AV1_MAIN8 (0x1000) -- color444 has no AV1 profile wired up", got)
	}
}
