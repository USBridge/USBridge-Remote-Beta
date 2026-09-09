//go:build android && cgo

package service

/*
#cgo CFLAGS: -I${SRCDIR}/../../moonlight-common-c/src -I${SRCDIR}/../../moonlight-common-c/enet/include
#cgo CFLAGS: -I${SRCDIR}/../../moonlight-common-c/build/android/opus/include/opus
#cgo android CFLAGS: -D__ANDROID_UNAVAILABLE_SYMBOLS_ARE_WEAK__
#cgo LDFLAGS: -L${SRCDIR}/../../moonlight-common-c/build/android -lmoonlight-common-c -lenet -lssl -lcrypto
#cgo LDFLAGS: -L${SRCDIR}/../../moonlight-common-c/build/android/opus/lib -lopus
#cgo LDFLAGS: -lmediandk -laaudio -lm -ldl -landroid -lEGL -lGLESv2

#include <media/NdkMediaCodec.h>
#include <media/NdkMediaFormat.h>
#include <aaudio/AAudio.h>
#include <android/log.h>
#include <android/native_window_jni.h>
#include <jni.h>
#include <Limelight.h>
#include <opus_multistream.h>
#include <stdarg.h>
#include <stdlib.h>
#include <string.h>

#define ALOGI(...) __android_log_print(ANDROID_LOG_INFO,  "Moonlight/CGO", __VA_ARGS__)
#define ALOGE(...) __android_log_print(ANDROID_LOG_ERROR, "Moonlight/CGO", __VA_ARGS__)

extern void goMoonlightStage(int stage, int result, int errCode);
extern void goMoonlightConnected(void);
extern void goMoonlightTerminated(int errCode);
extern void goVTLog(char *msg);
extern void goVTFrame(uint8_t *rgba, int width, int height, int stride);

// Declared in gl_video_impl_android.c
extern void         android_gl_set_jvm(JavaVM *jvm, jobject ctx);
extern ANativeWindow *android_gl_init(int width, int height);
extern uint8_t      *android_gl_get_frame(int width, int height);
extern void         *android_gl_get_frame_hwbuffer(int width, int height);
extern void          android_gl_release(void);

// Declared in vk_video_impl_android.c
extern void android_vk_set_jvm(JavaVM *jvm, jobject ctx);
extern int  android_vk_is_active(void);
extern int  android_vk_try_submit(uint8_t *rgba, int width, int height, int stride);
extern int  android_vk_try_submit_hwbuffer(void *ahb, int width, int height);
extern int  android_vk_hwbuffer_supported(void);

// ── Shared state ──────────────────────────────────────────────────────────────

#include <stdatomic.h>
// g_li_active uses a proper C11 atomic to ensure cross-thread visibility
// on ARM64 (plain volatile is insufficient for inter-thread signalling).
static _Atomic int    g_li_active      = 0;
static volatile int   g_audio_muted    = 0;
static AAudioStream  *g_aa_stream      = NULL;
// Sticks at AAUDIO_PERFORMANCE_MODE_NONE once ar_decode gives up on
// LOW_LATENCY (see the XRun-at-capacity escalation below) so every
// subsequent aaudio_open() -- including the ones triggered by write
// errors/stuck-stream recovery elsewhere in this file -- keeps using the
// mode that's actually sustainable on this device instead of reverting
// back to the one that was causing continuous underruns.
static aaudio_performance_mode_t g_aa_perf_mode = AAUDIO_PERFORMANCE_MODE_LOW_LATENCY;
static int            g_audio_channels = 2;
static int            g_samples_per_frame = 240; // 5ms @ 48kHz default, overwritten by ar_init
static OpusMSDecoder *g_opus_decoder   = NULL;
static AMediaCodec   *g_amc            = NULL;
static int            g_amc_w          = 0;
static int            g_amc_h          = 0;
static uint64_t       g_amc_pts        = 0;

// ── Connection callbacks ──────────────────────────────────────────────────────

static void cl_stage_starting(int s) {
    ALOGI("cl_stage_starting: %d", s);
    goMoonlightStage(s, 0, 0);
}
static void cl_stage_complete(int s) {
    ALOGI("cl_stage_complete: %d", s);
    goMoonlightStage(s, 1, 0);
}
static void cl_stage_failed(int s, int ec) {
    ALOGE("cl_stage_failed: stage=%d, error=%d", s, ec);
    goMoonlightStage(s, -1, ec);
}

// ── Audio: Opus → AAudio ─────────────────────────────────────────────────────

static void aaudio_open(int channels, int sample_rate) {
    if (g_aa_stream) {
        // A stream from a previous session is still around — this happens when
        // ar_init fires again before that session's ar_cleanup ran (overlapping
        // reconnects, a second client racing the first, etc). Reusing it here
        // used to be a silent no-op: opus keeps decoding fine and
        // AAudioStream_write keeps returning success into a stream that may not
        // match this session's channel count/rate (or may just be a dead/
        // orphaned handle), so playback goes silent with zero errors anywhere
        // in the pipeline. Always tear down and rebuild fresh instead of trusting
        // a stream we didn't just create.
        ALOGI("aaudio_open: closing stale stream from a previous session before opening a fresh one");
        AAudioStream_requestStop(g_aa_stream);
        AAudioStream_close(g_aa_stream);
        g_aa_stream = NULL;
    }
    AAudioStreamBuilder *builder = NULL;
    AAudio_createStreamBuilder(&builder);
    AAudioStreamBuilder_setDirection(builder,       AAUDIO_DIRECTION_OUTPUT);
    AAudioStreamBuilder_setSharingMode(builder,     AAUDIO_SHARING_MODE_SHARED);
    AAudioStreamBuilder_setFormat(builder,          AAUDIO_FORMAT_PCM_I16);
    AAudioStreamBuilder_setChannelCount(builder,    channels);
    AAudioStreamBuilder_setSampleRate(builder,      (int32_t)sample_rate);
    AAudioStreamBuilder_setPerformanceMode(builder, g_aa_perf_mode);
    aaudio_result_t status = AAudioStreamBuilder_openStream(builder, &g_aa_stream);
    AAudioStreamBuilder_delete(builder);
    if (status == AAUDIO_OK) {
        int32_t burst = AAudioStream_getFramesPerBurst(g_aa_stream);
        // Set a larger initial buffer (e.g. 4x burst size) to prevent crackling
        // (XRuns) during the first few seconds of connection before Android's
        // internal adaptive buffer algorithm kicks in.
        AAudioStream_setBufferSizeInFrames(g_aa_stream, burst * 4);

        AAudioStream_requestStart(g_aa_stream);
        ALOGI("AAudio stream started: ch=%d rate=%d burst=%d perf_mode=%d",
              channels, sample_rate, burst, (int)g_aa_perf_mode);
    } else {
        ALOGE("AAudio openStream failed: %d", (int)status);
        g_aa_stream = NULL;
    }
}

// ar_init: called by Moonlight with Opus multistream config.
static int ar_init(int audioConfig, const POPUS_MULTISTREAM_CONFIGURATION cfg, void *ctx, int arFlags) {
    (void)audioConfig; (void)ctx; (void)arFlags;
    ALOGI("ar_init: ch=%d rate=%d streams=%d coupled=%d",
          cfg->channelCount, cfg->sampleRate, cfg->streams, cfg->coupledStreams);
    g_audio_channels = cfg->channelCount;
    g_samples_per_frame = cfg->samplesPerFrame > 0 ? cfg->samplesPerFrame : 240;
    if (g_opus_decoder) { opus_multistream_decoder_destroy(g_opus_decoder); g_opus_decoder = NULL; }
    int err = OPUS_OK;
    g_opus_decoder = opus_multistream_decoder_create(
        cfg->sampleRate, cfg->channelCount,
        cfg->streams, cfg->coupledStreams, cfg->mapping, &err);
    if (err != OPUS_OK) { ALOGE("opus create failed: %d", err); return -1; }
    aaudio_open(cfg->channelCount, (int)cfg->sampleRate);
    return 0;
}

static void ar_cleanup(void) {
    ALOGI("ar_cleanup");
    if (g_aa_stream) {
        AAudioStream_requestStop(g_aa_stream);
        AAudioStream_close(g_aa_stream);
        g_aa_stream = NULL;
    }
    if (g_opus_decoder) { opus_multistream_decoder_destroy(g_opus_decoder); g_opus_decoder = NULL; }
}

static void ar_start(void) {}
static void ar_stop(void)  {}

// ar_decode: called with Opus-encoded packet; decode → AAudio.
static void ar_decode(char *data, int len) {
    if (!g_opus_decoder || !g_aa_stream) return;
    opus_int16 pcm[5760 * 8];
    // For a real packet, frame_size is just the max output buffer capacity —
    // opus infers the actual decoded sample count from the packet itself, so
    // keep this generous (the full buffer) to avoid OPUS_BUFFER_TOO_SMALL (and
    // therefore total silence) if a packet ever encodes more than one nominal
    // frame's worth of audio.
    // For concealment (data==NULL, on packet loss) opus has no packet to infer
    // length from and will generate exactly frame_size samples, so that case
    // must use the host's real samplesPerFrame — passing the 5760 buffer cap
    // there made every lost packet produce ~120ms of concealment audio instead
    // of the real ~5-10ms frame, throwing off AAudio's timeline and causing
    // periodic crackling on lossy networks.
    int frameSize = data ? 5760 : g_samples_per_frame;
    int samples = opus_multistream_decode(g_opus_decoder,
        (const unsigned char *)data, len, pcm, frameSize, 0);
    static int s_decode_calls = 0;
    static int s_decode_errs  = 0;
    static int s_write_errs   = 0;
    s_decode_calls++;
    if (samples <= 0) {
        s_decode_errs++;
        if (s_decode_errs <= 5 || (s_decode_errs % 200) == 0) {
            ALOGE("ar_decode: opus decode failed ret=%d (call #%d, %d failures so far, muted=%d)",
                  samples, s_decode_calls, s_decode_errs, g_audio_muted);
        }
        return;
    }
    if (g_audio_muted) memset(pcm, 0, (size_t)(samples * g_audio_channels * 2));

    // Adaptive buffer size to prevent crackling (XRuns) during Wi-Fi lag.
    //
    // Growing the buffer on every XRun is a losing game if XRuns keep piling
    // up regardless: on some devices/loads AAudio's own internal tuning walks
    // the buffer size back down between our checks (observed via
    // AAudioStream_getBufferSizeInFrames() never actually reading back as
    // "stuck at capacity" even though the resize we issue is clearly not
    // helping — it just re-grows to the same ceiling forever, logged on every
    // single call). So detection here does NOT depend on the resize becoming
    // a no-op; it tracks the XRun *rate* directly over a fixed window of
    // decode calls (~1s of audio at typical 5ms Opus frames) and escalates
    // once that rate crosses a threshold, however the buffer size happens to
    // be reading at the time.
    //
    // Escalation drops from AAUDIO_PERFORMANCE_MODE_LOW_LATENCY (exclusive
    // MMAP, tightest possible timing) to AAUDIO_PERFORMANCE_MODE_NONE (goes
    // through AudioFlinger's mixer, far more forgiving of scheduling jitter)
    // -- the standard Android fallback for exactly this case: LOW_LATENCY
    // can't keep its deadline when the same cores are also busy with video
    // decode + GL/Vulkan rendering. g_aa_perf_mode is process-global and only
    // ever moves LOW_LATENCY -> NONE, never back, so this can fire at most
    // once per app run: once we know LOW_LATENCY doesn't work on this device
    // under real load, there's no reason to keep re-trying it every session.
    #define AUDIO_XRUN_WINDOW_CALLS  200 // ~1s of audio at 5ms/frame
    #define AUDIO_XRUN_WINDOW_LIMIT   20 // >10% of frames underrunning in that window
    static int32_t s_previous_xruns      = 0;
    static int      s_window_start_call  = 0;
    static int      s_window_xruns       = 0;
    int32_t xruns = AAudioStream_getXRunCount(g_aa_stream);
    if (xruns > s_previous_xruns) {
        s_previous_xruns = xruns;
        s_window_xruns++;

        int32_t current_size = AAudioStream_getBufferSizeInFrames(g_aa_stream);
        int32_t burst = AAudioStream_getFramesPerBurst(g_aa_stream);
        int32_t capacity = AAudioStream_getBufferCapacityInFrames(g_aa_stream);
        int32_t new_size = current_size + burst * 2; // Increase aggressively
        if (new_size > capacity) new_size = capacity;
        if (new_size > current_size) {
            AAudioStream_setBufferSizeInFrames(g_aa_stream, new_size);
            ALOGI("ar_decode: XRun detected (%d total, %d in current window), increased buffer to %d frames to prevent crackling",
                  xruns, s_window_xruns, new_size);
        }

        if (s_window_xruns > AUDIO_XRUN_WINDOW_LIMIT && g_aa_perf_mode == AAUDIO_PERFORMANCE_MODE_LOW_LATENCY) {
            ALOGE("ar_decode: %d XRuns in the last %d decode calls (buffer resizing isn't helping) — "
                  "falling back to AAUDIO_PERFORMANCE_MODE_NONE", s_window_xruns, s_decode_calls - s_window_start_call);
            g_aa_perf_mode = AAUDIO_PERFORMANCE_MODE_NONE;
            int rate = AAudioStream_getSampleRate(g_aa_stream);
            aaudio_open(g_audio_channels, rate);
            s_previous_xruns = 0; // reset xrun tracker for the new stream
            s_window_start_call = s_decode_calls;
            s_window_xruns = 0;
        }
    }
    if (s_decode_calls - s_window_start_call >= AUDIO_XRUN_WINDOW_CALLS) {
        s_window_start_call = s_decode_calls;
        s_window_xruns = 0;
    }

    // A single AAudioStream_write() call can return fewer frames than requested
    // (or 0) if the audio pipeline is briefly backed up (buffer momentarily
    // full) — this is normal on some devices/chipsets and does not mean the
    // frames were rejected forever, just that they didn't fit in this attempt.
    // The old code treated a short write as "done" and silently dropped the
    // remainder, which is audible as a stutter/skip. Retry the remaining
    // frames (advancing the buffer pointer by whole interleaved samples) until
    // everything is written or we give up after a few attempts.
    int written = 0;
    int attempts = 0;
    while (written < samples && attempts < 8) {
        aaudio_result_t wret = AAudioStream_write(g_aa_stream,
            pcm + (size_t)written * g_audio_channels, samples - written, 5000000LL);
        attempts++;
        if (wret < 0) {
            ALOGE("ar_decode: AAudioStream_write fatal error %d, reopening stream", (int)wret);
            int rate = AAudioStream_getSampleRate(g_aa_stream);
            aaudio_open(g_audio_channels, rate);
            break; // Will retry writing on the next audio packet
        }
        if (wret == 0) continue;
        written += (int)wret;
    }

    static int s_consec_short_writes = 0;
    if (written < samples) {
        s_consec_short_writes++;
        s_write_errs++;
        if (s_write_errs <= 5 || (s_write_errs % 200) == 0) {
            ALOGE("ar_decode: AAudioStream_write short after retries: wrote=%d requested=%d attempts=%d (call #%d, %d failures so far)",
                  written, samples, attempts, s_decode_calls, s_write_errs);
        }
        // If the stream is completely stuck (buffer full and never draining), restart it
        if (s_consec_short_writes > 10) {
            ALOGE("ar_decode: Stream is stuck (10 short writes), forcing restart to recover audio");
            int rate = AAudioStream_getSampleRate(g_aa_stream);
            aaudio_open(g_audio_channels, rate);
            s_consec_short_writes = 0; // reset to avoid infinite restart loop
            s_previous_xruns = 0; // reset xrun tracker for the new stream
        }
    } else {
        s_consec_short_writes = 0;
    }

    if ((s_decode_calls % 500) == 0) {
        ALOGI("ar_decode: heartbeat calls=%d decode_errs=%d write_errs=%d muted=%d",
              s_decode_calls, s_decode_errs, s_write_errs, g_audio_muted);
    }
}

// ── Video implementation (AMediaCodec) ──────────────────────────────────────

static void amc_init(int fmt_codec, int width, int height) {
    ALOGI("amc_init: fmt=0x%x %dx%d", fmt_codec, width, height);
    // Safety: clean up stale codec and EGL if dr_cleanup wasn't called.
    if (g_amc) {
        ALOGE("amc_init: stale g_amc found — cleaning up");
        AMediaCodec_stop(g_amc);
        AMediaCodec_delete(g_amc);
        g_amc = NULL;
        android_gl_release();  // also tear down stale EGL context
    }
    g_amc_w = width; g_amc_h = height;

    ANativeWindow *nwin = android_gl_init(width, height);
    if (!nwin) { ALOGE("amc_init: no native window"); return; }

    const char *mime = "video/avc";
    if (fmt_codec & 0x0F00) { // VIDEO_FORMAT_MASK_H265
        mime = "video/hevc";
    } else if (fmt_codec & 0xF000) { // VIDEO_FORMAT_MASK_AV1
        mime = "video/av01";
    }

    AMediaFormat *fmt = AMediaFormat_new();
    AMediaFormat_setString(fmt, "mime", mime);
    AMediaFormat_setInt32(fmt,  "width",  width);
    AMediaFormat_setInt32(fmt,  "height", height);

    g_amc = AMediaCodec_createDecoderByType(mime);
    if (g_amc && AMediaCodec_configure(g_amc, fmt, nwin, NULL, 0) == AMEDIA_OK) {
        if (AMediaCodec_start(g_amc) == AMEDIA_OK) {
            ALOGI("AMediaCodec started (%s)", mime);
        } else { ALOGE("AMediaCodec_start failed (%s)", mime); }
    } else { ALOGE("AMediaCodec configure failed (%s)", mime); }

    AMediaFormat_delete(fmt);
    if (nwin) ANativeWindow_release(nwin);
}

static int dr_setup(int fmt, int w, int h, int rate, void *ctx, int flags) {
    ALOGI("dr_setup: %dx%d@%d", w, h, rate);
    amc_init(fmt, w, h);
    return 0;
}

static int dr_submit(PDECODE_UNIT du) {
    if (!g_amc) return DR_NEED_IDR;

    // Never silently drop a decode unit: if the codec has no free input buffer
    // (e.g. momentarily backed up), returning DR_OK here would discard this
    // frame's bitstream data with no recovery. Since H.264 P-frames predict off
    // prior frames, a silently dropped frame corrupts every subsequent frame
    // until the next IDR — which never comes because nothing signaled loss.
    // DR_NEED_IDR tells moonlight-common-c to ask the host for a fresh IDR so
    // the stream self-heals, matching what the other platform backends
    // (apple/windows/linux, all using ffmpeg/VideoToolbox) already do on
    // decode failure.
    ssize_t idx = AMediaCodec_dequeueInputBuffer(g_amc, 10000);
    if (idx < 0) {
        ALOGE("dr_submit: no input buffer available (idx=%zd) — requesting IDR", idx);
        return DR_NEED_IDR;
    }
    size_t sz = 0;
    uint8_t *buf = AMediaCodec_getInputBuffer(g_amc, idx, &sz);
    if (!buf) {
        ALOGE("dr_submit: getInputBuffer failed — requesting IDR");
        return DR_NEED_IDR;
    }
    size_t written = 0;
    for (PLENTRY e = du->bufferList; e && written < sz; e = e->next) {
        size_t n = (e->length < sz - written) ? e->length : sz - written;
        memcpy(buf + written, e->data, n);
        written += n;
    }
    AMediaCodec_queueInputBuffer(g_amc, idx, 0, written, g_amc_pts += 33333, 0);

    AMediaCodecBufferInfo info;
    ssize_t out_idx = AMediaCodec_dequeueOutputBuffer(g_amc, &info, 0);
    if (out_idx >= 0) {
        AMediaCodec_releaseOutputBuffer(g_amc, out_idx, 1);
        if (android_vk_is_active() && android_vk_hwbuffer_supported()) {
            // Zero-copy path: GL renders straight into an AHardwareBuffer
            // that Vulkan imports and blits from directly -- no glReadPixels,
            // no staging-buffer upload, no CPU touches the pixels at all.
            // See gl_video_impl_android.c/vk_video_impl_android.c.
            void *ahb = android_gl_get_frame_hwbuffer(g_amc_w, g_amc_h);
            if (ahb) {
                android_vk_try_submit_hwbuffer(ahb, g_amc_w, g_amc_h);
                // NULL: frame counting/FPS only, goVTFrame never dereferences
                // the pointer while Vulkan is active (see its Go side).
                goVTFrame(NULL, g_amc_w, g_amc_h, g_amc_w * 4);
            }
        } else {
            uint8_t *rgba = android_gl_get_frame(g_amc_w, g_amc_h);
            if (rgba) {
                if (android_vk_is_active()) {
                    // Vulkan overlay active but this device/driver lacks
                    // AHardwareBuffer import support -- fall back to the CPU
                    // staging-buffer path, which works everywhere.
                    android_vk_try_submit(rgba, g_amc_w, g_amc_h, g_amc_w * 4);
                }
                // Always notify Go for frame counting and FPS stats. goVTFrame detects
                // NativeVideoOverlayIsActive() and delivers nil to the callback so no
                // Go image allocation happens while Vulkan is rendering.
                goVTFrame(rgba, g_amc_w, g_amc_h, g_amc_w * 4);
            }
        }
    }
    return DR_OK;
}

static void dr_cleanup(void) {
    ALOGI("dr_cleanup");
    if (g_amc) {
        AMediaCodec_stop(g_amc);
        AMediaCodec_delete(g_amc);
        g_amc = NULL;
    }
    android_gl_release();  // always release EGL, even if codec creation failed
}

// ── Start/Stop ────────────────────────────────────────────────────────────────

static void cl_connected(void)    { goMoonlightConnected(); }
static void cl_terminated(int ec) { goMoonlightTerminated(ec); }
static void cl_log(const char *fmt, ...) {
    char buf[256];
    va_list ap; va_start(ap, fmt); vsnprintf(buf, sizeof(buf), fmt, ap); va_end(ap);
    int n = (int)strlen(buf); if (n > 0 && buf[n-1] == '\n') buf[n-1] = '\0';
    if (buf[0]) ALOGI("[Moonlight] %s", buf);
}

int do_li_start(const char *address, const char *appV, const char *gfeV, const char *rtsp,
                int codec, int videoFmt, int w, int h, int fps, int bit,
                const unsigned char *key, int kid, int pipeFd) {
    (void)pipeFd;
    if (videoFmt == 0) videoFmt = VIDEO_FORMAT_H264;
    ALOGI("do_li_start: addr=%s rtsp=%s codec=%d videoFmt=%d %dx%d@%d bit=%d", address, rtsp, codec, videoFmt, w, h, fps, bit);

    // Safety: unconditionally stop any lingering C-level connection before
    // starting a new one.  LiStopConnection is a no-op when stage==STAGE_NONE,
    // so this is safe even when the Go layer already called do_li_force_stop.
    atomic_store(&g_li_active, 0);
    LiStopConnection();

    g_amc_w = w; g_amc_h = h;

    SERVER_INFORMATION srv;
    LiInitializeServerInformation(&srv);
    srv.address                = address;
    srv.serverInfoAppVersion   = appV;
    srv.serverInfoGfeVersion   = gfeV;
    srv.rtspSessionUrl         = rtsp;
    srv.serverCodecModeSupport = codec;

    STREAM_CONFIGURATION cfg;
    LiInitializeStreamConfiguration(&cfg);
    cfg.width                  = w;
    cfg.height                 = h;
    cfg.fps                    = fps;
    cfg.bitrate                = bit;
    cfg.packetSize             = 1200;
    cfg.streamingRemotely      = STREAM_CFG_AUTO;
    cfg.audioConfiguration     = AUDIO_CONFIGURATION_STEREO;
    cfg.supportedVideoFormats  = videoFmt;
    cfg.clientRefreshRateX100  = fps * 100;
    // Opt into audio encryption exactly like the official Moonlight clients do
    // (moonlight-android/moonlight-qt default to ENCFLG_AUDIO). Hosts that
    // require encrypted audio turn it on regardless of this flag, so leaving it
    // at ENCFLG_NONE only ever led to a negotiation mismatch, never to plaintext
    // audio we could actually play.
    cfg.encryptionFlags        = ENCFLG_AUDIO;
    if (key) {
        memcpy(cfg.remoteInputAesKey, key, 16);
        // remoteInputAesIv holds the rikeyid in BIG-endian (network) byte
        // order — that is what we sent as "rikeyid" in /launch and what
        // AudioStream.c reads back via BE32() to build the per-packet audio
        // AES-CBC IV. Writing it little-endian made the client's IV differ from
        // the host's, and because CBC only feeds the IV into the *first* block,
        // every audio packet decrypted with a corrupted first 16 bytes (the
        // Opus TOC byte) and an otherwise intact tail: opus_multistream_decode
        // still returned "success" but played garbled/alien noise. Video was
        // unaffected (unencrypted) and remote input was unaffected too (the
        // Sunshine control stream derives its own GCM IV from the sequence
        // number), which is why only audio was broken.
        cfg.remoteInputAesIv[0] = (char)((kid >> 24) & 0xff);
        cfg.remoteInputAesIv[1] = (char)((kid >> 16) & 0xff);
        cfg.remoteInputAesIv[2] = (char)((kid >>  8) & 0xff);
        cfg.remoteInputAesIv[3] = (char)( kid        & 0xff);
    }

    DECODER_RENDERER_CALLBACKS dr;
    LiInitializeVideoCallbacks(&dr);
    dr.setup            = dr_setup;
    dr.submitDecodeUnit = dr_submit;
    dr.cleanup          = dr_cleanup;
    // See moonlight_cgo_shared.h's identical assignment for why the RFI
    // bits are added here too -- both sides (host DESCRIBE flag + this
    // capability bit) are required before moonlight-common-c actually uses
    // reference-frame-invalidation recovery instead of a full IDR request.
    dr.capabilities     = CAPABILITY_DIRECT_SUBMIT | CAPABILITY_REFERENCE_FRAME_INVALIDATION_AVC | CAPABILITY_REFERENCE_FRAME_INVALIDATION_HEVC | CAPABILITY_REFERENCE_FRAME_INVALIDATION_AV1;

    AUDIO_RENDERER_CALLBACKS ar;
    LiInitializeAudioCallbacks(&ar);
    ar.init                = ar_init;
    ar.start               = ar_start;
    ar.stop                = ar_stop;
    ar.cleanup             = ar_cleanup;
    ar.decodeAndPlaySample = ar_decode;

    CONNECTION_LISTENER_CALLBACKS cl;
    LiInitializeConnectionCallbacks(&cl);
    cl.stageStarting       = cl_stage_starting;
    cl.stageComplete       = cl_stage_complete;
    cl.stageFailed         = cl_stage_failed;
    cl.connectionStarted   = cl_connected;
    cl.connectionTerminated = cl_terminated;
    cl.logMessage          = cl_log;

    ALOGI("do_li_start: calling LiStartConnection");
    int ret = LiStartConnection(&srv, &cfg, &cl, &dr, &ar, NULL, 0, NULL, 0);
    ALOGI("do_li_start: LiStartConnection returned %d", ret);
    if (ret != 0) return ret;
    atomic_store(&g_li_active, 1);
    ALOGI("do_li_start: g_li_active set to 1");
    return 0;
}

void do_li_stop(void) {
    int was_active = atomic_load(&g_li_active);
    ALOGI("do_li_stop (active=%d)", was_active);
    if (!was_active) return;
    atomic_store(&g_li_active, 0);
    ALOGI("do_li_stop: calling LiStopConnection");
    LiStopConnection();
    ALOGI("do_li_stop: LiStopConnection returned");
}

// do_li_force_stop unconditionally calls LiStopConnection regardless of
// g_li_active.  Go code that reliably tracks connection state calls this
// instead of do_li_stop to avoid the g_li_active-is-mysteriously-zero bug.
//
// Limelight.h documents LiStartConnection()/LiStopConnection() as NOT
// thread-safe against each other -- only LiInterruptConnection() is safe to
// call while a LiStartConnection() is in flight on another thread ("this
// interruption happens asynchronously so it is not safe to start another
// connection before the first LiStartConnection() call returns"). The Go
// side must call do_li_interrupt() immediately (safe, unsynchronized) and
// then wait for any in-flight do_li_start() to actually return (by taking
// the same liStartCallMu it runs under) before calling this function --
// calling LiStopConnection() while LiStartConnection() is still running
// races on moonlight-common-c's process-global `stage`/`initialized`
// statics (Connection.c, InputStream.c), which can leave `initialized`
// clobbered to false for a session that's actually still current: every
// LiSendMousePositionEvent/LiSendKeyboardEvent2/etc. call after that
// returns -2 and is silently dropped (callers here never checked the
// return value), producing exactly "video/audio fine, no input reaches the
// host, zero errors anywhere" -- confirmed against a live gamestream-server
// with a raw ENet client: an out-of-band test packet decoded and injected
// correctly every time, proving the server side was never the problem.
void do_li_force_stop(void) {
    int prev = atomic_exchange(&g_li_active, 0);
    ALOGI("do_li_force_stop: was active=%d, calling LiStopConnection", prev);
    LiStopConnection();
    ALOGI("do_li_force_stop: LiStopConnection returned");
}

// See do_li_force_stop's docs -- call this immediately (before waiting for
// any lock) to make an in-flight do_li_start() abort promptly, then wait
// for that call to actually return before calling do_li_force_stop().
void do_li_interrupt(void) {
    ALOGI("do_li_interrupt: calling LiInterruptConnection");
    LiInterruptConnection();
}

void set_audio_muted(int muted) { g_audio_muted = muted; }

// force_av_shutdown unconditionally stops and releases the AAudio stream and
// AMediaCodec decoder, independent of LiStopConnection ever actually calling
// our registered ar.cleanup/dr.cleanup callbacks.
//
// Those callbacks only fire if moonlight-common-c's own internal `stage`
// (Connection.c, a process-global with no generation awareness -- see
// do_li_force_stop's docs for the sibling bug this same root cause already
// produced on the *input* side) is not already STAGE_NONE by the time
// LiStopConnection() runs. Observed live: hitting disconnect (or the host
// stopping the stream) sometimes left audio/video still actively playing
// afterward, exactly as if the stop had silently no-op'd -- with the Go
// side's own liConnected/generation bookkeeping showing everything torn down
// correctly, meaning the mismatch was inside the library's own state, not
// ours. Calling this directly, unconditionally, right alongside every
// do_li_force_stop() call, means the actual hardware output is silenced the
// instant the client decides to stop, whatever moonlight-common-c's internal
// stage happens to be.
void force_av_shutdown(void) {
    ALOGI("force_av_shutdown: unconditionally releasing AAudio + AMediaCodec");
    if (g_aa_stream) {
        AAudioStream_requestStop(g_aa_stream);
        AAudioStream_close(g_aa_stream);
        g_aa_stream = NULL;
    }
    if (g_amc) {
        AMediaCodec_stop(g_amc);
        AMediaCodec_delete(g_amc);
        g_amc = NULL;
    }
    android_gl_release();
}

void set_audio_pipe_fd(int fd)  { (void)fd; }

// ── Input ─────────────────────────────────────────────────────────────────────

void do_send_key(short code, char act, char mod) { LiSendKeyboardEvent(code, act, mod); }
void do_send_utf8_text(const char *text, unsigned int len) { LiSendUtf8TextEvent(text, len); }
void do_send_mouse_move(short dx, short dy) {
    int ret = LiSendMouseMoveEvent(dx, dy);
    if (ret != 0) ALOGE("LiSendMouseMoveEvent FAILED ret=%d dx=%d dy=%d", ret, (int)dx, (int)dy);
}
void do_send_mouse_position(short x, short y, short w, short h) {
    int ret = LiSendMousePositionEvent(x, y, w, h);
    if (ret != 0) ALOGE("LiSendMousePositionEvent FAILED ret=%d x=%d y=%d w=%d h=%d", ret, (int)x, (int)y, (int)w, (int)h);
}
void do_send_mouse_button(char act, int btn) {
    int ret = LiSendMouseButtonEvent(act, btn);
    if (ret != 0) ALOGE("LiSendMouseButtonEvent FAILED ret=%d act=%d btn=%d", ret, (int)act, btn);
}
void do_send_scroll(signed char c) { LiSendScrollEvent(c); }
void do_send_multi_controller(unsigned short cn, unsigned short am, unsigned short b, unsigned char lt, unsigned char rt, short lx, short ly, short rx, short ry) {
    LiSendMultiControllerEvent(cn, am, b, lt, rt, lx, ly, rx, ry);
}
*/
import "C"

