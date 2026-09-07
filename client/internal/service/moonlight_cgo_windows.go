//go:build windows && cgo

package service

/*
#cgo pkg-config: opus openssl
#cgo CFLAGS: -I${SRCDIR}/../../moonlight-common-c/src -I${SRCDIR}/../../moonlight-common-c/enet/include
#cgo LDFLAGS: -L${SRCDIR}/../../moonlight-common-c/build -L${SRCDIR}/../../moonlight-common-c/build/enet -lmoonlight-common-c -lenet -lws2_32 -lwinmm
#cgo LDFLAGS: -lavcodec -lavutil -lswscale
#cgo LDFLAGS: -lole32 -loleaut32 -luuid -lmfplat -lmfuuid

#define COBJMACROS
#define INITGUID
#include <stdarg.h>
#include <windows.h>
#include <mfapi.h>
#include <mmdeviceapi.h>
#include <audioclient.h>
#include <libavcodec/avcodec.h>
#include <libavutil/hwcontext.h>
#include <libavutil/hwcontext_d3d11va.h>
#include <libavutil/frame.h>
#include <libavutil/imgutils.h>
#include <libswscale/swscale.h>
#include <Limelight.h>
#include <opus_multistream.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

extern void goMoonlightStage(int stage, int result, int errCode);
extern void goMoonlightConnected(void);
extern void goMoonlightTerminated(int errCode);
extern void goVTLog(char *msg);
extern void goVTFrame(uint8_t *rgba, int width, int height, int stride);
extern void goVideoFormatNegotiated(int videoFormat);
extern void goAIVisionOverlay(uint8_t *rgba, int width, int height, int stride);

// Native overlay fast paths.
// Vulkan (vk_video_impl_windows.c) — preferred, RGBA format.
extern int vk_video_is_active(void);
extern int vk_video_try_submit(uint8_t *rgba, int width, int height, int stride);
// GDI fallback (gl_video_impl_windows.c) — BGRA format.
extern int gl_video_is_active(void);
extern int gl_video_try_submit(uint8_t *bgra, int width, int height, int stride);

// ── Shared state ──────────────────────────────────────────────────────────────

static volatile int    g_li_active          = 0;
static volatile int    g_audio_muted        = 0;
static OpusMSDecoder  *g_opus_ms_decoder    = NULL;
static int             g_audio_channels     = 2;

static void set_audio_pipe_fd(int fd) { (void)fd; }
static void set_audio_muted(int muted) { g_audio_muted = muted; }

// ── Connection callbacks ──────────────────────────────────────────────────────

static void cl_stage_starting(int s)      { goMoonlightStage(s,  0, 0); }
static void cl_stage_complete(int s)       { goMoonlightStage(s,  1, 0); }
static void cl_stage_failed(int s, int ec) { goMoonlightStage(s, -1, ec); }
static void cl_connected(void)             { goMoonlightConnected(); }
static void cl_terminated(int ec)          { goMoonlightTerminated(ec); }
static void cl_log(const char *fmt, ...) {
    char buf[256];
    va_list ap; va_start(ap, fmt); vsnprintf(buf, sizeof(buf), fmt, ap); va_end(ap);
    int n = (int)strlen(buf); if (n > 0 && buf[n-1] == '\n') buf[n-1] = '\0';
    if (buf[0]) goVTLog(buf);
}

// ═══════════════════════════════════════════════════════════════════════════════
// WASAPI audio output
// ═══════════════════════════════════════════════════════════════════════════════

static IAudioClient       *g_wa_client     = NULL;
static IAudioRenderClient *g_wa_render     = NULL;
static UINT32              g_wa_frames     = 0;
static CRITICAL_SECTION    g_wa_cs;
static int                 g_wa_cs_init    = 0;
static int                 g_wa_rate       = 48000;
static int                 g_wa_fail_count = 0;
static ULONGLONG           g_wa_last_write_ms = 0;

static void wasapi_init(int channels, int sample_rate) {
    g_wa_rate = sample_rate;
    if (!g_wa_cs_init) { InitializeCriticalSection(&g_wa_cs); g_wa_cs_init = 1; }
    if (g_wa_client) return;

    CoInitializeEx(NULL, COINIT_MULTITHREADED);

    IMMDeviceEnumerator *pEnum = NULL;
    if (FAILED(CoCreateInstance(&CLSID_MMDeviceEnumerator, NULL,
                                CLSCTX_ALL, &IID_IMMDeviceEnumerator,
                                (void**)&pEnum))) {
        goVTLog((char*)"WASAPI: CoCreateInstance MMDeviceEnumerator FAILED");
        return;
    }
    IMMDevice *pDev = NULL;
    if (FAILED(IMMDeviceEnumerator_GetDefaultAudioEndpoint(pEnum, eRender, eConsole, &pDev))) {
        IMMDeviceEnumerator_Release(pEnum);
        goVTLog((char*)"WASAPI: GetDefaultAudioEndpoint FAILED");
        return;
    }
    IMMDeviceEnumerator_Release(pEnum);

    IAudioClient *pClient = NULL;
    if (FAILED(IMMDevice_Activate(pDev, &IID_IAudioClient, CLSCTX_ALL,
                                   NULL, (void**)&pClient))) {
        IMMDevice_Release(pDev);
        goVTLog((char*)"WASAPI: IMMDevice::Activate FAILED");
        return;
    }
    IMMDevice_Release(pDev);

    WAVEFORMATEX wfx = {
        .wFormatTag      = WAVE_FORMAT_PCM,
        .nChannels       = (WORD)channels,
        .nSamplesPerSec  = (DWORD)sample_rate,
        .wBitsPerSample  = 16,
        .nBlockAlign     = (WORD)(channels * 2),
        .nAvgBytesPerSec = (DWORD)(sample_rate * channels * 2),
        .cbSize          = 0,
    };
    // Shared mode, event-driven — 40 ms buffer.
    HRESULT hr = IAudioClient_Initialize(pClient,
        AUDCLNT_SHAREMODE_SHARED,
        0,
        400000, // 40 ms in 100-ns units
        0, &wfx, NULL);
    if (FAILED(hr)) {
        IAudioClient_Release(pClient);
        goVTLog((char*)"WASAPI: IAudioClient::Initialize FAILED");
        return;
    }

    IAudioRenderClient *pRender = NULL;
    if (FAILED(IAudioClient_GetService(pClient, &IID_IAudioRenderClient, (void**)&pRender))) {
        IAudioClient_Release(pClient);
        goVTLog((char*)"WASAPI: GetService IAudioRenderClient FAILED");
        return;
    }
    IAudioClient_GetBufferSize(pClient, &g_wa_frames);
    IAudioClient_Start(pClient);

    EnterCriticalSection(&g_wa_cs);
    g_wa_client = pClient;
    g_wa_render = pRender;
    LeaveCriticalSection(&g_wa_cs);
    g_wa_fail_count    = 0;
    g_wa_last_write_ms = 0;
    goVTLog((char*)"WASAPI: audio client started (S16LE native output)");
}

static void wasapi_teardown(void) {
    if (!g_wa_cs_init) return;
    EnterCriticalSection(&g_wa_cs);
    IAudioClient       *c = g_wa_client; g_wa_client = NULL;
    IAudioRenderClient *r = g_wa_render; g_wa_render = NULL;
    LeaveCriticalSection(&g_wa_cs);
    if (c) { IAudioClient_Stop(c); IAudioClient_Release(c); }
    if (r) { IAudioRenderClient_Release(r); }
    goVTLog((char*)"WASAPI: audio client stopped");
}

// Repeated WASAPI failures (bad HRESULTs from a client that's fallen into an
// unrecoverable state) never self-heal — recreate the client from scratch so
// audio can resume instead of staying silent for the rest of the session.
static void wasapi_handle_failure(const char *where) {
    if (++g_wa_fail_count < 5) return;
    g_wa_fail_count = 0;
    char msg[128];
    snprintf(msg, sizeof(msg), "WASAPI: %s failing repeatedly, reinitializing audio client", where);
    goVTLog(msg);
    wasapi_teardown();
    wasapi_init(g_audio_channels, g_wa_rate);
}

static void wasapi_write(const opus_int16 *pcm, int samples) {
    EnterCriticalSection(&g_wa_cs);
    IAudioClient       *c = g_wa_client;
    IAudioRenderClient *r = g_wa_render;
    LeaveCriticalSection(&g_wa_cs);
    if (!c || !r) return;

    ULONGLONG now = GetTickCount64();
    // A long gap since the last successful write (network stall drained the
    // buffer) can leave the shared-mode client stuck rendering silence even
    // after new frames arrive; force a fresh clock so playback actually resumes.
    if (g_wa_last_write_ms != 0 && (now - g_wa_last_write_ms) > 250) {
        goVTLog((char*)"WASAPI: resuming after stall, restarting audio client");
        IAudioClient_Stop(c);
        IAudioClient_Reset(c);
        IAudioClient_Start(c);
    }

    UINT32 padding = 0;
    HRESULT hr = IAudioClient_GetCurrentPadding(c, &padding);
    if (FAILED(hr)) { wasapi_handle_failure("GetCurrentPadding"); return; }
    UINT32 avail = g_wa_frames - padding;
    if ((UINT32)samples > avail) return; // buffer full — drop frame, not an error

    BYTE *buf = NULL;
    hr = IAudioRenderClient_GetBuffer(r, (UINT32)samples, &buf);
    if (FAILED(hr) || !buf) { wasapi_handle_failure("GetBuffer"); return; }
    memcpy(buf, pcm, (size_t)samples * (size_t)g_audio_channels * 2);
    hr = IAudioRenderClient_ReleaseBuffer(r, (UINT32)samples, 0);
    if (FAILED(hr)) { wasapi_handle_failure("ReleaseBuffer"); return; }

    g_wa_fail_count    = 0;
    g_wa_last_write_ms = now;
}

// ── Audio callbacks ───────────────────────────────────────────────────────────

static int ar_init(int audioConfig, const POPUS_MULTISTREAM_CONFIGURATION cfg, void *ctx, int flags) {
    (void)audioConfig; (void)ctx; (void)flags;
    g_audio_channels = cfg->channelCount;
    if (g_opus_ms_decoder) { opus_multistream_decoder_destroy(g_opus_ms_decoder); g_opus_ms_decoder = NULL; }
    int error = OPUS_OK;
    g_opus_ms_decoder = opus_multistream_decoder_create(
        cfg->sampleRate, cfg->channelCount,
        cfg->streams, cfg->coupledStreams, cfg->mapping, &error);
    if (error != OPUS_OK) return -1;
    wasapi_init(cfg->channelCount, (int)cfg->sampleRate);
    return 0;
}
static void ar_start(void)   {}
static void ar_stop(void)    {}
static void ar_cleanup(void) {
    wasapi_teardown();
    if (g_opus_ms_decoder) { opus_multistream_decoder_destroy(g_opus_ms_decoder); g_opus_ms_decoder = NULL; }
}
static void ar_decode(char *data, int len) {
    if (!g_opus_ms_decoder) return;
    opus_int16 pcm[5760 * 8];
    int samples = opus_multistream_decode(g_opus_ms_decoder,
        (const unsigned char *)data, len, pcm, 5760, 0);
    if (samples <= 0) return;
    if (g_audio_muted) memset(pcm, 0, samples * g_audio_channels * 2);
    wasapi_write(pcm, samples);
}

// ═══════════════════════════════════════════════════════════════════════════════
// libavcodec H.264 decoder with D3D11VA hardware acceleration
// ═══════════════════════════════════════════════════════════════════════════════

static AVCodecContext    *g_avctx       = NULL;
static struct SwsContext *g_sws         = NULL;
static AVBufferRef       *g_hw_dev_ctx  = NULL;
static enum AVPixelFormat g_hw_pix_fmt  = AV_PIX_FMT_NONE;
static enum AVPixelFormat g_av_dst_fmt  = AV_PIX_FMT_NONE;
static int                g_av_w        = 0;
static int                g_av_h        = 0;
static CRITICAL_SECTION   g_av_cs;
static int                g_av_cs_init  = 0;
static uint64_t           g_av_frame_cnt = 0;

// Codec negotiated by moonlight-common-c for the current session (set in
// dr_setup from its NegotiatedVideoFormat param). VIDEO_FORMAT_* bitmask
// from Limelight.h: 0x0001=H264, 0x0100=H265/HEVC, 0x1000=AV1_MAIN8.
// win_av_init() reads this to pick a matching decoder -- without it, every
// session decoded as H264 regardless of what was actually negotiated, which
// silently breaks HEVC/AV1 sessions (the decoder rejects bitstream it can't
// parse as H264).
static int g_video_format = 0x0001;

static enum AVPixelFormat win_get_hw_format(AVCodecContext *ctx,
                                             const enum AVPixelFormat *fmts) {
    (void)ctx;
    for (const enum AVPixelFormat *p = fmts; *p != AV_PIX_FMT_NONE; p++) {
        if (*p == g_hw_pix_fmt) return *p;
    }
    return AV_PIX_FMT_NONE;
}

static void win_av_init(void) {
    if (!g_av_cs_init) { InitializeCriticalSection(&g_av_cs); g_av_cs_init = 1; }
    if (g_avctx) return;

    // Pick the decoder family to match what was actually negotiated for this
    // session (g_video_format, set by dr_setup) -- previously this always
    // picked H264 unconditionally, so an HEVC/AV1 session fed HEVC/AV1
    // bitstream into an H264 decoder and silently failed to produce frames.
    const char *hw_name;
    enum AVCodecID sw_id;
    const char *codec_label;
    if (g_video_format & 0x0F00) { // VIDEO_FORMAT_MASK_H265
        hw_name = "hevc_d3d11va"; sw_id = AV_CODEC_ID_HEVC; codec_label = "hevc";
    } else if (g_video_format & 0xF000) { // VIDEO_FORMAT_MASK_AV1
        hw_name = "av1_d3d11va"; sw_id = AV_CODEC_ID_AV1; codec_label = "av1";
    } else {
        hw_name = "h264_d3d11va"; sw_id = AV_CODEC_ID_H264; codec_label = "h264";
    }

    const AVCodec *codec = NULL;

    // Try D3D11VA hardware decoder.
    const AVCodec *hw_codec = avcodec_find_decoder_by_name(hw_name);
    if (hw_codec) {
        AVBufferRef *hw_ctx = NULL;
        if (av_hwdevice_ctx_create(&hw_ctx, AV_HWDEVICE_TYPE_D3D11VA, NULL, NULL, 0) == 0) {
            AVCodecContext *test = avcodec_alloc_context3(hw_codec);
            g_hw_pix_fmt = AV_PIX_FMT_D3D11;
            test->hw_device_ctx = av_buffer_ref(hw_ctx);
            test->get_format = win_get_hw_format;
            if (avcodec_open2(test, hw_codec, NULL) == 0) {
                codec = hw_codec;
                if (g_hw_dev_ctx) av_buffer_unref(&g_hw_dev_ctx);
                g_hw_dev_ctx = hw_ctx;
                char msg[96];
                snprintf(msg, sizeof(msg), "libavcodec/win: using %s (hardware)", hw_name);
                goVTLog(msg);
            } else {
                av_buffer_unref(&hw_ctx);
                g_hw_pix_fmt = AV_PIX_FMT_NONE;
            }
            avcodec_free_context(&test);
        }
    }
    if (!codec) {
        codec = avcodec_find_decoder(sw_id);
        g_hw_pix_fmt = AV_PIX_FMT_NONE;
        char msg[96];
        snprintf(msg, sizeof(msg), "libavcodec/win: using %s software fallback", codec_label);
        goVTLog(msg);
    }
    if (!codec) {
        char msg[96];
        snprintf(msg, sizeof(msg), "libavcodec/win: no decoder available for %s", codec_label);
        goVTLog(msg);
        return;
    }

    g_avctx = avcodec_alloc_context3(codec);
    if (g_hw_dev_ctx) {
        g_avctx->hw_device_ctx = av_buffer_ref(g_hw_dev_ctx);
        g_avctx->get_format    = win_get_hw_format;
    }
    if (avcodec_open2(g_avctx, codec, NULL) < 0) {
        avcodec_free_context(&g_avctx);
        goVTLog((char*)"libavcodec/win: avcodec_open2 FAILED");
    }
}

static void win_deliver_frame(AVFrame *frame) {
    AVFrame *sw = NULL;
    if (frame->format == AV_PIX_FMT_D3D11) {
        sw = av_frame_alloc();
        if (av_hwframe_transfer_data(sw, frame, 0) < 0) { av_frame_free(&sw); return; }
        sw->width = frame->width; sw->height = frame->height;
        frame = sw;
    }
    int w = frame->width, h = frame->height;
    // Vulkan and Fyne canvas both want RGBA; only GDI fallback needs BGRA.
    enum AVPixelFormat dst_fmt = (!vk_video_is_active() && gl_video_is_active())
                                 ? AV_PIX_FMT_BGRA : AV_PIX_FMT_RGBA;
    if (!g_sws || w != g_av_w || h != g_av_h || dst_fmt != g_av_dst_fmt) {
        if (g_sws) sws_freeContext(g_sws);
        g_sws = sws_getContext(w, h, (enum AVPixelFormat)frame->format,
                               w, h, dst_fmt, SWS_BILINEAR, NULL, NULL, NULL);
        g_av_w = w; g_av_h = h; g_av_dst_fmt = dst_fmt;
    }
    if (g_sws) {
        uint8_t *pixels = (uint8_t *)malloc((size_t)w * (size_t)h * 4);
        if (pixels) {
            uint8_t *dst[4]   = { pixels, NULL, NULL, NULL };
            int dst_stride[4] = { w * 4, 0, 0, 0 };
            sws_scale(g_sws, (const uint8_t *const *)frame->data, frame->linesize,
                      0, h, dst, dst_stride);
            if (++g_av_frame_cnt == 1) goVTLog((char*)"libavcodec/win: first video frame decoded");
            // AI Vision overlay: no-op unless the checkbox in the video
            // settings popup is on (checked internally, single atomic load
            // in the common case) -- burns detection boxes+ids into pixels
            // in place, before it reaches either native fast path. Mirrors
            // moonlight_cgo_linux.go's ordering. Only valid when pixels is
            // actually RGBA (dst_fmt above) -- skip it on the rare GDI/BGRA
            // fallback path, where ApplyAIVisionOverlay's box colors and
            // downstream PNG-encode-as-RGBA would both come out wrong
            // (R/B channels swapped).
            if (dst_fmt == AV_PIX_FMT_RGBA)
                goAIVisionOverlay(pixels, w, h, w * 4);
            // Submit to native overlay (Vulkan preferred, GDI fallback); no-op if inactive.
            if (!vk_video_try_submit(pixels, w, h, w * 4))
                gl_video_try_submit(pixels, w, h, w * 4);
            goVTFrame(pixels, w, h, w * 4);
            free(pixels);
        }
    }
    if (sw) av_frame_free(&sw);
}

// ── Video callbacks ───────────────────────────────────────────────────────────

static int  dr_setup(int fmt, int w, int h, int rate, void *ctx, int flags) {
    (void)w; (void)h; (void)rate; (void)ctx; (void)flags;
    g_video_format = fmt ? fmt : 0x0001;
    goVideoFormatNegotiated(fmt);
    return 0;
}
static void dr_start(void)   {}
static void dr_stop(void)    {}
static void dr_cleanup(void) {}

static int dr_submit(PDECODE_UNIT du) {
    if (!g_av_cs_init) { InitializeCriticalSection(&g_av_cs); g_av_cs_init = 1; }
    EnterCriticalSection(&g_av_cs);
    if (!g_avctx) win_av_init();
    AVCodecContext *ctx = g_avctx;
    LeaveCriticalSection(&g_av_cs);
    if (!ctx) return DR_NEED_IDR;

    int total = 0;
    for (PLENTRY e = du->bufferList; e; e = e->next) total += e->length;
    if (total <= 0) return DR_OK;

    uint8_t *data = (uint8_t *)av_malloc(total + AV_INPUT_BUFFER_PADDING_SIZE);
    if (!data) return DR_NEED_IDR;
    memset(data + total, 0, AV_INPUT_BUFFER_PADDING_SIZE);
    int off = 0;
    for (PLENTRY e = du->bufferList; e; e = e->next) {
        memcpy(data + off, e->data, e->length); off += e->length;
    }

    AVPacket *pkt = av_packet_alloc();
    pkt->data = data; pkt->size = total;
    int ret = avcodec_send_packet(ctx, pkt);
    av_packet_free(&pkt);
    av_free(data);
    if (ret < 0 && ret != AVERROR(EAGAIN)) return DR_NEED_IDR;

    AVFrame *frame = av_frame_alloc();
    while (avcodec_receive_frame(ctx, frame) == 0) {
        win_deliver_frame(frame);
        av_frame_unref(frame);
    }
    av_frame_free(&frame);
    return DR_OK;
}

// ── LiStartConnection entrypoint ─────────────────────────────────────────────

static int do_li_start(
    const char *address, const char *appVersion, const char *gfeVersion,
    const char *rtspSessionUrl, int serverCodecModeSupport,
    int videoFormat,
    int width, int height, int fps, int bitrate,
    const unsigned char *rikey, int rikeyid, uintptr_t unused
) {
    (void)unused;
    // Do NOT call win_av_init() here: g_video_format at this point is
    // whatever the *previous* session negotiated (there's nothing to reset
    // it in between -- dr_cleanup()/dr_stop() are no-ops), so creating the
    // decoder this early picks the wrong codec family whenever the user
    // switches codec between sessions (e.g. HEVC -> H264). dr_setup() runs
    // during LiStartConnection's stream init, before any decode units are
    // submitted, and updates g_video_format with what was *actually*
    // negotiated for this session; the lazy `if (!g_avctx) win_av_init();`
    // in dr_submit() then creates the decoder against the correct format.

    SERVER_INFORMATION srv; LiInitializeServerInformation(&srv);
    srv.address = address; srv.serverInfoAppVersion = appVersion;
    srv.serverInfoGfeVersion = gfeVersion; srv.rtspSessionUrl = rtspSessionUrl;
    srv.serverCodecModeSupport = serverCodecModeSupport;

    STREAM_CONFIGURATION cfg; LiInitializeStreamConfiguration(&cfg);
    cfg.width = width; cfg.height = height; cfg.fps = fps; cfg.bitrate = bitrate;
    cfg.packetSize = 1200; cfg.streamingRemotely = STREAM_CFG_AUTO;
    cfg.audioConfiguration = AUDIO_CONFIGURATION_STEREO;
    cfg.supportedVideoFormats = videoFormat ? videoFormat : VIDEO_FORMAT_H264;
    // ENCFLG_AUDIO matches the official Moonlight clients' default.
    cfg.clientRefreshRateX100 = fps * 100; cfg.encryptionFlags = ENCFLG_AUDIO;
    if (rikey) {
        memcpy(cfg.remoteInputAesKey, rikey, 16);
        // remoteInputAesIv holds the rikeyid in BIG-endian (network) byte order —
        // that is what we sent as "rikeyid" in /launch and what AudioStream.c
        // reads back via BE32() to build the per-packet audio AES-CBC IV.
        // Writing it little-endian corrupted the first 16 bytes (including the
        // Opus TOC byte) of every decrypted audio packet, producing garbled
        // audio while decode still reported success.
        cfg.remoteInputAesIv[0] = (char)((rikeyid >> 24) & 0xff);
        cfg.remoteInputAesIv[1] = (char)((rikeyid >> 16) & 0xff);
        cfg.remoteInputAesIv[2] = (char)((rikeyid >>  8) & 0xff);
        cfg.remoteInputAesIv[3] = (char)( rikeyid        & 0xff);
    }

    DECODER_RENDERER_CALLBACKS dr; LiInitializeVideoCallbacks(&dr);
    dr.setup = dr_setup; dr.start = dr_start; dr.stop = dr_stop;
    dr.cleanup = dr_cleanup; dr.submitDecodeUnit = dr_submit;
    dr.capabilities = CAPABILITY_DIRECT_SUBMIT;

    AUDIO_RENDERER_CALLBACKS ar; LiInitializeAudioCallbacks(&ar);
    ar.init = ar_init; ar.start = ar_start; ar.stop = ar_stop;
    ar.cleanup = ar_cleanup; ar.decodeAndPlaySample = ar_decode;

    CONNECTION_LISTENER_CALLBACKS cl; LiInitializeConnectionCallbacks(&cl);
    cl.stageStarting = cl_stage_starting; cl.stageComplete = cl_stage_complete;
    cl.stageFailed = cl_stage_failed; cl.connectionStarted = cl_connected;
    cl.connectionTerminated = cl_terminated; cl.logMessage = cl_log;

    int ret = LiStartConnection(&srv, &cfg, &cl, &dr, &ar, NULL, 0, NULL, 0);
    if (ret != 0) return ret;
    g_li_active = 1;
    return 0;
}

static void do_li_stop(void) {
    if (!g_li_active) return;
    g_li_active = 0;
    LiStopConnection();
    if (g_sws) { sws_freeContext(g_sws); g_sws = NULL; }
    if (g_avctx) avcodec_free_context(&g_avctx);
    if (g_hw_dev_ctx) av_buffer_unref(&g_hw_dev_ctx);
}

static void do_li_interrupt(void) {
    LiInterruptConnection();
}

// ── Input forwarders ──────────────────────────────────────────────────────────

static void do_send_key(short vkCode, char action, char modifiers) {
    LiSendKeyboardEvent(vkCode, action, modifiers);
}
static void do_send_mouse_move(short dx, short dy)        { LiSendMouseMoveEvent(dx, dy); }
static void do_send_mouse_position(short x, short y, short refW, short refH) {
    LiSendMousePositionEvent(x, y, refW, refH);
}
static void do_send_mouse_button(char action, int button) { LiSendMouseButtonEvent(action, button); }
static void do_send_scroll(signed char clicks)            { LiSendScrollEvent(clicks); }
static void do_send_multi_controller(
    unsigned short cn, unsigned short am, unsigned short b,
    unsigned char lt, unsigned char rt,
    short lx, short ly, short rx, short ry)
{
    LiSendMultiControllerEvent(cn, am, b, lt, rt, lx, ly, rx, ry);
}
static void do_send_utf8_text(const char *text, unsigned int len) { LiSendUtf8TextEvent(text, len); }
*/
import "C"

