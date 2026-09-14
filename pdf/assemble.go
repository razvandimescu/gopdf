package pdf

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Assembly turns labelled glyphs into words on baselines. Every constant here
// was measured on one document from one producer — Microsoft Print to PDF, one
// Helvetica-class family at two sizes — and none is claimed beyond it.
//
// Vertical terms: a glyph's bottom is its lowest ink point, and its offset is
// the distance from its bottom up to the baseline it sits on: positive for
// descenders, negative for a hyphen.

const (
	// seedChars sit flat on the baseline: their bottoms locate it.
	seedChars = "ABDEFHIKLMNPRTXZhiklmnrxz1247"
	// capHeight is the Helvetica-class cap height, in em.
	capHeight = 0.72
	// baselineTolerance is how far, in points, two bottoms may differ and sit
	// on one baseline: 2.5 steps of a 600 dpi grid. It must stay below the
	// 0.7 pt between the two baselines of a two-font row.
	baselineTolerance = 0.3
	// anchorSupport is the number of seeds that make a baseline anchored.
	anchorSupport = 5
	// offsetReach is how far below a glyph's bottom, in em, a baseline may lie
	// and still carry it; descenders reach about 0.3 em.
	offsetReach = 0.6
	// offsetSamples and offsetSpread decide when a shape's offset is known:
	// enough samples, agreeing to within this median absolute deviation.
	offsetSamples = 3
	offsetSpread  = 0.5 // points
	// fallbackWindow and fallbackReach bound where a glyph with no learned
	// offset may join a line: vertically from its bottom, horizontally from the
	// line's seeds and pass-1 glyphs, both in the line's em.
	fallbackWindow = 0.5
	fallbackReach  = 3
	// runGap splits a line into runs, the domain of word spacing. A run is not
	// a table cell: two cells nearer than this read as one.
	runGap = 1.5 // em
	// rehomeReach is how far, in em, a line's member may sit from a glyph and
	// still count as being beside it when re-homing. Measured in E4 and E6: it
	// shares runGap's value but not its meaning, and must be free to move when
	// column evidence replaces runGap.
	rehomeReach = 1.5 // em
	// gapBin is the histogram resolution for finding the word-space valley.
	gapBin = 0.02 // em
	// breakMargin is how far a gap must exceed its pair's fitted correction to
	// be a word break.
	breakMargin = 0.15 // em
	// unsplitWordGap is the word-break threshold, in em, of the degraded mode
	// when no split can be found at all; tuned on the measured document.
	unsplitWordGap = 0.26
)

// line is a baseline with the glyphs placed on it.
type line struct {
	y, em  float64
	seeds  []*outlineGlyph // an anchored line's seeds
	glyphs []*outlineGlyph
}

// members is everything on the line. Pass 2 appends to glyphs, so the copy
// this returns is a snapshot of the line as it stands.
func (l *line) members() []*outlineGlyph { return slices.Concat(l.seeds, l.glyphs) }

// run is a stretch of a line without a gap wider than runGap.
type run struct {
	line   *line
	em     float64
	glyphs []*outlineGlyph // left to right
	breaks []bool          // breaks[i]: a word ends after glyphs[i]
	gapv   []wordGap       // memoised gaps()
}

// wordGap is the gap between two adjacent glyphs of a run, in em.
type wordGap struct {
	left, right int // shapes
	v           float64
}

// gaps is read three times over the same run, so it is computed once.
func (r *run) gaps() []wordGap {
	if r.gapv == nil {
		r.gapv = make([]wordGap, 0, max(0, len(r.glyphs)-1))
		for i := 1; i < len(r.glyphs); i++ {
			a, b := r.glyphs[i-1], r.glyphs[i]
			r.gapv = append(r.gapv, wordGap{a.shape, b.shape, (b.x0 - a.x1) / r.em})
		}
	}
	return r.gapv
}

type assembly struct {
	runs  []*run
	spans [][]TextSpan // per page
}

