package pdf

import (
	"context"
	"fmt"
	"strconv"
)

// GlyphShape is one distinct outline, standing for every glyph drawn with it.
type GlyphShape struct {
	ID            int
	Count         int     // instances in the document
	Path          string  // PDF path operators ending in f or f*, origin at the lower-left of the bounds, y up
	Width, Height float64 // points
}

// GlyphLabeler names shapes. A label may be several characters, for fills that
// merged adjacent glyphs. "" rejects a shape as not text; a shape missing from
// the map is unlabelled.
type GlyphLabeler func(ctx context.Context, shapes []GlyphShape) (map[int]string, error)

// RecoveryReport accounts for every glyph RecoverOutlines found:
// Glyphs = placed + Rejected + the Count of each Unlabeled shape + len(Omitted),
// where placed includes FallbackPlaced and TransferPlaced.
type RecoveryReport struct {
	Glyphs, Shapes  int
	Rejected        int            // glyphs whose shape was labelled "" (not text)
	Unlabeled       []int          // shape IDs left without a label; their glyphs are omitted
	Omitted         []Occurrence   // glyphs with no baseline in bounds; omitted from the text
	FallbackPlaced  []Occurrence   // placed by the bounded fallback; included in the text
	TransferPlaced  []Occurrence   // placed by an offset learned on the same outline at another size; included in the text
	Guessed         []GuessedGlyph // characters decided by context; included in the text
	SpacingFallback bool           // no clear gap valley: word breaks come from local gaps, a degraded mode
}

// Occurrence locates one glyph. It is stable for a given file.
type Occurrence struct {
	Page  int  // 0-based, as in SearchResult
	Index int  // the fill's ordinal in the page's paint order
	Box   Rect // ink bounds in displayed space
}

// GuessedGlyph is a character chosen between look-alikes by its neighbours.
type GuessedGlyph struct {
	Occurrence
	Chosen       string
	Alternatives []string
}

// RecoverOutlines labels the document's outlined glyphs and, on success,
// installs the recovered text: TextSpans, TextLines, Text, Tables and Search
// include it from then on, merged with any real text on the page. The
// recovered spans are words, positioned at their ink extent, with an estimated
// FontSize and no Font.
//
// Recovery is experimental. Its placement and spacing rules were measured on
// one producer (Microsoft Print to PDF), and table structure built over
// recovered text is not claimed to be reliable: two cells closer than 1.5 em
// read as one.
//
// The labeller is called once, with ctx, and only when there are candidates.
// Shapes reach it without order or position. A labeller error or a cancelled
// ctx leaves the document as it was. A successful call replaces any earlier
// recovery. RecoverOutlines must not run concurrently with extraction on the
// same Document.
//
// Removal never sees recovered text: [Editor.RemoveText] and
// [Editor.RemoveRegion] delete only glyphs drawn with text operators, and
// outlines are paths.
func (d *Document) RecoverOutlines(ctx context.Context, label GlyphLabeler) (RecoveryReport, error) {
	a, report, err := d.recoverOutlines(ctx, label)
	if err != nil {
		return RecoveryReport{}, err
	}
	d.recovered = a.spans
	return report, nil
}

// recoverOutlines does all of RecoverOutlines but install the result.
func (d *Document) recoverOutlines(ctx context.Context, label GlyphLabeler) (assembly, RecoveryReport, error) {
	glyphs, shapes, err := d.outlineGlyphs()
	if err != nil {
		return assembly{}, RecoveryReport{}, err
	}
	labels := map[int]string{}
	if len(shapes) > 0 {
		if labels, err = label(ctx, shapes); err != nil {
			return assembly{}, RecoveryReport{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return assembly{}, RecoveryReport{}, err
	}
	for id := range labels {
		if id < 0 || id >= len(shapes) {
			return assembly{}, RecoveryReport{}, fmt.Errorf("pdf: label for unknown shape %d", id)
		}
	}

	report := RecoveryReport{Glyphs: len(glyphs), Shapes: len(shapes)}
	for _, s := range shapes {
		if _, ok := labels[s.ID]; !ok {
			report.Unlabeled = append(report.Unlabeled, s.ID)
		}
	}
	var text []*outlineGlyph
	for _, g := range glyphs {
		l, ok := labels[g.shape]
		switch {
		case !ok:
		case l == "":
			report.Rejected++
		default:
			g.label = l
			text = append(text, g)
		}
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

func (g *outlineGlyph) occurrence() Occurrence {
	return Occurrence{Page: g.page, Index: g.index, Box: Rect{X: g.x0, Y: g.y0, Width: g.x1 - g.x0, Height: g.y1 - g.y0}}
}

// outlineGlyphs returns every glyph candidate in the document, in page and
// paint order, and the shapes they cluster into.
func (d *Document) outlineGlyphs() ([]*outlineGlyph, []GlyphShape, error) {
	var glyphs []*outlineGlyph
	var cands []filledPath
	for p, dict := range d.pages {
		fills, err := pageFills(dict, d.reader)
		if err != nil {
			return nil, nil, err
		}
		for i, f := range fills {
			if !f.isGlyphCandidate() {
				continue
			}
			g := &outlineGlyph{page: p, index: i, fill: f}
			g.x0, g.y0, g.x1, g.y1 = f.bounds()
			glyphs = append(glyphs, g)
			cands = append(cands, f)
		}
	}
	ids, n := clusterShapes(cands)
	shapes := make([]GlyphShape, n)
	rects := make([]bool, n)
	for i, id := range ids {
		if g := glyphs[i]; shapes[id].Count == 0 {
			shapes[id] = GlyphShape{ID: id, Path: cands[i].pdfPath(), Width: g.x1 - g.x0, Height: g.y1 - g.y0}
			rects[id] = cands[i].isRect()
		}
		shapes[id].Count++
		glyphs[i].shape, glyphs[i].rect = id, rects[id]
	}
	return glyphs, shapes, nil
}

// GlyphSheet draws shapes as a one-page PDF grid, each cell tagged with its
// shape's ID, for a labeller that reads images: pass it to a vision model that
// accepts PDF, or rasterise it first.
func GlyphSheet(shapes []GlyphShape) ([]byte, error) {
	const cols, cell, scale = 12, 50.0, 2.0
	rows := max(1, (len(shapes)+cols-1)/cols)
	c := NewCreator()
	pb := c.NewPage(cols*cell, float64(rows)*cell)
	pb.SetFont("Helvetica", 7)
	pb.SetColor(0, 0, 1)
	for i, s := range shapes {
		x, y := float64(i%cols)*cell, float64(rows-1-i/cols)*cell
		pb.DrawText(x+2, y+cell-9, strconv.Itoa(s.ID))
		k := min(scale, (cell-14)/max(s.Width, s.Height))
		fmt.Fprintf(&pb.buf, "q 0 g %[1]s 0 0 %[1]s %s %s cm %s Q\n",
			formatOperand(k), formatOperand(x+(cell-k*s.Width)/2), formatOperand(y+4), s.Path)
	}
	return c.Build()
}