import (
	"fmt"
	"image"
	"os"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/sirupsen/logrus"

	"usbridge-client/internal/models"
)

var liStartConnectionActive atomic.Bool

// negotiatedVideoFormat holds the VIDEO_FORMAT_* value moonlight-common-c
// reported via dr_setup(NegotiatedVideoFormat, ...) -- the server's actual
// codec choice for the current session. -1 means "no session has reported a
// negotiated format yet". See moonlight_cgo_wrapper.go's identical pattern
// used on macOS/Linux.
var negotiatedVideoFormat atomic.Int32

func init() {
	negotiatedVideoFormat.Store(-1)
}

func windowsVideoFormatCodecName(format int32) (string, bool) {
	switch {
	case format < 0:
		return "", false
	case format&0x0F00 != 0:
		return models.VideoModeH265, true
	case format&0xF000 != 0:
		return models.VideoModeAV1, true
	case format&0x00FF != 0:
		return models.VideoModeH264, true
	default:
		return "", false
	}
}

var (
	activeStreamDone    chan struct{}
	activeStreamOnce    sync.Once
	activeStreamTermErr error
)

var (
	vtFrameCallback   func(image.Image)
	vtFrameCallbackMu sync.Mutex
)

// liStreamMu serializes LiStopConnection / LiStartConnection so they never
// run concurrently. liStreamGen is a generation counter that lets the goroutine
// detect whether it is still the "current" stream before touching shared state.
var (
	liStreamMu  sync.Mutex
	liStartMu   sync.Mutex // Ensures C.do_li_start is never executed concurrently
	liStreamGen atomic.Uint64
)