// assemble places glyphs, which are labelled and in page and paint order, and
// fills in the report's Omitted, FallbackPlaced, TransferPlaced, Guessed and
// SpacingFallback.
func assemble(numPages int, glyphs []*outlineGlyph, report *recoveryReport) assembly {
	pages := make([][]*outlineGlyph, numPages)
	for _, g := range glyphs {
		pages[g.page] = append(pages[g.page], g)
	}
	anchors := make([][]*line, numPages)
	for p, gs := range pages {
		anchors[p] = anchoredLines(gs)
	}
	own := learnOffsets(pages, anchors)
	moved := transferOffsets(glyphs, own.offset)

	a := assembly{spans: make([][]TextSpan, numPages)}
	for p, gs := range pages {
		pl := place(gs, anchors[p], own, moved)
		report.FallbackPlaced = append(report.FallbackPlaced, occurrences(pl.fallback)...)
		report.TransferPlaced = append(report.TransferPlaced, occurrences(pl.transferred)...)
		report.Rehomed = append(report.Rehomed, occurrences(pl.rehomed)...)
		report.Omitted = append(report.Omitted, occurrences(pl.omitted)...)
		for _, l := range pl.lines {
			a.runs = append(a.runs, runsOf(l)...)
		}
	}

	var all []wordGap
	for _, r := range a.runs {
		all = append(all, r.gaps()...)
	}
	sp := fitSpacing(all)
	report.SpacingFallback = !sp.clear
	if sp.clear {
		for _, r := range a.runs {
			for _, g := range r.gaps() {
				r.breaks = append(r.breaks, sp.isBreak(g))
			}
		}
	} else {
		localBreaks(a.runs)
	}

	for _, r := range a.runs {
		p := r.glyphs[0].page
		for _, w := range splitWords(r) {
			a.spans[p] = append(a.spans[p], wordSpan(w, r, report))
		}
	}
	slices.SortFunc(report.Guessed, func(x, y guessedGlyph) int {
		return cmp.Or(x.Page-y.Page, x.Index-y.Index)
	})
	return a
}

func occurrences(gs []*outlineGlyph) []glyphRef {
	out := make([]glyphRef, len(gs))
	for i, g := range gs {
		out[i] = g.occurrence()
	}
	return out
}

func (g *outlineGlyph) isSeed() bool {
	return len(g.label) == 1 && strings.Contains(seedChars, g.label)
}

func (g *outlineGlyph) height() float64 { return g.y1 - g.y0 }

// emOf estimates the em of the text gs belong to from its tallest seed, or its
// tallest glyph when there is none, which overestimates for brackets and
// descenders.
func emOf(gs []*outlineGlyph) float64 {
	var seed, tallest float64
	for _, g := range gs {
		tallest = math.Max(tallest, g.height())
		if g.isSeed() {
			seed = math.Max(seed, g.height())
		}
	}
	if seed > 0 {
		return seed / capHeight
	}
	return tallest / capHeight
}

// hdist is the horizontal distance from g to the nearest of gs.
func hdist(g *outlineGlyph, gs []*outlineGlyph) float64 {
	d := math.Inf(1)
	for _, s := range gs {
		d = math.Min(d, math.Max(0, math.Max(s.x0-g.x1, g.x0-s.x1)))
	}
	return d
}

// anchoredLines chains seed bottoms into baselines, top of the page first,
// and keeps those with enough seeds.
func anchoredLines(gs []*outlineGlyph) []*line {
	var seeds []*outlineGlyph
	for _, g := range gs {
		if g.isSeed() {
			seeds = append(seeds, g)
		}
	}
	slices.SortStableFunc(seeds, func(a, b *outlineGlyph) int { return cmp.Compare(b.y0, a.y0) })
	var lines []*line
	for i := 0; i < len(seeds); {
		j := i + 1
		for j < len(seeds) && seeds[j-1].y0-seeds[j].y0 <= baselineTolerance {
			j++
		}
		if group := seeds[i:j]; len(group) >= anchorSupport {
			bottoms := make([]float64, len(group))
			for k, g := range group {
				bottoms[k] = g.y0
			}
			lines = append(lines, &line{y: median(bottoms), em: emOf(group), seeds: group})
		}
		i = j
	}
	return lines
}

// learned is what anchored lines teach: each shape's offset, and the seed
// shapes of the lines whose samples agree with it. One outline is one font at
// one size, so those seeds identify the text the shape was learned in.
type learned struct {
	offset map[int]float64
	taught map[int]map[int]bool
}

