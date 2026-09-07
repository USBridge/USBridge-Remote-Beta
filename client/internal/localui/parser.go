package localui

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"
)

// debugTiming is set once from USBRIDGE_LOCALUI_DEBUG -- mirrors the
// device-side USBRIDGE_UI_PARSER_DEBUG convention (see
// usbridge/modules/ui_parser/parser.go's debugStats). When set, Parse
// prints a phase-by-phase timing breakdown to stderr: how much of the
// total is decode/encode, YOLO, DBNet (per tile), and SVTR (aggregate
// over all recognized crops) -- the actual answer to "what's the 3-5s
// bottleneck", not a guess.
var debugTiming = os.Getenv("USBRIDGE_LOCALUI_DEBUG") != ""

func debugf(format string, args ...interface{}) {
	if debugTiming {
		fmt.Fprintf(os.Stderr, "[localui] "+format+"\n", args...)
	}
}

const iconInputSize = 640

// Parser is the client-side (ONNX Runtime) counterpart to the device's
// ui_parser.Parser -- same three-model pipeline, same output JSON shape,
// run locally instead of over the network+NPU. See package doc comment.
type Parser struct {
	// iconMu and textMu are separate locks (not one Parser-wide mutex) so
	// a fresh icon_detect call never has to wait behind a still-running
	// dbnet+svtr OCR pass from an older frame -- see ParseIconsOnly's doc
	// comment for why this matters. icon_detect and the dbnet+svtr chain
	// are three independent ONNX Runtime sessions; ORT sessions support
	// concurrent Run() calls, so nothing below actually requires they be
	// serialized against each other, only against themselves (two
	// concurrent icon_detect calls, or two concurrent OCR calls, would
	// still race on the same session's input tensor -- see session.run).
	iconMu sync.Mutex
	icon   *session

	textMu sync.Mutex
	dbnet  *session
	svtr   *batchedSession

	dict  []string
	gpu   bool   // whether the models actually ended up on a non-CPU EP (see acceleratorEP)
	accel string // "coreml", "openvino", or "" -- see acceleratorEP
}

// Config describes where to find the ONNX models and the ONNX Runtime
// shared library, and whether to try the OpenVINO GPU execution provider.
type Config struct {
	IconONNXPath  string
	DBNetONNXPath string
	SVTRONNXPath  string
	SharedLibPath string // path to libonnxruntime.so; "" uses the system default search path
	UseGPU        bool   // try an accelerator EP for icon_detect+dbnet -- CoreML/DirectML/OpenVINO depending on GOOS, see acceleratorEP (SVTR stays CPU on darwin -- see NewParser)
}