import (
	"fmt"
	"image"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"fyne.io/fyne/v2/driver"
	"github.com/sirupsen/logrus"
)

var (
	jniInitOnce             sync.Once
	liStartConnectionActive atomic.Bool
	activeStreamDone        chan struct{}
	activeStreamOnce        sync.Once
	activeStreamTermErr     error
	vtFrameCallback         func(image.Image)
	vtFrameCallbackMu       sync.Mutex

	// liStreamMu serializes LiStopConnection / LiStartConnection so they never
	// run concurrently. liStreamGen is a generation counter that lets the goroutine
	// detect whether it is still the "current" stream before touching shared state.
	liStreamMu  sync.Mutex
	liStreamGen atomic.Uint64

	// liStartCallMu serializes entry into C.do_li_start (LiStartConnection).
	// moonlight-common-c keeps its RTSP encryption/decryption crypto contexts
	// (RtspConnection.c: `static PPLT_CRYPTO_CONTEXT encryptionCtx/decryptionCtx`)
	// as process-global statics with no internal locking — the library assumes
	// exactly one LiStartConnection call is in flight at a time.
	//
	// liConnected only flips true once do_li_start() *returns* successfully, so
	// it cannot detect "a previous do_li_start() call is still mid-handshake".
	// A rapid reconnect (StartStream called again before the first call reaches
	// liConnected.Store(true)) used to slip past the liConnected guard and run
	// a second do_li_start() concurrently with the first — both racing on the
	// same static crypto contexts. Confirmed by device testing
	// (tests/test_android_ui_stress.sh, aggressive connect/disconnect hammering):
	// SIGSEGV null-deref in PltDestroyCryptoContext called from
	// performRtspHandshake, plus a handful of JNI CallVoidMethod SIGABRTs from
	// the resulting doubled-up native render/JNI threads.
	//
	// liStartCallMu wraps do_li_start() (LiStartConnection). do_li_force_stop()
	// (LiStopConnection) must ALSO go through this same mutex now -- see
	// stopConnectionSafely()'s docs. The previous design let do_li_force_stop()
	// run concurrently with an in-flight do_li_start() on the theory that
	// LiStopConnection() would "interrupt" it; Limelight.h actually documents
	// LiStartConnection()/LiStopConnection() as NOT thread-safe against each
	// other (only LiInterruptConnection() is safe to call concurrently), and
	// that mismatch was a real bug: Connection.c's `stage`/InputStream.c's
	// `initialized` are process-global statics with no generation awareness,
	// so a stale session's concurrent LiStopConnection() could clobber
	// `initialized` back to false for a session that had just legitimately
	// started, silently breaking every mouse/keyboard send from that point on
	// (confirmed live: server-side ENet decode/inject proven correct with a
	// raw test packet, yet real client input never arrived after a reconnect).
	liStartCallMu sync.Mutex

	// liConnected tracks whether LiStartConnection succeeded and LiStopConnection
	// has NOT yet been called.  Unlike C's g_li_active (which is mysteriously
	// cleared before StopStream reaches it), this is a reliable Go-level atomic
	// that is set by the goroutine and cleared atomically by whoever calls
	// LiStopConnection first (goroutine or StopStream).
	liConnected atomic.Bool
)