// learnOffsets learns each shape's offset from glyphs that sit unambiguously
// near an anchored line's seeds. Nothing placed later trains it, so one wrong
// attachment cannot spread through a shape to the whole document.
func learnOffsets(pages [][]*outlineGlyph, anchors [][]*line) learned {
	var heights []float64
	for _, gs := range pages {
		for _, g := range gs {
			if g.isSeed() {
				heights = append(heights, g.height())
			}
		}
	}
	if len(heights) == 0 {
		return learned{}
	}
	seedHeight := median(heights)

	type sample struct {
		v    float64
		line *line
	}
	samples := map[int][]sample{}
	for p, gs := range pages {
		for _, g := range gs {
			var best *line
			bestD, second := math.Inf(1), math.Inf(1)
			for _, a := range anchors[p] {
				if a.y > g.y1 || a.y < g.y0-offsetReach*a.em {
					continue
				}
				d := hdist(g, a.seeds)
				if d > a.em {
					continue
				}
				if d < bestD {
					second, best, bestD = bestD, a, d
				} else {
					second = math.Min(second, d)
				}
			}
			// In a two-font row, a glyph near seeds of both baselines says
			// nothing about its own offset.
			if best != nil && second > bestD+0.5*seedHeight {
				samples[g.shape] = append(samples[g.shape], sample{best.y - g.y0, best})
			}
		}
	}
	l := learned{offset: map[int]float64{}, taught: map[int]map[int]bool{}}
	for shape, ss := range samples {
		if len(ss) < offsetSamples {
			continue
		}
		v := make([]float64, len(ss))
		for i, s := range ss {
			v[i] = s.v
		}
		m := median(v)
		dev := make([]float64, len(v))
		for i, x := range v {
			dev[i] = math.Abs(x - m)
		}
		if median(dev) > offsetSpread {
			continue
		}
		l.offset[shape] = m
		l.taught[shape] = map[int]bool{}
		seen := map[*line]bool{}
		for _, s := range ss {
			if math.Abs(s.v-m) <= offsetSpread && !seen[s.line] {
				seen[s.line] = true
				for _, seed := range s.line.seeds {
					l.taught[shape][seed.shape] = true
				}
			}
		}
	}
	return l
}

// transferOffsets gives a shape with no offset of its own the offset of the
// shapes that are its outline at another size, scaled by the ratio of their
// sizes. Two outlines match when, scaled to the smaller of their sizes, every
// coordinate agrees within shapeTolerance. Only offsets learned on anchored
// lines are given, so a transfer never seeds another, and a shape whose
// donors disagree by more than offsetSpread gets none. Bare rectangles take
// no part: one says nothing but its aspect ratio, and a hyphen matches a
// hyphen of another font that sits at another height.
//
// A match says the glyphs sit alike, not that they are one character: O and
// o can be one outline at two sizes. Labels are left as the labeller gave
// them.
func transferOffsets(glyphs []*outlineGlyph, own map[int]float64) map[int]transfer {
	type outline struct {
		key    shapeKey
		coords []float64
		size   float64
	}
	outlines := map[int]outline{}
	for _, g := range glyphs {
		if _, ok := outlines[g.shape]; !ok && !g.rect {
			key, coords := g.fill.shape()
			outlines[g.shape] = outline{key, coords, math.Max(g.x1-g.x0, g.y1-g.y0)}
		}
	}
	// Only outlines sharing a key can match, so each is compared with its
	// bucket rather than with every other outline.
	byKey := map[shapeKey][]int{}
	for s, o := range outlines {
		byKey[o.key] = append(byKey[o.key], s)
	}
	same := func(a, b outline) bool {
		if a.key != b.key {
			return false
		}
		small := math.Min(a.size, b.size)
		for k := range a.coords {
			if math.Abs(a.coords[k]*small/a.size-b.coords[k]*small/b.size) > shapeTolerance {
				return false
			}
		}
		return true
	}
	moved := map[int]transfer{}
	for s, o := range outlines {
		if _, ok := own[s]; ok {
			continue
		}
		var offs []float64
		scale := 1.0
		for _, d := range byKey[o.key] {
			donor := outlines[d]
			if off, ok := own[d]; ok && same(donor, o) {
				offs = append(offs, off*o.size/donor.size)
				scale = math.Max(scale, o.size/donor.size)
			}
		}
		if len(offs) > 0 && slices.Max(offs)-slices.Min(offs) <= offsetSpread {
			moved[s] = transfer{median(offs), baselineTolerance * scale}
		}
	}
	return moved
}