// NewParser loads all three ONNX models. icon_detect and dbnet try an
// accelerator EP when Config.UseGPU is set (acceleratorEP -- CoreML on
// macOS, DirectML on Windows, OpenVINO GPU on Linux): all plain conv nets
// and benchmarked at a large win on every platform tried so far --
// (USBRIDGE_LOCALUI_DEBUG=1, cmd/localui_bench, M1 Air) CoreML:
// icon_detect infer+decode ~800-1090ms -> ~90-340ms, dbnet's 12 tiles
// ~4.0s -> ~1.6-2.3s total; (same tool, Windows box w/ RTX 3090 + Radeon
// 780M, 1280x800 single-tile frame) DirectML: icon_detect infer
// ~283-322ms -> ~98-104ms, dbnet ~211-246ms -> ~96-102ms.
//
// SVTR is deliberately NOT given useGPU on darwin: the same benchmark run
// showed batched CoreML SVTR at ~150-165ms/crop vs. batched CPU's
// ~30-35ms/crop -- a ~5x REGRESSION, not a win (CoreML's per-batch dispatch
// overhead for this model's varying batch shapes swamps whatever the ANE/GPU
// saves on the compute itself). Non-darwin keeps the GPU EP attempt for SVTR
// too: OpenVINO's batch=16 win was benchmarked for real (batchedSession's
// doc comment) and hasn't been re-tested against CoreML's regression, so
// there's no evidence to change it there; DirectML's SVTR win was directly
// benchmarked instead of assumed -- same Windows run above, batched SVTR
// dropped from CPU's ~17-20ms/crop to DirectML's ~6.3-6.9ms/crop, no
// CoreML-style regression on this GPU stack.
func NewParser(cfg Config) (*Parser, error) {
	if err := initRuntime(cfg.SharedLibPath); err != nil {
		return nil, fmt.Errorf("init onnxruntime: %w", err)
	}

	icon, iconAccel, err := newSession(cfg.IconONNXPath, []int64{1, 3, iconInputSize, iconInputSize}, []int64{1, 5, 8400}, cfg.UseGPU)
	if err != nil {
		return nil, fmt.Errorf("load icon_detect: %w", err)
	}
	dbnet, dbnetAccel, err := newSession(cfg.DBNetONNXPath, []int64{1, 3, dbnetMapSize, dbnetMapSize}, []int64{1, 1, dbnetMapSize, dbnetMapSize}, cfg.UseGPU)
	if err != nil {
		icon.Close()
		return nil, fmt.Errorf("load paddle_dbnet: %w", err)
	}
	svtrUseGPU := cfg.UseGPU && runtime.GOOS != "darwin"
	svtr, svtrAccel, err := newBatchedSession(cfg.SVTRONNXPath, svtrUseGPU)
	if err != nil {
		icon.Close()
		dbnet.Close()
		return nil, fmt.Errorf("load paddle_svtr: %w", err)
	}

	// accel reflects whichever accelerator icon_detect landed on -- the
	// three models are always requested together (Config.UseGPU is one
	// knob), and in practice all three succeed or all three fall back
	// together, so it's representative for the Backend label without
	// needing a three-way string.
	accel := iconAccel
	if accel == "" {
		accel = dbnetAccel
	}
	if accel == "" {
		accel = svtrAccel
	}

	return &Parser{icon: icon, dbnet: dbnet, svtr: svtr, dict: loadSVTRDict(), gpu: accel != "", accel: accel}, nil
}

func (p *Parser) Close() {
	p.iconMu.Lock()
	p.icon.Close()
	p.iconMu.Unlock()

	p.textMu.Lock()
	p.dbnet.Close()
	p.svtr.Close()
	p.textMu.Unlock()
}

// Parse runs the full local pipeline on a PNG-encoded screenshot (as
// produced by the device's screen.get_image) and returns the annotated PNG
// plus the structured result, in the same shape as the device's own
// ui.parse response (see types.go).
func (p *Parser) Parse(imgBytes []byte) (markedPNG []byte, result *Result, err error) {
	return p.parse(imgBytes, true, nil, nil, nil)
}

// ParseFast is Parse without producing the annotated marked-up PNG: it
// returns the same structured Result, just with markedPNG always nil.
// drawResult (draw boxes + tags into a copy, then PNG-encode it) measured
// ~1.2-1.3s of a ~13s Parse call on a 2880x1800 frame -- real cost, but
// pure waste for a caller that only wants Result and never looks at the
// image. ai_vision.go's OCR loop (maybeKickOCR) uses this: it burns its
// own overlay directly into the live video frame via drawCachedOverlay,
// using the same localui.DrawDetectionBox/Tag helpers drawResult calls, so
// the annotated PNG Parse would have built would just be discarded. MCP's
// ui.parse (local_ui_intercept.go) keeps using plain Parse: its JSON-RPC
// result actually returns the annotated image to the caller.
func (p *Parser) ParseFast(imgBytes []byte) (result *Result, err error) {
	_, result, err = p.parse(imgBytes, false, nil, nil, nil)
	return result, err
}

// ParseFastNearIcons is ParseFast, plus one more filter: after dbnet finds
// text boxes and before the expensive SVTR OCR stage runs on them, boxes
// with no detected icon within nearIconGap pixels (labels.go) are dropped.
// Standalone text with no nearby icon -- a paragraph, a line of code, a
// filename far from any button -- never gets OCR'd at all.
//
// This is NOT what MCP's ui.parse (local_ui_intercept.go, via plain
// ParseFast/Parse) should ever use: an agent may legitimately need to read
// arbitrary on-screen text that has nothing to do with an icon. It's built
// for ai_vision.go's live *preview* overlay instead, where an operator
// mainly cares what's clickable and how it's labeled -- and where, on a
// text-heavy screen (an IDE, a document), most dbnet-detected boxes aren't
// icon labels at all. Measured on a real dense code-editor screenshot via
// cmd/localui_bench -near-icons -- see that run's numbers for the actual
// before/after crop count and timing (results vary a lot by how
// icon-dense vs. text-dense a given frame is, unlike the roughly-fixed
// percentages the other perf changes in this package measured).
func (p *Parser) ParseFastNearIcons(imgBytes []byte) (result *Result, err error) {
	_, result, err = p.parse(imgBytes, false, nil, filterBoxesNearIcons, nil)
	return result, err
}