type MoonlightCgoWrapper struct {
	host       string
	audioMuted bool
}

func NewMoonlightCgoWrapper(host string) *MoonlightCgoWrapper {
	jniInitOnce.Do(func() {
		_ = driver.RunNative(func(ctx any) error {
			if ac, ok := ctx.(*driver.AndroidContext); ok && ac.VM != 0 && ac.Ctx != 0 {
				C.android_gl_set_jvm((*C.JavaVM)(unsafe.Pointer(ac.VM)), (C.jobject)(unsafe.Pointer(ac.Ctx)))
				C.android_vk_set_jvm((*C.JavaVM)(unsafe.Pointer(ac.VM)), (C.jobject)(unsafe.Pointer(ac.Ctx)))
				logrus.Info("🎬 [Moonlight/Android] JNI Initialized (VER: V4_FINAL_TRACE)")
			}
			return nil
		})
	})
	return &MoonlightCgoWrapper{host: host}
}

// stopConnectionSafely calls LiInterruptConnection() immediately (documented
// safe to call while a LiStartConnection() is in flight on another
// goroutine), then waits for liStartCallMu -- i.e. for that in-flight
// do_li_start() to actually return -- before calling LiStopConnection()
// itself. Every do_li_force_stop() call site must go through this instead
// of calling it directly; see liStartCallMu's doc comment for why calling
// LiStopConnection() concurrently with LiStartConnection() corrupts
// moonlight-common-c's shared `stage`/`initialized` statics.
func stopConnectionSafely() {
	C.do_li_interrupt()
	liStartCallMu.Lock()
	defer liStartCallMu.Unlock()
	C.do_li_force_stop()
	// Belt-and-suspenders: do_li_force_stop only guarantees our own
	// ar.cleanup/dr.cleanup callbacks fire if moonlight-common-c's internal
	// `stage` isn't already STAGE_NONE by the time LiStopConnection() runs
	// (see force_av_shutdown's doc comment) -- calling this directly here
	// means audio/video hardware output actually stops the instant anything
	// calls stopConnectionSafely, regardless of that internal state.
	C.force_av_shutdown()
}