// transfer is an offset carried from another size, and the tolerance it is
// known to: an offset learned to within baselineTolerance is scaled with it.
type transfer struct{ off, tolerance float64 }

// placement is one page's lines, anchored first, then inferred in order of
// creation, and the glyphs placed by inference or not at all.
type placement struct {
	lines                                   []*line
	fallback, transferred, rehomed, omitted []*outlineGlyph
}

// place puts one page's glyphs on lines in two deterministic passes.
//
// Pass 1 places glyphs whose shape has an offset, learned or transferred, on
// an anchored line when one lies within tolerance of the predicted baseline,
// else on an inferred line; then dissolvePhantoms re-homes what it can of
// the inferred lines only one shape predicts. Horizontal proximity only
// breaks ties between anchored lines; it never excludes one. Pass 2 places
// the rest against the lines as pass 1 left them, so fallback glyphs never
// attract each other and their order does not matter. Re-homing then runs a
// second time: a glyph whose right-hand neighbour has no offset of its own
// gains members on both sides only once pass 2 has placed that neighbour.
func place(gs []*outlineGlyph, anchors []*line, own learned, moved map[int]transfer) (pl placement) {
	var pending []*outlineGlyph
	var inferredLines []*line
	for _, g := range gs {
		off, ok := own.offset[g.shape]
		tol := baselineTolerance
		if t, moves := moved[g.shape]; !ok && moves {
			off, tol, ok = t.off, t.tolerance, true
			pl.transferred = append(pl.transferred, g)
		}
		if !ok {
			pending = append(pending, g)
			continue
		}
		yb := g.y0 + off
		var best *line
		bestD := math.Inf(1)
		for _, a := range anchors {
			if math.Abs(a.y-yb) <= tol {
				if d := hdist(g, a.seeds); best == nil || d < bestD {
					best, bestD = a, d
				}
			}
		}
		if best == nil {
			for _, l := range inferredLines {
				if math.Abs(l.y-yb) <= tol {
					best = l
					break
				}
			}
		}
		if best == nil {
			best = &line{y: yb}
			inferredLines = append(inferredLines, best)
		}
		best.glyphs = append(best.glyphs, g)
	}
	inferredLines = dissolvePhantoms(inferredLines, anchors, own.taught, &pl, &pending)

	// Snapshot the targets. Their member slices keep their pass-1 length while
	// pass 2 appends to the lines.
	type target struct {
		line    *line
		members []*outlineGlyph
	}
	for _, l := range inferredLines {
		l.em = emOf(l.glyphs)
	}
	var targets []target
	for _, l := range slices.Concat(anchors, inferredLines) {
		targets = append(targets, target{l, l.members()})
	}
	type candidate struct {
		line *line
		d, h float64
	}
	for _, g := range pending {
		var near []candidate
		closest := math.Inf(1)
		for _, t := range targets {
			d := math.Abs(t.line.y - g.y0)
			if d > fallbackWindow*t.line.em {
				continue
			}
			if h := hdist(g, t.members); h <= fallbackReach*t.line.em {
				near = append(near, candidate{t.line, d, h})
				closest = math.Min(closest, d)
			}
		}
		var best *line
		bestH := math.Inf(1)
		for _, c := range near {
			if c.d <= closest+baselineTolerance && c.h < bestH {
				best, bestH = c.line, c.h
			}
		}
		if best == nil {
			pl.omitted = append(pl.omitted, g)
			continue
		}
		best.glyphs = append(best.glyphs, g)
		pl.fallback = append(pl.fallback, g)
	}

	inferredLines = dissolvePhantoms(inferredLines, anchors, own.taught, &pl, nil)

	for _, l := range slices.Concat(anchors, inferredLines) {
		if len(l.glyphs) > 0 {
			pl.lines = append(pl.lines, l)
		}
	}
	return pl
}