// ParseFastNearIconsStaged is ParseFastNearIcons plus one more checkpoint:
// onTextBoxes (if non-nil) is invoked with the filtered dbnet box
// *positions* as soon as they're ready -- before the much slower svtr OCR
// stage even starts. dbnet is nearly as cheap as icon_detect on CoreML
// (~1-2s for 12 tiles, see NewParser's doc comment); svtr is what actually
// costs the 8-11s a text-heavy pass takes (onnx.go's svtrBatchSize doc
// comment). Before this, "detected but not yet recognized" text boxes were
// invisible: Result.Text only ever contained entries SVTR had finished
// recognizing, so green boxes only appeared once EVERYTHING -- detection
// *and* recognition -- was done, even though detecting their positions
// (dbnet) is cheap and fast, same as icon_detect.
//
// The boxes handed to onTextBoxes carry no text and no ID (recognition and
// ID assignment both require the final pass) -- callers draw them as bare
// outlines, then swap in the real TextRegion (with its recognized string
// and tag) once the full result arrives.
func (p *Parser) ParseFastNearIconsStaged(imgBytes []byte, onTextBoxes func(boxes []Box)) (result *Result, err error) {
	_, result, err = p.parse(imgBytes, false, nil, filterBoxesNearIcons, onTextBoxes)
	return result, err
}

// ParseStaged is ParseFast plus one extra checkpoint: onIcons (if non-nil)
// is invoked with the icon_detect results as soon as they're ready --
// before the much slower dbnet+svtr text/OCR stage even starts. With
// CoreML, icon_detect (prep+infer+decode) typically finishes in a few
// hundred ms; OCR on a text-heavy frame is still several seconds (see
// onnx.go's svtrBatchSize doc comment) -- so a caller that only cares
// about "where are the clickable things" no longer has to wait for OCR to
// find out.
//
// Icon IDs are stable between the two callbacks: assignMarkIDs always
// numbers icons before text (marks.go), so onIcons's icons and the icons
// in the final Result carry the same "00".."FF" tags -- nothing gets
// renumbered once OCR finishes. icons[i].Label is empty at the onIcons
// callback (labels come from nearby OCR'd text via associateLabels, which
// needs text and only runs once OCR is done) and gets filled in on the
// final Result as usual.
//
// ai_vision.go doesn't actually use this anymore -- it runs icon_detect
// and OCR as two fully independent loops instead (ParseIconsOnly +
// ParseFast, see maybeKickIconDetection/maybeKickOCR) so the icon loop
// never has to share a goroutine, or wait its turn, with a still-running
// OCR pass at all. ParseStaged is kept for cmd/localui_bench -staged and
// any future caller that specifically wants "one pass, two checkpoints"
// rather than two independently-paced passes.
//
// Built for ai_vision.go's live overlay (maybeKickDetection): before this,
// the icon/grid overlay and the text overlay updated together once per
// Parse call, so a slow OCR pass on a text-heavy frame held up icon boxes
// that were ready seconds earlier -- exactly the "OCR blocks grid
// detection" behavior this exists to fix. Since iconMu/textMu are separate
// locks (see Parser's doc comment), a ParseIconsOnly call from a newer
// frame can also now run concurrently with this call's OCR stage instead
// of queuing up behind it -- so ai_vision.go pairs this with its own
// separate icon-only kickoff loop (see ParseIconsOnly) rather than relying
// on ParseStaged calls alone to keep the grid responsive pass-to-pass.
func (p *Parser) ParseStaged(imgBytes []byte, onIcons func(icons []Icon)) (result *Result, err error) {
	_, result, err = p.parse(imgBytes, false, onIcons, nil, nil)
	return result, err
}