func closeActiveStreamDone() {
	activeStreamOnce.Do(func() { close(activeStreamDone) })
}

// stopConnectionSafely tears down the current connection without racing an
// in-flight LiStartConnection() on another goroutine. LiStartConnection()
// and LiStopConnection() are documented (Limelight.h) as NOT safe to call
// concurrently with each other -- only LiInterruptConnection() is safe to
// call at any time. liStreamMu alone does not prevent this race: do_li_start
// runs under the separate liStartMu while holding liStreamMu only briefly
// (or not at all, in the deferred-stop path), so a concurrent do_li_stop()
// under liStreamMu could still overlap a do_li_start() in flight elsewhere.
// Interrupting first unblocks any in-progress LiStartConnection() quickly,
// then waiting on liStartMu guarantees do_li_start has fully returned before
// do_li_stop() touches moonlight-common-c's shared static state.
func stopConnectionSafely() {
	C.do_li_interrupt()
	liStartMu.Lock()
	defer liStartMu.Unlock()
	C.do_li_stop()
}

type MoonlightCgoWrapper struct {
	host       string
	audioMuted bool
}

func NewMoonlightCgoWrapper(host string) *MoonlightCgoWrapper {
	return &MoonlightCgoWrapper{host: host}
}

func (w *MoonlightCgoWrapper) StartStream(
	rtspSessionUrl string,
	rikey []byte,
	appVersion, gfeVersion string,
	serverCodecModeSupport int,
	videoFormat int,
	width, height, fps, bitrate int,
	pipeWrite *os.File,
	audioPipeWrite *os.File,
	onStop func(error),
) error {
	// Hold the stream mutex while stopping any previous connection and resetting
	// state.  This blocks until any in-progress LiStopConnection (from a prior
	// goroutine or from StopStream) has fully returned, preventing concurrent
	// LiStartConnection + LiStopConnection which corrupts moonlight-common-c
	// static state and causes SIGSEGV.
	liStreamMu.Lock()
	stopConnectionSafely()
	myGen := liStreamGen.Add(1)
	activeStreamDone = make(chan struct{})
	activeStreamOnce = sync.Once{}
	activeStreamTermErr = nil
	liStreamMu.Unlock()

	host := C.CString(w.host)
	appVer := C.CString(appVersion)
	gfeVer := C.CString(gfeVersion)
	rtsp := C.CString("rtsp://" + rtspSessionUrl)

	var cRikey *C.uchar
	if len(rikey) == 16 {
		cRikey = (*C.uchar)(C.CBytes(rikey))
	}

	go func() {
		defer C.free(unsafe.Pointer(host))
		defer C.free(unsafe.Pointer(appVer))
		defer C.free(unsafe.Pointer(gfeVer))
		defer C.free(unsafe.Pointer(rtsp))
		if cRikey != nil {
			defer C.free(unsafe.Pointer(cRikey))
		}

		liStartMu.Lock()
		// If another StartStream or StopStream occurred while we waited for the lock, abort this stale attempt.
		if liStreamGen.Load() != myGen {
			liStartMu.Unlock()
			logrus.Info("🌕 [Moonlight/CGO/Win] Aborting stale stream start")
			return
		}

		logrus.Infof("🌕 [Moonlight/CGO/Win] LiStartConnection: host=%s %dx%d@%d bitrate=%d",
			w.host, width, height, fps, bitrate)

		ret := C.do_li_start(
			host, appVer, gfeVer, rtsp,
			C.int(serverCodecModeSupport), C.int(videoFormat),
			C.int(width), C.int(height), C.int(fps), C.int(bitrate),
			cRikey, C.int(1), C.uintptr_t(0),
		)
		liStartMu.Unlock()

		if int(ret) != 0 {
			logrus.Errorf("🌕 [Moonlight/CGO/Win] LiStartConnection FAILED: code=%d", int(ret))
			if pipeWrite != nil {
				_ = pipeWrite.Close()
			}
			if onStop != nil && liStreamGen.Load() == myGen {
				onStop(fmt.Errorf("LiStartConnection error code %d", int(ret)))
			}
			return
		}

		logrus.Info("🌕 [Moonlight/CGO/Win] ✅ streams active")
		// Unconditionally true -- see the Android cgo file's identical fix
		// for why gating this the same way as the generation-checked reset
		// below caused a real bug: under reconnect races this branch could
		// run for a stale generation and skip the store, leaving
		// IsInputActive() stuck false (and every mouse/keyboard send, all
		// gated on it, silently dropped) even though video/audio kept
		// streaming fine. This goroutine's own do_li_start really did just
		// succeed, so the store is always correct and idempotent here.
		liStartConnectionActive.Store(true)

		<-activeStreamDone

		logrus.Info("🌕 [Moonlight/CGO/Win] termination received — stopping")
		// Call LiStopConnection under the mutex so that the next StartStream
		// cannot call LiStartConnection until this stop is fully complete.
		liStreamMu.Lock()
		stopConnectionSafely()
		liStreamMu.Unlock()

		// Only clear shared state if we are still the current generation;
		// a newer StartStream may have already reset these.
		if liStreamGen.Load() == myGen {
			vtFrameCallbackMu.Lock()
			vtFrameCallback = nil
			vtFrameCallbackMu.Unlock()
			liStartConnectionActive.Store(false)
		}
		if pipeWrite != nil {
			_ = pipeWrite.Close()
		}
		if onStop != nil && liStreamGen.Load() == myGen {
			onStop(activeStreamTermErr)
		}
	}()
	return nil
}

