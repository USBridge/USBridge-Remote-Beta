package localui

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// DefaultRuntimeLibName returns the ONNX Runtime shared library's filename
// on this platform -- callers that resolve a default path under
// ~/.usbridge/localui/runtime (internal/api/local_ui_init.go,
// cmd/localui_bench) join this onto that directory instead of hardcoding
// the Linux ".so" name, which silently never matched on macOS (Homebrew's
// onnxruntime, or scripts/setup_localui.sh's own output once it grows a
// macOS branch, installs "libonnxruntime.dylib" there) and left
// LocalUIParseEnabled looking "not available" with no indication why.
func DefaultRuntimeLibName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libonnxruntime.dylib"
	case "windows":
		return "onnxruntime.dll"
	default:
		return "libonnxruntime.so"
	}
}

var initOnce sync.Once
var initErr error

// initRuntime points onnxruntime_go at a concrete libonnxruntime.so and
// initializes the ORT environment exactly once per process (the C API
// panics if InitializeEnvironment is called twice).
func initRuntime(sharedLibPath string) error {
	initOnce.Do(func() {
		if sharedLibPath != "" {
			ort.SetSharedLibraryPath(sharedLibPath)
		}
		initErr = ort.InitializeEnvironment()
	})
	return initErr
}

// session wraps one ONNX Runtime AdvancedSession for a single fixed-shape
// input/output pair -- all three models here (icon_detect, dbnet, svtr)
// take one float32 tensor in and produce one float32 tensor out.
type session struct {
	s          *ort.AdvancedSession
	inputT     *ort.Tensor[float32]
	outputT    *ort.Tensor[float32]
	inputShape ort.Shape
}

// acceleratorEP picks and appends the best available non-CPU execution
// provider to opts, if useGPU is requested, and reports which one actually
// took (for Parser.Backend/backendLabel) -- "" means it's running on plain
// CPU, either because useGPU was false or every accelerator attempt failed.
//
// OpenVINO's GPU plugin is Intel-only: it silently fails to initialize on
// Apple Silicon, which used to mean the "GPU" checkbox did nothing at all on
// a Mac -- every model quietly ran CPU EP regardless. CoreML is the actual
// accelerator path there: it routes eligible ops to the GPU and/or Apple
// Neural Engine through Core ML. icon_detect and dbnet are plain conv nets
// (YOLOv8 / a DBNet backbone) -- squarely CoreML's sweet spot; SVTR is a
// transformer and benefits less per-op, but batching already amortizes its
// per-call overhead (see batchedSession's doc comment), so it's still worth
// trying. MLComputeUnits=ALL lets Core ML place each op on whichever of
// ANE/GPU/CPU it estimates fastest, rather than pinning everything to one.
//
// Windows had the exact same "GPU checkbox does nothing" bug CoreML fixed on
// macOS: fetch_onnxruntime.sh only ever fetched the plain CPU-only PyPI
// wheel, and the OpenVINO EP this function tried unconditionally on every
// non-darwin OS needs onnxruntime_providers_openvino.dll + a real OpenVINO
// runtime install -- neither ships on Windows, so AppendExecutionProviderOpenVINO
// always errored and silently fell through to CPU, exactly as intended for a
// missing-but-optional accelerator, just never actually accelerating anything
// there. DirectML is the fix: it's a DirectX 12 execution provider, vendor-
// agnostic (works on NVIDIA/AMD/Intel GPUs alike through the one D3D12 driver
// every GPU on Windows already has), and needs no separate vendor SDK -- just
// the onnxruntime.dll + DirectML.dll pair from the "onnxruntime-directml"
// PyPI wheel (see fetch_onnxruntime.sh's windows branch) sitting next to each
// other. Benchmarked live on this dev box's dual-GPU setup (NVIDIA RTX 3090 +
// AMD Radeon 780M iGPU, USBRIDGE_LOCALUI_DEBUG=1, cmd/localui_bench -gpu,
// 1280x800 screenshot, warm runs): CPU EP totaled ~1.08-1.15s/parse
// (icon_detect infer ~283-322ms, dbnet ~211-246ms, svtr ~17-20ms/crop);
// DirectML on the RTX 3090 (see directmlDeviceID) totaled ~430-460ms/parse
// (icon_detect infer ~98-104ms, dbnet ~96-102ms, svtr ~6.3-6.9ms/crop) -- a
// ~2.5x end-to-end win, same shape as CoreML's on Apple Silicon, confirmed
// via `nvidia-smi` showing GPU 0 utilization jump to 34-53% and ~3GB VRAM
// allocated for the process's duration while the AMD iGPU stayed idle.
//
// AMD's Ryzen AI NPU (the XDNA "NPU Compute Accelerator Device" this same
// 780M-equipped chip also exposes -- confirmed present via `Get-PnpDevice`,
// PCI\VEN_1022&DEV_1502) is deliberately NOT attempted here. DirectML only
// ever targets a D3D12 GPU device, never that NPU; reaching the NPU needs
// AMD's separate Ryzen AI Software SDK (the onnxruntime-vitisai EP + AMD's
// proprietary "VOE" runtime + Windows NPU driver, not just the generic
// Microsoft-provided one this box currently has) plus INT8-requantizing all
// three models through AMD's vai_q_onnx quantizer -- a materially bigger,
// separately-versioned pipeline than "fetch a redistributable wheel", closer
// in shape to setup_localui.sh's whole paddle2onnx export step than to a
// switch statement here. Left as a documented follow-up rather than half-
// wired in unverified.
func acceleratorEP(opts *ort.SessionOptions, useGPU bool) string {
	if !useGPU {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		if err := opts.AppendExecutionProviderCoreMLV2(map[string]string{
			"MLComputeUnits": "ALL",
		}); err == nil {
			return "coreml"
		}
		return ""
	case "windows":
		if err := opts.AppendExecutionProviderDirectML(directmlDeviceID()); err == nil {
			return "directml"
		}
		// Falls through to the OpenVINO attempt below for the rare Windows
		// box that has an OpenVINO runtime installed (setup_localui.sh-style
		// manual setup) but no DirectML.dll bundled next to onnxruntime.dll.
	}
	if err := opts.AppendExecutionProviderOpenVINO(map[string]string{
		"device_type": "GPU",
	}); err == nil {
		return "openvino"
	}
	return ""
}