func (w *MoonlightCgoWrapper) StartStream(url string, key []byte, appV, gfeV string, codec, videoFormat, width, height, fps, bit int, pw, apw *os.File, onStop func(error)) error {
	// Acquire the mutex and cleanly stop any in-progress connection before
	// resetting state.  We use liConnected (Go-level atomic) as the reliable
	// signal because C's g_li_active may be zero by the time we reach here
	// even though the connection is technically still active.
	liStreamMu.Lock()
	if liConnected.Swap(false) {
		logrus.Info("🌕 [Moonlight/Android] StartStream: stopping previous connection")
		stopConnectionSafely()
	}
	myGen := liStreamGen.Add(1)
	activeStreamDone = make(chan struct{})
	activeStreamOnce = sync.Once{}
	activeStreamTermErr = nil // clear stale terminated-during-startup callbacks
	liStreamMu.Unlock()

	cHost := C.CString(w.host)
	cAppV := C.CString(appV)
	cGfeV := C.CString(gfeV)
	cRtsp := C.CString(url)
	var cKey *C.uchar
	if len(key) == 16 {
		cKey = (*C.uchar)(C.CBytes(key))
	}

	go func() {
		defer C.free(unsafe.Pointer(cHost))
		defer C.free(unsafe.Pointer(cAppV))
		defer C.free(unsafe.Pointer(cGfeV))
		defer C.free(unsafe.Pointer(cRtsp))
		if cKey != nil {
			defer C.free(unsafe.Pointer(cKey))
		}
		if pw != nil {
			defer pw.Close()
		}
		if apw != nil {
			defer apw.Close()
		}

		logrus.Infof("🌕 [Moonlight/CGO/Android] CALLING_C_LI_START: host=%s rtsp=%s codec=%d videoFmt=%d %dx%d@%d",
			w.host, url, codec, videoFormat, width, height, fps)

		// See liStartCallMu's doc comment: only one do_li_start (LiStartConnection)
		// call may run at a time — moonlight-common-c's crypto contexts are
		// unsynchronized process-global statics. Lock wait time is logged because
		// a nonzero wait here means a previous connect attempt was still tearing
		// down when this one arrived (expected under rapid reconnects, not a bug
		// by itself — the bug was letting both calls run at once).
		lockWaitStart := time.Now()
		liStartCallMu.Lock()
		if waited := time.Since(lockWaitStart); waited > 5*time.Millisecond {
			logrus.Infof("🌕 [Moonlight/CGO/Android] do_li_start waited %v for previous call to release liStartCallMu", waited)
		}
		callStart := time.Now()
		ret := C.do_li_start(cHost, cAppV, cGfeV, cRtsp,
			C.int(codec), C.int(videoFormat), C.int(width), C.int(height), C.int(fps), C.int(bit),
			cKey, C.int(1), C.int(-1))
		callDur := time.Since(callStart)
		liStartCallMu.Unlock()
		logrus.Infof("🌕 [Moonlight/CGO/Android] do_li_start returned ret=%d in %v", int(ret), callDur)

		if int(ret) != 0 {
			logrus.Errorf("🌕 [Moonlight/CGO/Android] LI_START_ERROR_CODE: %d", int(ret))
			if onStop != nil && liStreamGen.Load() == myGen {
				onStop(fmt.Errorf("LiStartConnection error %d", int(ret)))
			}
			return
		}

		// Mark that LiStartConnection succeeded — from this point on whoever
		// atomically swaps liConnected from true→false owns calling
		// do_li_force_stop (i.e. LiStopConnection).
		liConnected.Store(true)
		logrus.Info("🌕 [Moonlight/CGO/Android] ✅ streams active")
		// Unconditionally true, unlike the liStreamGen-gated reset below: this
		// goroutine's own do_li_start really did just succeed, so marking
		// input active is always correct even if a newer generation started
		// concurrently (harmless idempotent write -- the newer generation's
		// own do_li_start sets it true again once it succeeds). Gating this
		// the same way as the reset caused a real bug: under the reconnect
		// races this app can hit (rapid StartStream calls, a process restart
		// mid-handshake), this branch could run for a stale generation and
		// skip the store, leaving IsInputActive() stuck false even though
		// video/audio kept streaming fine -- every mouse/keyboard send (all
		// gated on this flag) was then silently dropped with no visible
		// error anywhere.
		liStartConnectionActive.Store(true)
		<-activeStreamDone

		logrus.Infof("🌕 [Moonlight/Android] goroutine woke from activeStreamDone — gen=%d current=%d connected=%v",
			myGen, liStreamGen.Load(), liConnected.Load())

		// Whoever wins the liConnected.Swap(false) race calls LiStopConnection.
		// This is reliable regardless of g_li_active state.
		liStreamMu.Lock()
		if liConnected.Swap(false) {
			logrus.Info("🌕 [Moonlight/Android] goroutine calling do_li_force_stop")
			stopConnectionSafely()
		} else {
			logrus.Info("🌕 [Moonlight/Android] goroutine: StopStream already called LiStopConnection")
		}
		liStreamMu.Unlock()

		// Only clear shared state if we are still the current generation.
		if liStreamGen.Load() == myGen {
			vtFrameCallbackMu.Lock()
			vtFrameCallback = nil
			vtFrameCallbackMu.Unlock()
			liStartConnectionActive.Store(false)
			if onStop != nil {
				onStop(activeStreamTermErr)
			}
		}
	}()
	return nil
}