func (w *MoonlightCgoWrapper) StopStream() {
	logrus.Info("🌕 [Moonlight/CGO/Win] StopStream: stopping")
	liStreamMu.Lock()
	stopConnectionSafely()
	liStreamMu.Unlock()
	if activeStreamDone != nil {
		closeActiveStreamDone()
	}
}

func (w *MoonlightCgoWrapper) SetAudioMuted(muted bool) {
	w.audioMuted = muted
	if muted {
		C.set_audio_muted(1)
	} else {
		C.set_audio_muted(0)
	}
}
func (w *MoonlightCgoWrapper) GetAudioMuted() bool { return w.audioMuted }

func (w *MoonlightCgoWrapper) SendMoonlightKey(vkCode int16, action int8, modifiers int8) {
	if !liStartConnectionActive.Load() {
		return
	}
	C.do_send_key(C.short(vkCode), C.char(action), C.char(modifiers))
}
func (w *MoonlightCgoWrapper) SendMoonlightMouseMove(dx, dy int16) {
	if !liStartConnectionActive.Load() {
		return
	}
	C.do_send_mouse_move(C.short(dx), C.short(dy))
}
func (w *MoonlightCgoWrapper) SendMoonlightMousePosition(x, y, refW, refH int16) {
	if !liStartConnectionActive.Load() {
		return
	}
	C.do_send_mouse_position(C.short(x), C.short(y), C.short(refW), C.short(refH))
}
func (w *MoonlightCgoWrapper) SendMoonlightMouseButton(action int8, button int) {
	if !liStartConnectionActive.Load() {
		return
	}
	C.do_send_mouse_button(C.char(action), C.int(button))
}
func (w *MoonlightCgoWrapper) SendMoonlightScroll(clicks int8) {
	if !liStartConnectionActive.Load() {
		return
	}
	C.do_send_scroll(C.schar(clicks))
}
func (w *MoonlightCgoWrapper) SendMoonlightControllerEvent(
	controllerNumber uint16, activeGamepadMask uint16, buttons uint16,
	leftTrigger uint8, rightTrigger uint8,
	leftStickX int16, leftStickY int16,
	rightStickX int16, rightStickY int16,
) {
	if !liStartConnectionActive.Load() {
		return
	}
	C.do_send_multi_controller(
		C.ushort(controllerNumber), C.ushort(activeGamepadMask), C.ushort(buttons),
		C.uchar(leftTrigger), C.uchar(rightTrigger),
		C.short(leftStickX), C.short(leftStickY),
		C.short(rightStickX), C.short(rightStickY),
	)
}
func (w *MoonlightCgoWrapper) IsInputActive() bool { return liStartConnectionActive.Load() }