// dissolvePhantoms re-homes glyphs of inferred lines that sit inside an
// anchored line's text. A comma's offset puts an apostrophe drawn with the
// same outline on a line of its own, inside the body of the line it belongs
// to. A glyph joins an anchored line when:
//
//   - its ink overlaps the line's body, from the baseline to cap height;
//   - its shape learned its offset in the same text, on lines sharing a seed
//     shape with this one, which rules out transferred offsets, a smaller
//     font's cell in a mixed row, and a superscript;
//   - the line has members on both sides of it within runGap, which rules
//     out a cell set beside the line rather than within it.
//
// A glyph inside two lines is ambiguous and goes to pass 2; with pending nil,
// after pass 2, it stays where it is.
func dissolvePhantoms(inferred, anchors []*line, taught map[int]map[int]bool, pl *placement, pending *[]*outlineGlyph) []*line {
	members := map[*line][]*outlineGlyph{}
	for _, a := range anchors {
		members[a] = a.members()
	}
	inside := func(g *outlineGlyph, a *line) bool {
		reach := rehomeReach * a.em
		return g.y1 > a.y && g.y0 < a.y+capHeight*a.em &&
			slices.ContainsFunc(a.seeds, func(s *outlineGlyph) bool { return taught[g.shape][s.shape] }) &&
			slices.ContainsFunc(members[a], func(m *outlineGlyph) bool { return m.x0 < g.x0 && g.x0-m.x1 <= reach }) &&
			slices.ContainsFunc(members[a], func(m *outlineGlyph) bool { return m.x1 > g.x1 && m.x0-g.x1 <= reach })
	}

	var kept []*line
	for _, l := range inferred {
		var stay []*outlineGlyph
		for _, g := range l.glyphs {
			var homes []*line
			for _, a := range anchors {
				if inside(g, a) {
					homes = append(homes, a)
				}
			}
			switch len(homes) {
			case 0:
				stay = append(stay, g)
			case 1:
				homes[0].glyphs = append(homes[0].glyphs, g)
				pl.rehomed = append(pl.rehomed, g)
			default:
				if pending == nil {
					stay = append(stay, g)
				} else {
					*pending = append(*pending, g)
				}
			}
		}
		if l.glyphs = stay; len(stay) > 0 {
			kept = append(kept, l)
		}
	}
	return kept
}

// runsOf splits a line at gaps wider than runGap.
func runsOf(l *line) []*run {
	gs := slices.Clone(l.glyphs)
	slices.SortStableFunc(gs, func(a, b *outlineGlyph) int { return cmp.Compare(a.x0, b.x0) })
	em := emOf(gs)
	var runs []*run
	start := 0
	for i := 1; i <= len(gs); i++ {
		if i == len(gs) || gs[i].x0-gs[i-1].x1 > runGap*em {
			runs = append(runs, &run{line: l, em: em, glyphs: gs[start:i]})
			start = i
		}
	}
	return runs
}

// spacing decides word breaks from per-shape corrections fitted below the
// word-space valley. The corrections are effective, not font sidebearings:
// only the sums r(A) + l(B) are defined, and tracking and kerning fold in.
type spacing struct {
	valley float64 // em
	clear  bool
	r, l   map[int]float64 // corrections of shapes with evidence, as left and right glyph
	mr, ml float64         // their medians, for a side without evidence
}

// fitSpacing finds the valley between intra-word gaps and word spaces, then
// fits corrections by median polish to the gaps below it.
func fitSpacing(gaps []wordGap) spacing {
	var pool []float64
	for _, g := range gaps {
		if g.v < 1 {
			pool = append(pool, g.v)
		}
	}
	var sp spacing
	if sp.valley, sp.clear = findValley(pool); !sp.clear {
		return sp
	}

	var ex []wordGap
	nr, nl := map[int]int{}, map[int]int{}
	for _, g := range gaps {
		if g.v < sp.valley {
			ex = append(ex, g)
			nr[g.left]++
			nl[g.right]++
		}
	}
	var r map[int]float64
	l := map[int]float64{}
	for range 10 {
		r = polish(ex, func(g wordGap) (int, float64) { return g.left, g.v - l[g.right] })
		l = polish(ex, func(g wordGap) (int, float64) { return g.right, g.v - r[g.left] })
	}
	sp.r, sp.l = evidenced(r, nr), evidenced(l, nl)
	if len(sp.r) == 0 || len(sp.l) == 0 {
		sp.r, sp.l = nil, nil
		return sp
	}
	sp.mr, sp.ml = median(values(sp.r)), median(values(sp.l))
	return sp
}