// ParseIconsOnly decodes imgBytes and runs icon_detect ONLY -- no dbnet, no
// svtr, no marked PNG. Assigns icons their own "00".."FF" IDs (assignMarkIDs
// with no text) as if there were no text at all; a concurrent/later
// ParseStaged/ParseFast/Parse call numbers icons the same way (icons always
// come first, see assignMarkIDs), so in practice IDs from two calls on a
// similar frame line up, but there's no hard guarantee across independent
// calls the way there is between ParseStaged's onIcons callback and its own
// final Result (same slice, numbered once).
//
// Built for ai_vision.go's live overlay: icon_detect is cheap (CoreML:
// typically 100-300ms, see NewParser's doc comment), so it's given its own
// fast, independent kickoff cadence, decoupled from the much slower dbnet+
// svtr OCR pass. This only works because it takes iconMu, never textMu (see
// Parser's doc comment) -- it can run concurrently with an OCR pass from an
// older frame still in flight on a different goroutine. This is what
// actually keeps the visible grid responsive: before iconMu/textMu were
// split apart, ANY new detection pass -- icon-only or not -- had to wait
// for the previous pass's OCR to finish and release Parser's one shared
// mutex, however long that took (multiple seconds on a text-heavy frame),
// which is what made box positions visibly lag behind the actual screen.
func (p *Parser) ParseIconsOnly(imgBytes []byte) (icons []Icon, err error) {
	original, err := decodeToRGB(imgBytes)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	if original.W == 0 || original.H == 0 {
		return nil, fmt.Errorf("decode image: empty result")
	}
	icons, err = p.runIconStage(original)
	if err != nil {
		return nil, err
	}
	assignMarkIDs(icons, nil)
	return icons, nil
}

// runIconStage runs icon_detect (CLAHE+letterbox prep, inference, YOLO
// decode+coordinate-mapping) and returns icons in original-image
// coordinates -- no IDs assigned, no labels (callers number/associate as
// appropriate for their own context, see ParseIconsOnly and parse's
// onIcons handling). Only the inference call itself (p.icon.run) needs
// iconMu -- prep is pure Go/CPU and decode is pure math on the output, so
// neither touches the shared session state a concurrent call could race on.
func (p *Parser) runIconStage(original *rgbImage) ([]Icon, error) {
	tIcon := time.Now()
	clahe := applyCLAHE(original)
	claheLB, iconLBMeta := letterboxRGB(clahe, iconInputSize)
	iconInput := claheLB.toNCHWFloat()
	tIconPrep := time.Since(tIcon)

	p.iconMu.Lock()
	tIconInfer := time.Now()
	iconOut, err := p.icon.run(iconInput)
	p.iconMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("icon_detect inference: %w", err)
	}

	var icons []Icon
	for _, icon := range decodeYOLO(iconOut) {
		icon.Bbox = iconLBMeta.toOriginal(icon.Bbox)
		icons = append(icons, icon)
	}
	debugf("icon_detect: prep(CLAHE+letterbox)=%v infer+decode=%v -> %d icons", tIconPrep, time.Since(tIconInfer), len(icons))
	return icons, nil
}