// NegotiatedVideoCodecName returns the codec moonlight-common-c actually
// negotiated with the server for the current session (from dr_setup's
// NegotiatedVideoFormat), matching the macOS/Linux implementation.
func (w *MoonlightCgoWrapper) NegotiatedVideoCodecName() (string, bool) {
	if !liStartConnectionActive.Load() {
		return "", false
	}
	return windowsVideoFormatCodecName(negotiatedVideoFormat.Load())
}

//export goVideoFormatNegotiated
func goVideoFormatNegotiated(format C.int) {
	negotiatedVideoFormat.Store(int32(format))
	name, ok := windowsVideoFormatCodecName(int32(format))
	if !ok {
		logrus.Warnf("🎬 [Moonlight/HW/Win] negotiated video format: unrecognized 0x%04X", int(format))
		return
	}
	logrus.Infof("🎬 [Moonlight/HW/Win] negotiated video format: %s (0x%04X)", name, int(format))
}

func (w *MoonlightCgoWrapper) SendMoonlightUtf8Text(text string) {
	if !liStartConnectionActive.Load() || len(text) == 0 {
		return
	}
	cs := C.CString(text)
	defer C.free(unsafe.Pointer(cs))
	C.do_send_utf8_text(cs, C.uint(len(text)))
}

// ── CGO-exported Go callbacks ─────────────────────────────────────────────────