// directmlDeviceID picks which D3D12 adapter DirectML should bind to.
// AppendExecutionProviderDirectML's device_id is a plain index into
// EnumAdapters1 order, not a "give me the fastest one" request -- on a
// machine with more than one GPU (like this dev box's discrete RTX 3090 +
// integrated Radeon 780M) that order is whatever DXGI enumerates, which is
// conventionally-but-not-guaranteed-by-spec the primary/highest-performance
// adapter first. USBRIDGE_LOCALUI_DML_DEVICE overrides it for a box where
// that assumption is wrong (e.g. an external display wired to the iGPU,
// which can flip DXGI's enumeration order) without needing a code change --
// matches USBRIDGE_LOCALUI_DEBUG's env-var-escape-hatch pattern elsewhere in
// this package. Verified on this box via `nvidia-smi --query-gpu=utilization.gpu,memory.used`
// polled during a sustained cmd/localui_bench -gpu run: device 0 drove the
// RTX 3090 to 34-53% utilization and ~3GB VRAM for the run's duration, while
// the 780M iGPU was never touched.
func directmlDeviceID() int {
	if v := os.Getenv("USBRIDGE_LOCALUI_DML_DEVICE"); v != "" {
		if id, err := strconv.Atoi(v); err == nil && id >= 0 {
			return id
		}
	}
	return 0
}

// newSession loads onnxPath and builds a session for a fixed input/output
// shape. useGPU requests an accelerator EP via acceleratorEP; on any failure
// to initialize one (no compatible GPU/ANE, provider not present in this
// libonnxruntime.so, etc.) this transparently falls back to plain CPU --
// matching the device pattern of "degrade to a working default, don't hard
// fail the whole feature over one optional accelerator". Returns the
// accelerator name that actually took ("" if none -- see acceleratorEP).
func newSession(onnxPath string, inputShape, outputShape []int64, useGPU bool) (*session, string, error) {
	inShape := ort.NewShape(inputShape...)
	outShape := ort.NewShape(outputShape...)

	inputT, err := ort.NewEmptyTensor[float32](inShape)
	if err != nil {
		return nil, "", fmt.Errorf("alloc input tensor: %w", err)
	}
	outputT, err := ort.NewEmptyTensor[float32](outShape)
	if err != nil {
		inputT.Destroy()
		return nil, "", fmt.Errorf("alloc output tensor: %w", err)
	}

	opts, err := ort.NewSessionOptions()
	if err != nil {
		inputT.Destroy()
		outputT.Destroy()
		return nil, "", fmt.Errorf("session options: %w", err)
	}
	defer opts.Destroy()
	// ORT's default level already enables most graph optimizations, but
	// pin it explicitly to the max (op fusion, constant folding, etc.) --
	// free wins that cost nothing at runtime, only at load time.
	_ = opts.SetGraphOptimizationLevel(ort.GraphOptimizationLevelEnableAll)

	accel := acceleratorEP(opts, useGPU)

	s, err := loadNamedSession(onnxPath, inputT, outputT, opts)
	if err != nil {
		inputT.Destroy()
		outputT.Destroy()
		return nil, "", err
	}

	return &session{s: s, inputT: inputT, outputT: outputT, inputShape: inShape}, accel, nil
}

