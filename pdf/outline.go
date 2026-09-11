package pdf

import "math"

// Some producers draw every glyph as a filled path — no text operators, no
// fonts — and such a page extracts as empty. This file captures those fills
// during the ordinary content-stream walk and says whether a page looks like
// that. Deciding what each outline spells is left to the caller.

const (
	// maxGlyphSize is the longest side, in points, a fill may have and still be
	// taken for a glyph; cell backgrounds and page furniture are larger.
	maxGlyphSize = 36.0
	// maxGlyphAspect bounds the long side against the short one, leaving
	// ruling lines out.
	maxGlyphAspect = 12.0
	// shapeTolerance is how far corresponding coordinates of one outline may
	// wander between its instances: two steps of the 600 dpi grid some
	// producers snap outlines to.
	shapeTolerance = 0.25
)

// pathSeg is one segment of a normalised path: 'm' and 'l' use pts[0], 'c'
// all three.
type pathSeg struct {
	op  byte
	pts [3][2]float64
}

func (s pathSeg) points() [][2]float64 {
	if s.op == 'c' {
		return s.pts[:]
	}
	return s.pts[:1]
}

// filledPath is one path as it was filled, in page space.
//
// Equivalent geometry is written one way: re becomes four lines, v and y become
// c, and every subpath ends with an explicit edge back to its start, which a
// fill implies whether or not the producer wrote h.
type filledPath struct {
	segs    []pathSeg
	evenOdd bool
}

// pathCollector gathers the filled paths of a content stream as the extractor
// walks it, so they are positioned by the same CTM, Form XObject and rotation
// handling as text. Like showRecorder, its methods are nil-safe: extraction
// passes nil when nobody asked for paths, which costs nothing.
type pathCollector struct {
	cur         []pathSeg
	start, last [2]float64
	hasPoint    bool // a current point exists
	closed      bool // the last subpath was closed; the next segment starts a new one at start
	fills       []filledPath
}

// construct applies a path-construction operator to its operands.
func (c *pathCollector) construct(op string, operands []any) {
	if c == nil {
		return
	}
	var n int
	switch op {
	case "m", "l":
		n = 2
	case "v", "y", "re":
		n = 4
	case "c":
		n = 6
	}
	if len(operands) < n {
		return
	}
	var v [6]float64
	for i := range n {
		v[i] = asFloat(operands[len(operands)-n+i])
	}
	switch op {
	case "m":
		c.moveTo(v[0], v[1])
	case "l":
		c.lineTo(v[0], v[1])
	case "c":
		c.curveTo(v[0], v[1], v[2], v[3], v[4], v[5])
	case "v":
		c.curveTo(c.last[0], c.last[1], v[0], v[1], v[2], v[3])
	case "y":
		c.curveTo(v[0], v[1], v[2], v[3], v[2], v[3])
	case "re":
		x, y, w, h := v[0], v[1], v[2], v[3]
		c.moveTo(x, y)
		c.lineTo(x+w, y)
		c.lineTo(x+w, y+h)
		c.lineTo(x, y+h)
		c.closeSubpath()
	}
}

func (c *pathCollector) moveTo(x, y float64) {
	c.closeSubpath()
	if n := len(c.cur); n > 0 && c.cur[n-1].op == 'm' {
		c.cur = c.cur[:n-1] // a subpath with no segments draws nothing
	}
	c.cur = append(c.cur, pathSeg{op: 'm', pts: [3][2]float64{{x, y}}})
	c.start, c.last = [2]float64{x, y}, [2]float64{x, y}
	c.hasPoint, c.closed = true, false
}

// segment appends a segment ending at end. A segment with no current point is
// malformed and dropped; one following a closed subpath starts a new subpath
// at the old one's start, as the spec has it.
func (c *pathCollector) segment(s pathSeg, end [2]float64) bool {
	if !c.hasPoint {
		return false
	}
	if c.closed {
		c.moveTo(c.start[0], c.start[1])
	}
	c.cur = append(c.cur, s)
	c.last = end
	return true
}

func (c *pathCollector) lineTo(x, y float64) {
	c.segment(pathSeg{op: 'l', pts: [3][2]float64{{x, y}}}, [2]float64{x, y})
}

func (c *pathCollector) curveTo(x1, y1, x2, y2, x3, y3 float64) {
	c.segment(pathSeg{op: 'c', pts: [3][2]float64{{x1, y1}, {x2, y2}, {x3, y3}}}, [2]float64{x3, y3})
}

// closeSubpath writes the edge back to the subpath's start, unless the
// producer already drew it.
func (c *pathCollector) closeSubpath() {
	if c == nil || !c.hasPoint || c.closed {
		return
	}
	if c.last != c.start {
		c.cur = append(c.cur, pathSeg{op: 'l', pts: [3][2]float64{c.start}})
		c.last = c.start
	}
	c.closed = true
}

// fill records the current path as filled under ctm and ends it.
func (c *pathCollector) fill(ctm [6]float64, evenOdd bool) {
	if c == nil {
		return
	}
	c.closeSubpath()
	if n := len(c.cur); n > 0 && c.cur[n-1].op == 'm' {
		c.cur = c.cur[:n-1]
	}
	if len(c.cur) > 0 {
		segs := make([]pathSeg, len(c.cur))
		for i, s := range c.cur {
			for k := range s.points() {
				s.pts[k][0], s.pts[k][1] = applyMatrix6(ctm, s.pts[k][0], s.pts[k][1])
			}
			segs[i] = s
		}
		c.fills = append(c.fills, filledPath{segs: segs, evenOdd: evenOdd})
	}
	c.discard()
}