var stageNames = []string{
	"none", "platform-init", "name-resolution", "audio-stream-init",
	"rtsp-handshake", "control-stream-init", "video-stream-init",
	"input-stream-init", "control-stream-start", "video-stream-start",
	"audio-stream-start", "input-stream-start",
}

//export goMoonlightStage
func goMoonlightStage(stage, result, errCode C.int) {
	name := "unknown"
	if int(stage) < len(stageNames) {
		name = stageNames[stage]
	}
	switch int(result) {
	case 0:
		logrus.Infof("🌕 [Moonlight] ► %s …", name)
	case 1:
		logrus.Infof("🌕 [Moonlight] ✅ %s", name)
	default:
		logrus.Errorf("🌕 [Moonlight] ❌ %s failed (err=%d)", name, int(errCode))
	}
}

//export goMoonlightConnected
func goMoonlightConnected() { logrus.Info("🌕 [Moonlight] stream connected ✅") }

//export goMoonlightTerminated
func goMoonlightTerminated(errCode C.int) {
	reason := "unknown"
	switch int(errCode) {
	case 0:
		reason = "clean disconnect"
	}
	logrus.Errorf("🌕 [Moonlight] ❌ terminated: code=%d (%s)", int(errCode), reason)
	activeStreamTermErr = fmt.Errorf("stream terminated: code=%d (%s)", int(errCode), reason)
	// Clear the negotiated codec so a stale value from this session can't be
	// shown as "currently active" once the stream has actually ended.
	negotiatedVideoFormat.Store(-1)
	closeActiveStreamDone()
}