// loadNamedSession introspects onnxPath for its single input/output tensor
// names (all three models here have exactly one of each) and builds the
// AdvancedSession -- avoids hardcoding names like "x"/"images" that differ
// between the icon detector and the OCR models.
func loadNamedSession(onnxPath string, inputT, outputT *ort.Tensor[float32], opts *ort.SessionOptions) (*ort.AdvancedSession, error) {
	inputName, outputName, err := ort.GetInputOutputInfo(onnxPath)
	if err != nil {
		return nil, fmt.Errorf("inspect model %s: %w", onnxPath, err)
	}
	if len(inputName) != 1 || len(outputName) != 1 {
		return nil, fmt.Errorf("model %s: expected exactly 1 input and 1 output, got %d/%d", onnxPath, len(inputName), len(outputName))
	}
	return ort.NewAdvancedSession(onnxPath,
		[]string{inputName[0].Name}, []string{outputName[0].Name},
		[]ort.Value{inputT}, []ort.Value{outputT}, opts)
}

// run copies data into the input tensor, executes the session, and returns
// a copy of the output tensor's data.
func (s *session) run(data []float32) ([]float32, error) {
	copy(s.inputT.GetData(), data)
	if err := s.s.Run(); err != nil {
		return nil, err
	}
	out := s.outputT.GetData()
	return append([]float32(nil), out...), nil
}

func (s *session) Close() {
	if s.s != nil {
		s.s.Destroy()
	}
	if s.inputT != nil {
		s.inputT.Destroy()
	}
	if s.outputT != nil {
		s.outputT.Destroy()
	}
}

// batchedSession wraps a DynamicAdvancedSession -- unlike session (fixed
// shape, bound once at creation), this accepts a fresh input tensor with a
// different batch size on every call, output auto-allocated by ONNX
// Runtime. Built for SVTR: a real ui.parse call recognizes anywhere from a
// handful to a couple hundred text crops, and running them one at a time
// (a plain `session`, batch=1 always) spends most of its wall time on
// per-Run() overhead rather than actual compute. Batching amortizes that.
// SVTR always stays on plain CPU EP on darwin (see NewParser's doc comment
// -- CoreML measured a ~5x per-crop regression for it), so svtrBatchSize is
// tuned for CPU, not an accelerator.
type batchedSession struct {
	s          *ort.DynamicAdvancedSession
	inputName  string
	outputName string
}

// svtrBatchSize is the batch size batchRecognizeSVTR groups crops into.
// Re-benchmarked (USBRIDGE_LOCALUI_DEBUG=1, cmd/localui_bench -fast, M1 Air,
// 2880x1800 frame, 272 crops, CPU EP) after the icon_detect/dbnet CoreML
// change freed up CPU headroom that SVTR's threads now get to use: 16 ->
// ~26-36ms/crop, 32 -> ~19.5-25ms/crop, 64 -> ~18.5-20ms/crop (plateau, and
// more wasted padding compute on frames with fewer crops that don't divide
// evenly). 32 is the sweet spot -- most of 64's gain at half the padding
// waste on a typical (non-worst-case) frame.
const svtrBatchSize = 32

func newBatchedSession(onnxPath string, useGPU bool) (*batchedSession, string, error) {
	inputName, outputName, err := ort.GetInputOutputInfo(onnxPath)
	if err != nil {
		return nil, "", fmt.Errorf("inspect model %s: %w", onnxPath, err)
	}
	if len(inputName) != 1 || len(outputName) != 1 {
		return nil, "", fmt.Errorf("model %s: expected exactly 1 input and 1 output, got %d/%d", onnxPath, len(inputName), len(outputName))
	}

	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, "", fmt.Errorf("session options: %w", err)
	}
	defer opts.Destroy()
	_ = opts.SetGraphOptimizationLevel(ort.GraphOptimizationLevelEnableAll)
	accel := acceleratorEP(opts, useGPU)

	s, err := ort.NewDynamicAdvancedSession(onnxPath,
		[]string{inputName[0].Name}, []string{outputName[0].Name}, opts)
	if err != nil {
		return nil, "", err
	}
	return &batchedSession{s: s, inputName: inputName[0].Name, outputName: outputName[0].Name}, accel, nil
}

// run executes one batch: data must hold exactly batch*3*height*width
// float32 values (NCHW, batch outermost). Returns the flat output
// (batch*outPerItem floats) and lets the caller slice per-item.
func (b *batchedSession) run(data []float32, batch, channels, height, width int) ([]float32, error) {
	inShape := ort.NewShape(int64(batch), int64(channels), int64(height), int64(width))
	inputT, err := ort.NewTensor(inShape, data)
	if err != nil {
		return nil, fmt.Errorf("alloc batch input tensor: %w", err)
	}
	defer inputT.Destroy()

	outputs := []ort.Value{nil}
	if err := b.s.Run([]ort.Value{inputT}, outputs); err != nil {
		return nil, err
	}
	outT, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		outputs[0].Destroy()
		return nil, fmt.Errorf("unexpected output tensor type %T", outputs[0])
	}
	defer outT.Destroy()
	return append([]float32(nil), outT.GetData()...), nil
}

func (b *batchedSession) Close() {
	if b.s != nil {
		b.s.Destroy()
	}
}