func (w *MoonlightCgoWrapper) StopStream() {
	liStreamMu.Lock()
	// Always call stopConnectionSafely to trigger LiStopConnection, which will
	// interrupt LiStartConnection if it is blocking/looping inside C code.
	logrus.Info("🌕 [Moonlight/Android] StopStream: calling do_li_force_stop")
	stopConnectionSafely()

	// Reset connected flag so next start can proceed cleanly.
	liConnected.Store(false)
	liStreamMu.Unlock()

	// Unconditional, not gated on liStreamGen == myGen like the streaming
	// goroutine's own cleanup below -- an explicit StopStream() call is
	// authoritative regardless of which generation is "current": the user
	// (or the app) asked for playback to stop, so the callback that would
	// otherwise keep painting whatever frames are still in flight must be
	// cleared right now, not only once that goroutine's generation check
	// happens to agree.
	vtFrameCallbackMu.Lock()
	vtFrameCallback = nil
	vtFrameCallbackMu.Unlock()

	if activeStreamDone != nil {
		activeStreamOnce.Do(func() { close(activeStreamDone) })
	}
}

func (w *MoonlightCgoWrapper) SetAudioMuted(m bool) {
	w.audioMuted = m
	if m {
		C.set_audio_muted(1)
	} else {
		C.set_audio_muted(0)
	}
}
func (w *MoonlightCgoWrapper) GetAudioMuted() bool { return w.audioMuted }
func (w *MoonlightCgoWrapper) IsInputActive() bool { return liStartConnectionActive.Load() }