// discard ends the current path without recording it: n, and the stroking
// operators, which draw no glyph outlines.
func (c *pathCollector) discard() {
	if c == nil {
		return
	}
	c.cur = c.cur[:0]
	c.hasPoint, c.closed = false, false
}

// mark and transformSince bracket a Form XObject: its fills are recorded in
// the form's own space and carried out through the form's CTM afterwards, as
// its text spans are.
func (c *pathCollector) mark() int {
	if c == nil {
		return 0
	}
	return len(c.fills)
}

func (c *pathCollector) transformSince(from int, m [6]float64) {
	if c == nil {
		return
	}
	for _, f := range c.fills[from:] {
		f.transform(m)
	}
}

func (f filledPath) transform(m [6]float64) {
	for i := range f.segs {
		for k := range f.segs[i].points() {
			p := &f.segs[i].pts[k]
			p[0], p[1] = applyMatrix6(m, p[0], p[1])
		}
	}
}

// bounds is the box around every point, control points included.
func (f filledPath) bounds() (x0, y0, x1, y1 float64) {
	x0, y0 = math.Inf(1), math.Inf(1)
	x1, y1 = math.Inf(-1), math.Inf(-1)
	for _, s := range f.segs {
		for _, p := range s.points() {
			x0, x1 = math.Min(x0, p[0]), math.Max(x1, p[0])
			y0, y1 = math.Min(y0, p[1]), math.Max(y1, p[1])
		}
	}
	return
}

// isGlyphCandidate reports whether a fill is sized and shaped like a glyph.
// Colour is ignored: white text on a dark cell is still text.
func (f filledPath) isGlyphCandidate() bool {
	x0, y0, x1, y1 := f.bounds()
	long, short := math.Max(x1-x0, y1-y0), math.Min(x1-x0, y1-y0)
	return short > 0 && long <= maxGlyphSize && long <= maxGlyphAspect*short
}

// shapeKey is what two instances of one outline must share exactly.
type shapeKey struct {
	ops     string
	evenOdd bool
}

// shape returns a fill's key and its coordinates relative to its first point,
// so the outline compares equal wherever on the page it was drawn.
func (f filledPath) shape() (shapeKey, []float64) {
	ops := make([]byte, len(f.segs))
	origin := f.segs[0].pts[0]
	var coords []float64
	for i, s := range f.segs {
		ops[i] = s.op
		for _, p := range s.points() {
			coords = append(coords, p[0]-origin[0], p[1]-origin[1])
		}
	}
	return shapeKey{ops: string(ops), evenOdd: f.evenOdd}, coords
}

// clusterShapes assigns each fill a shape ID, numbered in order of first
// appearance.
//
// A fill joins a shape only if every coordinate stays within shapeTolerance of
// the shape's running minimum and maximum. That bounds the whole shape's
// spread, not just each member's distance to the first one, so a chain of
// small differences cannot merge two outlines. An extra shape costs a label; a
// bad merge would corrupt every glyph drawn with it.
func clusterShapes(fills []filledPath) (ids []int, shapes int) {
	type spread struct{ lo, hi []float64 }
	var all []spread
	byKey := make(map[shapeKey][]int)
	ids = make([]int, len(fills))
	for i, f := range fills {
		key, v := f.shape()
		id := -1
		for _, s := range byKey[key] {
			if fitsRange(all[s].lo, all[s].hi, v) {
				id = s
				break
			}
		}
		if id < 0 {
			id = len(all)
			all = append(all, spread{lo: append([]float64(nil), v...), hi: append([]float64(nil), v...)})
			byKey[key] = append(byKey[key], id)
		} else {
			for k, x := range v {
				all[id].lo[k] = math.Min(all[id].lo[k], x)
				all[id].hi[k] = math.Max(all[id].hi[k], x)
			}
		}
		ids[i] = id
	}
	return ids, len(all)
}

func fitsRange(lo, hi, v []float64) bool {
	for k, x := range v {
		if math.Max(hi[k], x)-math.Min(lo[k], x) > shapeTolerance {
			return false
		}
	}
	return true
}

// pageFills returns the page's filled paths in displayed space, in paint order.
func pageFills(page Dict, reader *Reader) ([]filledPath, error) {
	paths := &pathCollector{}
	if _, err := extractPage(page, reader, paths); err != nil {
		return nil, err
	}
	return paths.fills, nil
}

// OutlineHint summarises the glyph-sized fills on a page: the evidence that
// its text may be drawn as outlines rather than with text operators.
type OutlineHint struct {
	Candidates int // filled paths sized and shaped like glyphs
	Shapes     int // distinct outlines among them
}

// Possible reports whether the candidates repeat enough to suggest text drawn
// as outlines: at least 20 of them, at least three per distinct outline.
//
// It is a hint, not a verdict. Repeated icons, checkbox grids and diagrams
// satisfy it too, and a page with only a few outlined words does not.
func (h OutlineHint) Possible() bool {
	return h.Candidates >= 20 && h.Candidates >= 3*h.Shapes
}

// OutlineHint measures the page's glyph-sized fills. Text extraction is
// unaffected: this reads the page separately.
func (p *Page) OutlineHint() (OutlineHint, error) {
	fills, err := pageFills(p.dict, p.reader)
	if err != nil {
		return OutlineHint{}, err
	}
	var cands []filledPath
	for _, f := range fills {
		if f.isGlyphCandidate() {
			cands = append(cands, f)
		}
	}
	_, shapes := clusterShapes(cands)
	return OutlineHint{Candidates: len(cands), Shapes: shapes}, nil
}