// polish is one half-step of median polish: each shape's median residual.
func polish(ex []wordGap, residual func(wordGap) (int, float64)) map[int]float64 {
	res := map[int][]float64{}
	for _, g := range ex {
		k, v := residual(g)
		res[k] = append(res[k], v)
	}
	out := make(map[int]float64, len(res))
	for k, v := range res {
		out[k] = median(v)
	}
	return out
}

// evidenced keeps the corrections of shapes seen in at least three examples.
func evidenced(c map[int]float64, n map[int]int) map[int]float64 {
	out := map[int]float64{}
	for k, v := range c {
		if n[k] >= 3 {
			out[k] = v
		}
	}
	return out
}

func values(m map[int]float64) []float64 {
	v := make([]float64, 0, len(m))
	for _, x := range m {
		v = append(v, x)
	}
	return v
}

// isBreak uses whichever side has evidence; only when neither does is the gap
// compared with the valley directly.
func (sp spacing) isBreak(g wordGap) bool {
	r, okR := sp.r[g.left]
	l, okL := sp.l[g.right]
	if !okR && !okL {
		return g.v > sp.valley
	}
	if !okR {
		r = sp.mr
	}
	if !okL {
		l = sp.ml
	}
	return g.v-(r+l) > breakMargin
}

// findValley returns the centre of the least-populated gapBin between the two
// Otsu class means, ties going to the higher bin. The valley is clear when it
// holds at most a fifth of the upper mode's bin and at least 30 gaps lie above
// it; both criteria are provisional.
func findValley(pool []float64) (centre float64, clear bool) {
	if len(pool) < 2 {
		return 0, false
	}
	v := slices.Clone(pool)
	slices.Sort(v)
	_, m0, m1 := otsu(v)
	bins := map[int]int{}
	for _, x := range v {
		bins[int(x/gapBin)]++
	}
	lo, hi := int(m0/gapBin)+1, int(m1/gapBin)
	if lo >= hi {
		return 0, false
	}
	vb := lo
	for b := lo; b < hi; b++ {
		if bins[b] <= bins[vb] {
			vb = b
		}
	}
	upperMode, upper := 0, 0
	for b, n := range bins {
		if b > vb {
			upperMode = max(upperMode, n)
		}
	}
	for _, x := range v {
		if x > float64(vb+1)*gapBin {
			upper++
		}
	}
	return (float64(vb) + 0.5) * gapBin, float64(bins[vb]) <= 0.2*float64(upperMode) && upper >= 30
}

// otsu splits sorted values into v[:k] and v[k:] where the between-class
// variance is largest, and returns the class means.
func otsu(v []float64) (k int, m0, m1 float64) {
	var total, sum float64
	for _, x := range v {
		total += x
	}
	best := -1.0
	for i := 1; i < len(v); i++ {
		sum += v[i-1]
		n0, n1 := float64(i), float64(len(v)-i)
		a, b := sum/n0, (total-sum)/n1
		if s := n0 * n1 * (b - a) * (b - a); s > best {
			best, k, m0, m1 = s, i, a, b
		}
	}
	return k, m0, m1
}

// localBreaks is the degraded mode, used when there is no clear valley: each
// run is split in two by its own gaps, or by those of its page and size when
// it is too short or too uniform to split.
func localBreaks(runs []*run) {
	type group struct {
		page int
		size float64
	}
	groupOf := func(r *run) group { return group{r.glyphs[0].page, math.RoundToEven(r.em*2) / 2} }
	gaps := make([][]float64, len(runs))
	pooled := map[group][]float64{}
	for i, r := range runs {
		for _, g := range r.gaps() {
			gaps[i] = append(gaps[i], g.v)
		}
		g := groupOf(r)
		pooled[g] = append(pooled[g], gaps[i]...)
	}
	thresholds := map[group]float64{}
	for k, v := range pooled {
		t, ok := splitGaps(v)
		if !ok {
			t = unsplitWordGap
		}
		thresholds[k] = t
	}
	for i, r := range runs {
		t, ok := splitGaps(gaps[i])
		if !ok {
			t = thresholds[groupOf(r)]
		}
		for _, x := range gaps[i] {
			r.breaks = append(r.breaks, x > t)
		}
	}
}

