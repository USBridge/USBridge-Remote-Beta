package localui

import (
	"fmt"
	"runtime"
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
func acceleratorEP(opts *ort.SessionOptions, useGPU bool) string {
	if !useGPU {
		return ""
	}
	if runtime.GOOS == "darwin" {
		if err := opts.AppendExecutionProviderCoreMLV2(map[string]string{
			"MLComputeUnits": "ALL",
		}); err == nil {
			return "coreml"
		}
		return ""
	}
	if err := opts.AppendExecutionProviderOpenVINO(map[string]string{
		"device_type": "GPU",
	}); err == nil {
		return "openvino"
	}
	return ""
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