//export goVTLog
func goVTLog(msg *C.char) { logrus.Infof("🎬 [Moonlight/HW/Win] %s", C.GoString(msg)) }

var vtFrameCount int64

//export goVTFrame
func goVTFrame(rgba *C.uint8_t, width, height, stride C.int) {
	vtFrameCallbackMu.Lock()
	cb := vtFrameCallback
	vtFrameCallbackMu.Unlock()
	if cb == nil {
		return
	}

	cnt := atomic.AddInt64(&vtFrameCount, 1)
	if cnt == 1 {
		logrus.Infof("🎬 [Moonlight/HW/Win] ✅ first video frame — %dx%d", int(width), int(height))
	}

	// When GL overlay is active, the frame was already submitted at C level.
	// Skip the 3.5 MB Go image allocation; deliver nil for stats-only tracking.
	if NativeVideoOverlayIsActive() {
		cb(nil)
		return
	}

	w, h, s := int(width), int(height), int(stride)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rowBytes := w * 4
	if s == rowBytes {
		copy(img.Pix, (*[1 << 30]byte)(unsafe.Pointer(rgba))[:w*h*4:w*h*4])
	} else {
		src := (*[1 << 30]byte)(unsafe.Pointer(rgba))[: h*s : h*s]
		for y := 0; y < h; y++ {
			copy(img.Pix[y*rowBytes:], src[y*s:y*s+rowBytes])
		}
	}
	cb(img)
}

// goAIVisionOverlay is the Windows counterpart to moonlight_cgo_wrapper.go's
// export of the same name (that file is built only for darwin/ios/linux --
// see its own doc comment for why Windows needs a separate definition, and
// win_deliver_frame's call site in this file for why Windows actually has a
// genuine CPU-readable RGBA buffer to overlay into on every frame, unlike
// the true zero-copy GPU-texture paths that comment also describes).
// Identical body: no-op unless the checkbox is on, draws detection boxes
// into rgba in place.
//
//export goAIVisionOverlay
func goAIVisionOverlay(rgba *C.uint8_t, width, height, stride C.int) {
	if rgba == nil || width <= 0 || height <= 0 || stride <= 0 {
		return
	}
	if !aiVisionEnabled.Load() {
		return
	}
	w, h, s := int(width), int(height), int(stride)
	buf := unsafe.Slice((*byte)(unsafe.Pointer(rgba)), s*h)
	ApplyAIVisionOverlay(buf, w, h, s)
}