// NegotiatedVideoCodecName is not yet wired on Android: dr_setup here uses
// its fmt parameter locally to pick the AMediaCodec MIME type but never
// reports it back to Go (unlike the shared macOS/Linux dr_setup in
// moonlight_cgo_shared.h). Follow-up.
func (w *MoonlightCgoWrapper) NegotiatedVideoCodecName() (string, bool) { return "", false }

func (w *MoonlightCgoWrapper) SendMoonlightKey(c int16, a, m int8) {
	if liStartConnectionActive.Load() {
		C.do_send_key(C.short(c), C.char(a), C.char(m))
	}
}
func (w *MoonlightCgoWrapper) SendMoonlightMouseMove(dx, dy int16) {
	if liStartConnectionActive.Load() {
		C.do_send_mouse_move(C.short(dx), C.short(dy))
	}
}
func (w *MoonlightCgoWrapper) SendMoonlightMousePosition(x, y, rw, rh int16) {
	if liStartConnectionActive.Load() {
		C.do_send_mouse_position(C.short(x), C.short(y), C.short(rw), C.short(rh))
	}
}
func (w *MoonlightCgoWrapper) SendMoonlightMouseButton(a int8, b int) {
	if liStartConnectionActive.Load() {
		C.do_send_mouse_button(C.char(a), C.int(b))
	}
}
func (w *MoonlightCgoWrapper) SendMoonlightScroll(c int8) {
	if liStartConnectionActive.Load() {
		C.do_send_scroll(C.schar(c))
	}
}
func (w *MoonlightCgoWrapper) SendMoonlightControllerEvent(cn uint16, am uint16, b uint16, lt uint8, rt uint8, lx int16, ly int16, rx int16, ry int16) {
	if liStartConnectionActive.Load() {
		C.do_send_multi_controller(C.ushort(cn), C.ushort(am), C.ushort(b), C.uchar(lt), C.uchar(rt), C.short(lx), C.short(ly), C.short(rx), C.short(ry))
	}
}

