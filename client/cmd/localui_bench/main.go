// Command localui_bench is a small standalone diagnostic for the local
// ui.parse offload (see internal/localui and scripts/setup_localui.sh):
// loads the three ONNX models from the default (or LOCALUI_DIR-relative)
// paths and runs the full pipeline once against a screenshot PNG, printing
// timing and the decoded ui_elements/text so a setup can be verified
// without going through the full GUI app or a live device connection.
//
// Usage:
//
//	./scripts/setup_localui.sh          # once per machine
//	go run ./cmd/localui_bench -gpu path/to/screenshot.png
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"usbridge-client/internal/localui"
)

func main() {
	gpu := flag.Bool("gpu", true, "try the OpenVINO GPU execution provider (falls back to CPU automatically)")
	dir := flag.String("dir", defaultLocalUIDir(), "~/.usbridge/localui equivalent (must contain models/ and runtime/)")
	runs := flag.Int("runs", 1, "call Parse this many times on the same image, printing timing for each -- the first run pays one-time ONNX Runtime/OpenVINO graph-compile costs that later calls don't")
	fast := flag.Bool("fast", false, "call ParseFast instead of Parse -- skips drawing+encoding the annotated PNG, matching the ai_vision.go live-overlay hot path (no .localui_marked.png is written)")
	staged := flag.Bool("staged", false, "call ParseStaged instead of Parse/ParseFast -- prints when the icon-only onIcons callback fires relative to the final (icons+OCR) result, matching the ai_vision.go live-overlay hot path")
	nearIcons := flag.Bool("near-icons", false, "call ParseFastNearIcons instead of Parse/ParseFast/ParseStaged -- only OCRs text boxes near a detected icon, matching ai_vision.go's OCR loop")
	textStaged := flag.Bool("text-staged", false, "call ParseFastNearIconsStaged instead -- prints when the onTextBoxes callback fires (dbnet+filter done) relative to the final (OCR'd) result, matching ai_vision.go's OCR loop")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: localui_bench [-gpu] [-dir ~/.usbridge/localui] <screenshot.png>")
		os.Exit(2)
	}
	imgPath := flag.Arg(0)

	cfg := localui.Config{
		IconONNXPath:  filepath.Join(*dir, "models", "icon_detect.onnx"),
		DBNetONNXPath: filepath.Join(*dir, "models", "dbnet.onnx"),
		SVTRONNXPath:  filepath.Join(*dir, "models", "svtr.onnx"),
		SharedLibPath: filepath.Join(*dir, "runtime", localui.DefaultRuntimeLibName()),
		UseGPU:        *gpu,
	}

	t0 := time.Now()
	p, err := localui.NewParser(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewParser: %v\n", err)
		os.Exit(1)
	}
	defer p.Close()
	fmt.Printf("model load: %v\n", time.Since(t0))

	imgBytes, err := os.ReadFile(imgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", imgPath, err)
		os.Exit(1)
	}

	var marked []byte
	var result *localui.Result
	for run := 1; run <= *runs; run++ {
		t0 = time.Now()
		switch {
		case *textStaged:
			result, err = p.ParseFastNearIconsStaged(imgBytes, func(boxes []localui.Box) {
				fmt.Printf("  onTextBoxes fired at %v: %d boxes\n", time.Since(t0), len(boxes))
			})
		case *nearIcons:
			result, err = p.ParseFastNearIcons(imgBytes)
		case *staged:
			result, err = p.ParseStaged(imgBytes, func(icons []localui.Icon) {
				fmt.Printf("  onIcons fired at %v: %d icons\n", time.Since(t0), len(icons))
			})
		case *fast:
			result, err = p.ParseFast(imgBytes)
		default:
			marked, result, err = p.Parse(imgBytes)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Parse: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Parse run %d/%d: %v -- icons=%d text=%d backend=%s\n", run, *runs, time.Since(t0), len(result.Icons), len(result.Text), result.Backend)
	}

	for _, t := range result.Text {
		fmt.Printf("  [%s] conf=%.2f %q\n", t.ID, t.Confidence, t.Text)
	}

	if marked != nil {
		out := imgPath + ".localui_marked.png"
		if err := os.WriteFile(out, marked, 0644); err == nil {
			fmt.Println("wrote", out)
		}
	}
}

func defaultLocalUIDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".usbridge", "localui")
	}
	return filepath.Join(home, ".usbridge", "localui")
}