func (p *Parser) parse(imgBytes []byte, drawMarked bool, onIcons func(icons []Icon), textFilter func(icons []Icon, boxes []Box) []Box, onTextBoxes func(boxes []Box)) (markedPNG []byte, result *Result, err error) {
	t0 := time.Now()

	tDecode := time.Now()
	original, err := decodeToRGB(imgBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("decode image: %w", err)
	}
	if original.W == 0 || original.H == 0 {
		return nil, nil, fmt.Errorf("decode image: empty result")
	}
	debugf("decode PNG (%dx%d, %d bytes): %v", original.W, original.H, len(imgBytes), time.Since(tDecode))

	res := &Result{ImageWidth: original.W, ImageHeight: original.H, Backend: backendLabel(p.accel)}

	res.Icons, err = p.runIconStage(original)
	if err != nil {
		return nil, nil, err
	}

	if onIcons != nil {
		// iconsCopy: onIcons's caller (ai_vision.go) hands this straight to
		// a live overlay that can render concurrently with the rest of this
		// call still mutating res.Icons' backing array via associateLabels
		// below -- give it an independent slice+backing array so there's no
		// data race between "OCR fills in Label" here and "overlay reads
		// Label" there.
		iconsCopy := append([]Icon(nil), res.Icons...)
		assignMarkIDs(iconsCopy, nil) // stable: icons are always numbered before text, see assignMarkIDs
		onIcons(iconsCopy)
	}

	// -- Text (tiled DBNet, see tile.go) --
	// textMu is a separate lock from iconMu (see Parser's doc comment):
	// held for the rest of this call so a concurrent ParseIconsOnly on a
	// newer frame never has to wait for this dbnet+svtr pass to finish.
	p.textMu.Lock()
	defer p.textMu.Unlock()

	tiles := tileRects(original.W, original.H, dbnetMapSize, dbnetTileOverlap)
	if tiles == nil {
		tiles = []rect{{X1: 0, Y1: 0, X2: original.W, Y2: original.H}}
	}

	tDBNet := time.Now()
	var allTextBoxes []Box
	for _, t := range tiles {
		boxes, err := p.detectTextInTile(original, t)
		if err != nil {
			return nil, nil, err
		}
		allTextBoxes = append(allTextBoxes, boxes...)
	}
	textBoxes := mergeOverlappingBoxes(allTextBoxes, 0.3)
	debugf("dbnet: %d tiles, %v total (%v/tile avg) -> %d boxes after merge", len(tiles), time.Since(tDBNet), time.Since(tDBNet)/time.Duration(len(tiles)), len(textBoxes))

	if textFilter != nil {
		before := len(textBoxes)
		textBoxes = textFilter(res.Icons, textBoxes)
		debugf("text filter: %d -> %d boxes", before, len(textBoxes))
	}

	if onTextBoxes != nil {
		// boxesCopy: same independent-slice rationale as onIcons's iconsCopy
		// above -- the caller may render this concurrently with
		// batchRecognizeSVTR/associateLabels still running below.
		boxesCopy := append([]Box(nil), textBoxes...)
		onTextBoxes(boxesCopy)
	}

	tSVTR := time.Now()
	res.Text = p.batchRecognizeSVTR(original, textBoxes)
	svtrElapsed := time.Since(tSVTR)
	perCrop := time.Duration(0)
	if len(textBoxes) > 0 {
		perCrop = svtrElapsed / time.Duration(len(textBoxes))
	}
	debugf("svtr: %d crops, %v total (%v/crop avg) -> %d texts recognized", len(textBoxes), svtrElapsed, perCrop, len(res.Text))

	tAssoc := time.Now()
	associateLabels(res.Icons, res.Text)
	assignMarkIDs(res.Icons, res.Text)
	res.ZoomHints = findZoomHints(res.Icons)
	debugf("associate+mark: %v", time.Since(tAssoc))

	if drawMarked {
		tDraw := time.Now()
		markedPNG = drawResult(original, res)
		debugf("draw+encode marked PNG (%d bytes): %v", len(markedPNG), time.Since(tDraw))
	}

	debugf("TOTAL: %v", time.Since(t0))
	return markedPNG, res, nil
}

func backendLabel(accel string) string {
	if accel == "" {
		return "local-onnx-cpu"
	}
	return "local-onnx-" + accel
}

func (p *Parser) detectTextInTile(original *rgbImage, t rect) ([]Box, error) {
	tileImg, dbLBMeta := prepareDBNetTile(original, t, dbnetMapSize)
	dbInput := tileImg.toNCHWFloat()

	dbOut, err := p.dbnet.run(dbInput)
	if err != nil {
		return nil, fmt.Errorf("paddle_dbnet inference: %w", err)
	}

	var boxes []Box
	for _, boxLocal := range decodeDBNet(dbOut) {
		boxTile := dbLBMeta.toOriginal(boxLocal)
		boxes = append(boxes, Box{
			X1: boxTile.X1 + float64(t.X1),
			Y1: boxTile.Y1 + float64(t.Y1),
			X2: boxTile.X2 + float64(t.X1),
			Y2: boxTile.Y2 + float64(t.Y1),
		})
	}
	return boxes, nil
}

func prepareDBNetTile(original *rgbImage, t rect, size int) (*rgbImage, letterboxMeta) {
	crop := original.region(t.X1, t.Y1, t.X2, t.Y2)
	if crop.W == size && crop.H == size {
		return applyGrayCLAHE(crop), letterboxMeta{scale: 1}
	}
	lb, meta := letterboxRGB(crop, size)
	return applyGrayCLAHE(lb), meta
}

const maxSVTRAspect = 6.0