// splitGaps finds a threshold between two well-separated classes of gaps.
func splitGaps(gaps []float64) (float64, bool) {
	if len(gaps) < 4 {
		return 0, false
	}
	v := slices.Clone(gaps)
	slices.Sort(v)
	k, m0, m1 := otsu(v)
	if v[k]-v[k-1] >= 0.05 && m1-m0 >= 0.1 && m1 >= 2*m0 {
		return (v[k-1] + v[k]) / 2, true
	}
	return 0, false
}

func splitWords(r *run) [][]*outlineGlyph {
	var out [][]*outlineGlyph
	start := 0
	for i := range r.glyphs {
		if i == len(r.glyphs)-1 || r.breaks[i] {
			out = append(out, r.glyphs[start:i+1])
			start = i + 1
		}
	}
	return out
}

// lookAlikes are labels a labeller cannot tell apart when their shape is a
// bare rectangle, as it is for all three in many sans-serif faces.
var lookAlikes = []string{"l", "I", "|"}

// wordSpan builds one word's span, deciding look-alike characters by the
// strongest evidence available. Geometry resolves them: a comma sitting well
// above the baseline is an apostrophe. A bare rectangle is only guessed, and
// each guess is reported with what decided it.
func wordSpan(w []*outlineGlyph, r *run, report *recoveryReport) TextSpan {
	var b strings.Builder
	x0, x1 := math.Inf(1), math.Inf(-1)
	for i, g := range w {
		x0, x1 = math.Min(x0, g.x0), math.Max(x1, g.x1)
		label := g.label
		switch {
		case label == "," && g.y0-r.line.y > 0.25*r.em:
			label = "'"
		case g.isLookAlike():
			var by labelEvidence
			label, by = lookAlike(w, i)
			alt := slices.DeleteFunc(slices.Clone(lookAlikes), func(a string) bool { return a == label })
			report.Guessed = append(report.Guessed, guessedGlyph{g.occurrence(), label, alt, by})
		}
		b.WriteString(label)
	}
	return TextSpan{X: x0, Y: r.line.y, EndX: x1, FontSize: r.em, Text: b.String()}
}

// lookAlike decides the rectangle w[i] from the case of the letters around it,
// up to the nearest non-letter, other rectangles not counting. A capital after
// the first letter and no lowercase make it I, as in INVOICE; a first capital
// and only lowercase after make it l, as in Please. Anything else is left to
// its neighbours, I beside a capital and otherwise l: a lowercase word, which
// may be an identifier (myItem); mixed case (OpenAI); a rectangle that begins
// its word (Ideal, lever) or stands alone.
func lookAlike(w []*outlineGlyph, i int) (string, labelEvidence) {
	isLetter := func(g *outlineGlyph) bool {
		return g.isLookAlike() || strings.ToLower(g.label) != strings.ToUpper(g.label)
	}
	lo, hi := i, i
	for lo > 0 && isLetter(w[lo-1]) {
		lo--
	}
	for hi < len(w)-1 && isLetter(w[hi+1]) {
		hi++
	}
	var upper, lower bool // besides the first letter
	for j, g := range w[lo : hi+1] {
		switch {
		case g.isLookAlike():
		case isCapital(g):
			upper = upper || j > 0
		case strings.ToUpper(g.label) != g.label:
			lower = true
		}
	}
	switch {
	case upper && !lower:
		return "I", evidenceByCase
	case lower && !upper && isCapital(w[lo]):
		return "l", evidenceByCase
	case (i > 0 && isCapital(w[i-1])) || (i < len(w)-1 && isCapital(w[i+1])):
		return "I", evidenceByNeighbour
	}
	return "l", evidenceByNeighbour
}

func isCapital(g *outlineGlyph) bool {
	c, n := utf8.DecodeRuneInString(g.label)
	return n == len(g.label) && unicode.IsUpper(c) && !g.isLookAlike()
}

func (g *outlineGlyph) isLookAlike() bool {
	return g.rect && slices.Contains(lookAlikes, g.label)
}

func median(v []float64) float64 {
	s := slices.Clone(v)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
