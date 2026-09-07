# Local ui.parse ONNX models

The three ONNX weight files in this directory back `internal/localui`'s
client-side mirror of the device's `modules/ui_parser` Set-of-Mark pipeline:
YOLOv8 icon/element detector + PaddleOCR-style DBNet text detector + SVTR
text recognizer -- run on CPU, or accelerated via CoreML (macOS Apple
Silicon), DirectML (Windows, any NVIDIA/AMD/Intel GPU), or OpenVINO (Linux,
Intel iGPU), see `internal/localui/onnx.go`'s `acceleratorEP`. See that
package's doc comment and `internal/api/local_ui_intercept.go` for how
they're used.

They're committed here (rather than left as a fetch-on-demand blob) so a
clone of this open repo is self-contained and buildable without needing
Docker, PaddlePaddle, or access to the private `usbridge` (device) repo
just to exercise the local offload path.

| File | Size | Model |
|---|---|---|
| `icon_detect.onnx` | ~77 MiB | YOLOv8 icon/element detector (OmniParser-v2 `icon_detect`), fp32, 640×640 input |
| `dbnet.onnx` | ~2.3 MiB | PP-OCRv3 multilingual DBNet text-region detector, 960×960 input |
| `svtr.onnx` | ~8.6 MiB | PP-OCRv3 Cyrillic SVTR text recognizer, 48×320 input |

## Provenance

Same upstream sources as the device's `.rknn` deploy artifacts (see
`usbridge/models/ui_parser/README.md`'s own Provenance section), exported
one step earlier in the pipeline -- ONNX instead of RKNN, no NPU
quantization/compilation:

- `icon_detect.onnx`: `microsoft/OmniParser-v2.0`'s `icon_detect/model.pt`
  (YOLOv8), exported straight to ONNX via ultralytics' own exporter
  (opset 12, 640×640).
- `dbnet.onnx` / `svtr.onnx`: PaddleOCR's `Multilingual_PP-OCRv3_det_infer`
  / `cyrillic_PP-OCRv3_rec_infer` inference models, converted with
  `paddle2onnx` (opset 11).

`../scripts/setup_localui.sh` is the reproducible record of exactly how
these were produced (and is still the right tool to regenerate them after
an upstream model update) -- it now copies its output here instead of only
to `~/.usbridge/localui/models`, so re-running it after a model refresh
is how these get updated.

Verified end-to-end against a live 1920x1080 screenshot with
`cmd/localui_bench` before committing (icon boxes + recognized Cyrillic/
Latin text, Set-of-Mark ids assigned) -- not just "loads without erroring".
