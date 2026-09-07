package localui

import "sort"

// Ported verbatim from usbridge/modules/ui_parser/labels.go -- pure math,
// no OpenCV involved. See that file for the full rationale.

const (
	labelMaxLineGap = 45.0
	labelMaxHOffset = 70.0
)

func associateLabels(icons []Icon, texts []TextRegion) {
	if len(icons) == 0 || len(texts) == 0 {
		return
	}
	claimed := make([]bool, len(texts))

	type candidate struct {
		iconIdx int
		textIdx int
		dist    float64
	}
	var insideCandidates []candidate
	for i, icon := range icons {
		for j, t := range texts {
			if overlapsH(icon.Bbox, t.Bbox) && overlapsV(icon.Bbox, t.Bbox) {
				insideCandidates = append(insideCandidates, candidate{i, j, hCenterDist(icon.Bbox, t.Bbox)})
			}
		}
	}
	sort.Slice(insideCandidates, func(a, b int) bool { return insideCandidates[a].dist < insideCandidates[b].dist })
	insideLabel := make(map[int]int)
	for _, c := range insideCandidates {
		if claimed[c.textIdx] {
			continue
		}
		if _, have := insideLabel[c.iconIdx]; have {
			continue
		}
		insideLabel[c.iconIdx] = c.textIdx
		claimed[c.textIdx] = true
	}
	for iconIdx, textIdx := range insideLabel {
		icons[iconIdx].Label = texts[textIdx].Text
	}

	for i := range icons {
		if icons[i].Label != "" {
			continue
		}
		cursor := icons[i].Bbox
		var lines []string
		for {
			bestIdx := -1
			bestDist := labelMaxLineGap + 1
			for j, t := range texts {
				if claimed[j] {
					continue
				}
				if t.Bbox.Y1 < cursor.Y2 {
					continue
				}
				gap := t.Bbox.Y1 - cursor.Y2
				if gap > labelMaxLineGap {
					continue
				}
				if hCenterDist(icons[i].Bbox, t.Bbox) > labelMaxHOffset {
					continue
				}
				if gap < bestDist {
					bestDist = gap
					bestIdx = j
				}
			}
			if bestIdx < 0 {
				break
			}
			claimed[bestIdx] = true
			lines = append(lines, texts[bestIdx].Text)
			cursor = texts[bestIdx].Bbox
			if len(lines) >= 3 {
				break
			}
		}
		if len(lines) > 0 {
			icons[i].Label = joinChunks(lines)
		}
	}
}

func overlapsH(a, b Box) bool { return a.X1 < b.X2 && a.X2 > b.X1 }
func overlapsV(a, b Box) bool { return a.Y1 < b.Y2 && a.Y2 > b.Y1 }

func hCenterDist(a, b Box) float64 {
	ac := (a.X1 + a.X2) / 2
	bc := (b.X1 + b.X2) / 2
	if ac > bc {
		return ac - bc
	}
	return bc - ac
}

// nearIconGap is how far (in pixels, at native resolution) a detected text
// box can be from the nearest icon's bounding box and still count as
// "near" for filterBoxesNearIcons -- deliberately more generous than
// associateLabels' own labelMaxLineGap/labelMaxHOffset (45/70) above,
// since under-filtering (a few extra OCR'd boxes) is a much cheaper
// mistake than over-filtering (silently dropping a real label).
const nearIconGap = 80.0

// filterBoxesNearIcons keeps only the boxes that fall within nearIconGap
// of at least one icon's bounding box (expanded by that gap on every
// side) -- see ParseFastNearIcons's doc comment in parser.go for why and
// where this is used. Returns boxes unchanged if there are no icons to
// filter against, rather than silently dropping everything.
func filterBoxesNearIcons(icons []Icon, boxes []Box) []Box {
	if len(icons) == 0 {
		return boxes
	}
	var out []Box
	for _, b := range boxes {
		for _, icon := range icons {
			expanded := Box{
				X1: icon.Bbox.X1 - nearIconGap,
				Y1: icon.Bbox.Y1 - nearIconGap,
				X2: icon.Bbox.X2 + nearIconGap,
				Y2: icon.Bbox.Y2 + nearIconGap,
			}
			if overlapsH(b, expanded) && overlapsV(b, expanded) {
				out = append(out, b)
				break
			}
		}
	}
	return out
}