// svtrJob is one SVTR crop queued for batched recognition: either a whole
// text box, or one chunk of a box too wide for a single 48x320 SVTR pass
// (see planSVTRCrops). textIdx groups chunks back to their original box
// after decoding, the same way the old per-box recognizeBox loop did
// inline -- just decoupled from the actual inference call so many jobs
// across many boxes can share one batched Run().
type svtrJob struct {
	textIdx int // index into the textBoxes slice this crop belongs to
	crop    Box
}

// planSVTRCrops returns the SVTR crop(s) for one detected text box: the
// box itself if its aspect ratio fits in one 48x320 pass, or several
// overlapping chunks otherwise -- identical splitting logic to the old
// recognizeBox, just returning the plan instead of immediately running
// inference on it.
func planSVTRCrops(box Box) []Box {
	w := box.X2 - box.X1
	h := box.Y2 - box.Y1
	if h <= 0 || w/h <= maxSVTRAspect {
		return []Box{box}
	}

	chunkW := maxSVTRAspect * h
	overlap := chunkW * 0.15
	n := int(w/(chunkW-overlap)) + 1

	var chunks []Box
	for i := 0; i < n; i++ {
		x1 := box.X1 + float64(i)*(chunkW-overlap)
		x2 := x1 + chunkW
		if x2 > box.X2 {
			x2 = box.X2
		}
		if x2-x1 < h {
			continue
		}
		chunks = append(chunks, Box{X1: x1, Y1: box.Y1, X2: x2, Y2: box.Y2})
	}
	return chunks
}

// batchRecognizeSVTR runs SVTR over every detected text box, batching
// crops svtrBatchSize at a time instead of one Run() call per crop -- see
// batchedSession's doc comment for why (per-call overhead, not compute,
// dominated the old serial loop).
func (p *Parser) batchRecognizeSVTR(original *rgbImage, textBoxes []Box) []TextRegion {
	var jobs []svtrJob
	for i, box := range textBoxes {
		for _, crop := range planSVTRCrops(box) {
			jobs = append(jobs, svtrJob{textIdx: i, crop: crop})
		}
	}
	if len(jobs) == 0 {
		return nil
	}

	const itemFloats = 3 * svtrHeight * svtrWidth
	const outFloats = svtrTimeSteps * svtrNumClasses

	type partial struct {
		text string
		conf float64
	}
	parts := make(map[int][]partial, len(textBoxes))

	for start := 0; start < len(jobs); start += svtrBatchSize {
		end := start + svtrBatchSize
		if end > len(jobs) {
			end = len(jobs)
		}
		batch := jobs[start:end]

		buf := make([]float32, len(batch)*itemFloats)
		valid := make([]bool, len(batch))
		for i, j := range batch {
			crop, ok := safeCrop(original, j.crop)
			if !ok {
				continue
			}
			copy(buf[i*itemFloats:(i+1)*itemFloats], preprocessSVTRCrop(crop))
			valid[i] = true
		}

		out, err := p.svtr.run(buf, len(batch), 3, svtrHeight, svtrWidth)
		if err != nil {
			continue // this batch's crops just contribute no text, rest of Parse still succeeds
		}
		for i, j := range batch {
			if !valid[i] {
				continue
			}
			logits := out[i*outFloats : (i+1)*outFloats]
			text, conf := ctcGreedyDecodeSVTR(logits, p.dict)
			if text == "" {
				continue
			}
			parts[j.textIdx] = append(parts[j.textIdx], partial{text: text, conf: conf})
		}
	}

	var out []TextRegion
	for i, box := range textBoxes {
		ps := parts[i]
		if len(ps) == 0 {
			continue
		}
		texts := make([]string, len(ps))
		var confSum float64
		for k, pt := range ps {
			texts[k] = pt.text
			confSum += pt.conf
		}
		out = append(out, TextRegion{Bbox: box, Text: joinChunks(texts), Confidence: confSum / float64(len(ps))})
	}
	return out
}

func safeCrop(img *rgbImage, box Box) (*rgbImage, bool) {
	x1, y1 := int(box.X1), int(box.Y1)
	x2, y2 := int(box.X2), int(box.Y2)
	if x1 < 0 {
		x1 = 0
	}
	if y1 < 0 {
		y1 = 0
	}
	if x2 > img.W {
		x2 = img.W
	}
	if y2 > img.H {
		y2 = img.H
	}
	if x2-x1 < 2 || y2-y1 < 2 {
		return nil, false
	}
	return img.region(x1, y1, x2, y2), true
}