func (w *MoonlightCgoWrapper) SendMoonlightUtf8Text(text string) {
	if !liStartConnectionActive.Load() || len(text) == 0 {
		return
	}
	cs := C.CString(text)
	defer C.free(unsafe.Pointer(cs))
	C.do_send_utf8_text(cs, C.uint(len(text)))
}

//export goMoonlightStage
func goMoonlightStage(s, r, e C.int) {
	stages := []string{"none", "platform-init", "name-resolution", "audio-stream-init", "rtsp-handshake", "control-stream-init", "video-stream-init", "input-stream-init", "control-stream-start", "video-stream-start", "audio-stream-start", "input-stream-start"}
	name := "unknown"
	if int(s) < len(stages) {
		name = stages[s]
	}
	if r == 0 {
		logrus.Infof("🌕 [Moonlight] ► %s …", name)
	} else if r == 1 {
		logrus.Infof("🌕 [Moonlight] ✅ %s", name)
	} else {
		logrus.Errorf("🌕 [Moonlight] ❌ %s failed (%d)", name, int(e))
	}
}

//export goMoonlightConnected
func goMoonlightConnected() { logrus.Info("🌕 [Moonlight] connected ✅") }

//export goMoonlightTerminated
func goMoonlightTerminated(e C.int) {
	logrus.Errorf("🌕 [Moonlight/Android] ❌ terminated: code=%d active=%v", int(e), liStartConnectionActive.Load())
	activeStreamTermErr = fmt.Errorf("terminated %d", int(e))
	if liStartConnectionActive.Load() {
		activeStreamOnce.Do(func() { close(activeStreamDone) })
	}
}

