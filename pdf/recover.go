package pdf

import (
	"context"
	"fmt"
)

// glyphShape is one distinct outline, standing for every glyph drawn with it.
type glyphShape struct {
	ID            int
	Count         int     // instances in the document
	Path          string  // PDF path operators ending in f or f*, origin at the lower-left of the bounds, y up
	Width, Height float64 // points
}

// glyphLabeler names shapes. A label may be several characters, for fills that
// merged adjacent glyphs. "" rejects a shape as not text; a shape missing from
// the map is unlabelled.
type glyphLabeler func(ctx context.Context, shapes []glyphShape) (map[int]string, error)

// recoveryReport accounts for every glyph computeRecovery found:
// Glyphs = placed + Rejected + the Count of each Unlabeled shape + len(Omitted),
// where placed includes FallbackPlaced, TransferPlaced and Rehomed. A glyph
// that could belong to two lines is decided by the fallback, and appears in
// FallbackPlaced or Omitted.
type recoveryReport struct {
	Glyphs, Shapes  int
	Rejected        int            // glyphs whose shape was labelled "" (not text)
	Unlabeled       []int          // shape IDs left without a label; their glyphs are omitted
	Omitted         []glyphRef     // glyphs with no baseline in bounds; omitted from the text
	FallbackPlaced  []glyphRef     // placed by the bounded fallback; included in the text
	TransferPlaced  []glyphRef     // placed by an offset learned on the same outline at another size; included in the text
	Rehomed         []glyphRef     // moved off a line only their own shape predicted, into the line whose text they sit in; included in the text
	Guessed         []guessedGlyph // look-alikes decided by the letters around them; included in the text
	SpacingFallback bool           // no clear gap valley: word breaks come from local gaps, a degraded mode
}

// glyphRef locates one glyph. It is stable for a given file.
type glyphRef struct {
	Page  int  // 0-based, as in SearchResult
	Index int  // the fill's ordinal in the page's paint order
	Box   Rect // ink bounds in displayed space
}

// guessedGlyph is a character chosen between look-alikes (l, I and | drawn as
// one bare rectangle), and what chose it.
type guessedGlyph struct {
	glyphRef
	Chosen string
	By     labelEvidence
}

// labelEvidence is what decided a guessedGlyph.
type labelEvidence int

const (
	// evidenceByNeighbour: the word's case settles nothing, so the glyph is I beside
	// a capital and l otherwise. A guess: Item reads ltem, and myItem myltem.
	evidenceByNeighbour labelEvidence = iota
	// evidenceByCase: the word's other letters, up to a non-letter, are capitals (I),
	// or a capital followed by lowercase (l).
	evidenceByCase
)

// computeRecovery labels the document's outlined glyphs and assembles them
// into words: per page, spans positioned at their ink extent, with an
// estimated FontSize and no Font. The document is not changed.
//
// Recovery is experimental. Its placement and spacing rules were measured on
// one producer (Microsoft Print to PDF), and table structure built over
// recovered text is not claimed to be reliable: two cells closer than 1.5 em
// read as one.
//
// The labeller is called once, with ctx, and only when there are candidates.
// Shapes reach it without order or position.
func (d *Document) computeRecovery(ctx context.Context, label glyphLabeler) (assembly, recoveryReport, error) {
	glyphs, shapes, err := d.outlineGlyphs()
	if err != nil {
		return assembly{}, recoveryReport{}, err
	}
	labels := map[int]string{}
	if len(shapes) > 0 {
		if labels, err = label(ctx, shapes); err != nil {
			return assembly{}, recoveryReport{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return assembly{}, recoveryReport{}, err
	}
	for id := range labels {
		if id < 0 || id >= len(shapes) {
			return assembly{}, recoveryReport{}, fmt.Errorf("pdf: label for unknown shape %d", id)
		}
	}

	report := recoveryReport{Glyphs: len(glyphs), Shapes: len(shapes)}
	for _, s := range shapes {
		if _, ok := labels[s.ID]; !ok {
			report.Unlabeled = append(report.Unlabeled, s.ID)
		}
	}
	var text []*outlineGlyph
	for _, g := range glyphs {
		l, ok := labels[g.shape]
		if !ok {
			continue
		}
		if l == "" {
			report.Rejected++
			continue
		}
		g.label = l
		text = append(text, g)
	}
	a := assemble(len(d.pages), text, &report)
	return a, report, nil
}

// outlineGlyph is one glyph candidate: where it was painted and which shape drew it.
type outlineGlyph struct {
	page, index    int
	x0, y0, x1, y1 float64 // ink bounds, displayed space
	shape          int
	rect           bool // the shape is a bare axis-aligned rectangle
	label          string
	fill           filledPath
}

func (g *outlineGlyph) occurrence() glyphRef {
	return glyphRef{Page: g.page, Index: g.index, Box: Rect{X: g.x0, Y: g.y0, Width: g.x1 - g.x0, Height: g.y1 - g.y0}}
}

// outlineGlyphs returns every glyph candidate in the document, in page and
// paint order, and the shapes they cluster into.
func (d *Document) outlineGlyphs() ([]*outlineGlyph, []glyphShape, error) {
	var glyphs []*outlineGlyph
	var cands []filledPath
	for p, dict := range d.pages {
		fills, err := pageFills(dict, d.reader)
		if err != nil {
			return nil, nil, err
		}
		pageCands, index := glyphCandidates(fills)
		for k, f := range pageCands {
			g := &outlineGlyph{page: p, index: index[k], fill: f}
			g.x0, g.y0, g.x1, g.y1 = f.bounds()
			glyphs = append(glyphs, g)
		}
		cands = append(cands, pageCands...)
	}
	ids, n := clusterShapes(cands)
	shapes := make([]glyphShape, n)
	rects := make([]bool, n)
	for i, id := range ids {
		if g := glyphs[i]; shapes[id].Count == 0 {
			shapes[id] = glyphShape{ID: id, Path: cands[i].pdfPath(), Width: g.x1 - g.x0, Height: g.y1 - g.y0}
			rects[id] = cands[i].isRect()
		}
		shapes[id].Count++
		glyphs[i].shape, glyphs[i].rect = id, rects[id]
	}
	return glyphs, shapes, nil
}