//export goVTLog
func goVTLog(m *C.char) { logrus.Infof("🎬 [Moonlight/CGO] %s", C.GoString(m)) }

// vtFramePool reuses image.RGBA buffers to avoid per-frame allocation.
var vtFramePool sync.Pool

//export goVTFrame
func goVTFrame(rgba *C.uint8_t, w, h, s C.int) {
	vtFrameCallbackMu.Lock()
	cb := vtFrameCallback
	vtFrameCallbackMu.Unlock()
	if cb == nil {
		return
	}
	// A nil rgba means the C caller (dr_submit in this file) already decided
	// the frame was presented via the zero-copy AHardwareBuffer/Vulkan path
	// and passed NULL on purpose — just notify Go to increment frame counters.
	//
	// This used to re-derive that same decision here by calling
	// android_vk_is_active() again instead of trusting the pointer. That is
	// a TOCTOU race: dr_submit's own android_vk_is_active() check and this
	// one read that same flag at two different times with no synchronization
	// between them. During disconnect, the Vulkan renderer can be torn down
	// (setting the flag to inactive) in the gap between dr_submit deciding
	// "zero-copy, pass NULL" and this function re-checking the flag — which
	// then read "not active" and fell through to unsafe.Slice(nil, n) with
	// n > 0, crashing the process (SIGABRT) on every disconnect that raced
	// with an in-flight frame. Trusting rgba's nil-ness directly removes the
	// race: it's the same value the caller committed to in this one call.
	if rgba == nil {
		cb(nil)
		return
	}
	n := int(w) * int(h) * 4
	// Reuse or allocate image buffer.
	img, _ := vtFramePool.Get().(*image.RGBA)
	if img == nil || len(img.Pix) != n {
		img = image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
	} else {
		img.Rect = image.Rect(0, 0, int(w), int(h))
		img.Stride = int(w) * 4
	}
	// C buffer orientation is already correct (Y-flipped in kVerts, then glReadPixels un-flips).
	// One copy via unsafe.Slice avoids C.GoBytes intermediate allocation.
	src := unsafe.Slice((*byte)(unsafe.Pointer(rgba)), n)
	copy(img.Pix[:n], src)
	cb(img)
	vtFramePool.Put(img)
}
func SetVTFrameCallback(cb func(image.Image)) {
	vtFrameCallbackMu.Lock()
	vtFrameCallback = cb
	vtFrameCallbackMu.Unlock()
}
