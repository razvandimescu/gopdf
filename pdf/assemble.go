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

// run is a stretch of a line without a gap wider than runGap.
type run struct {
	line   *line
	em     float64
	glyphs []*outlineGlyph // left to right
	breaks []bool          // breaks[i]: a word ends after glyphs[i]
}

// wordGap is the gap between two adjacent glyphs of a run, in em.
type wordGap struct {
	left, right int // shapes
	v           float64
}

func (r *run) gaps() []wordGap {
	var gs []wordGap
	for i := 1; i < len(r.glyphs); i++ {
		a, b := r.glyphs[i-1], r.glyphs[i]
		gs = append(gs, wordGap{a.shape, b.shape, (b.x0 - a.x1) / r.em})
	}
	return gs
}

type assembly struct {
	runs  []*run
	spans [][]TextSpan // per page
}

// assemble places glyphs, which are labelled and in page and paint order, and
// fills in the report's Omitted, FallbackPlaced, Guessed and SpacingFallback.
func assemble(numPages int, glyphs []*outlineGlyph, report *RecoveryReport) assembly {
	pages := make([][]*outlineGlyph, numPages)
	for _, g := range glyphs {
		pages[g.page] = append(pages[g.page], g)
	}
	anchors := make([][]*line, numPages)
	for p, gs := range pages {
		anchors[p] = anchoredLines(gs)
	}
	offsets := learnOffsets(pages, anchors)

	a := assembly{spans: make([][]TextSpan, numPages)}
	for p, gs := range pages {
		lines, fallback, omitted := place(gs, anchors[p], offsets)
		report.FallbackPlaced = append(report.FallbackPlaced, occurrences(fallback)...)
		report.Omitted = append(report.Omitted, occurrences(omitted)...)
		for _, l := range lines {
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
	slices.SortFunc(report.Guessed, func(x, y GuessedGlyph) int {
		return cmp.Or(x.Page-y.Page, x.Index-y.Index)
	})
	return a
}

func occurrences(gs []*outlineGlyph) []Occurrence {
	out := make([]Occurrence, len(gs))
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

// learnOffsets learns each shape's offset from glyphs that sit unambiguously
// near an anchored line's seeds. Nothing placed later trains it, so one wrong
// attachment cannot spread through a shape to the whole document.
func learnOffsets(pages [][]*outlineGlyph, anchors [][]*line) map[int]float64 {
	var heights []float64
	for _, gs := range pages {
		for _, g := range gs {
			if g.isSeed() {
				heights = append(heights, g.height())
			}
		}
	}
	if len(heights) == 0 {
		return nil
	}
	seedHeight := median(heights)

	samples := map[int][]float64{}
	for p, gs := range pages {
		for _, g := range gs {
			best, bestD, second := -1, math.Inf(1), math.Inf(1)
			for i, a := range anchors[p] {
				if a.y > g.y1 || a.y < g.y0-offsetReach*a.em {
					continue
				}
				d := hdist(g, a.seeds)
				if d > a.em {
					continue
				}
				if d < bestD {
					second, best, bestD = bestD, i, d
				} else {
					second = math.Min(second, d)
				}
			}
			// In a two-font row, a glyph near seeds of both baselines says
			// nothing about its own offset.
			if best >= 0 && second > bestD+0.5*seedHeight {
				samples[g.shape] = append(samples[g.shape], anchors[p][best].y-g.y0)
			}
		}
	}
	offsets := map[int]float64{}
	for shape, v := range samples {
		if len(v) < offsetSamples {
			continue
		}
		m := median(v)
		dev := make([]float64, len(v))
		for i, x := range v {
			dev[i] = math.Abs(x - m)
		}
		if median(dev) <= offsetSpread {
			offsets[shape] = m
		}
	}
	return offsets
}

// place puts one page's glyphs on lines in two deterministic passes and
// returns the lines, anchored first, then inferred in order of creation.
//
// Pass 1 places glyphs whose shape has an offset, on an anchored line when one
// lies within tolerance of the predicted baseline, else on an inferred line.
// Horizontal proximity only breaks ties between anchored lines; it never
// excludes one. Pass 2 places the rest against the lines as pass 1 left them,
// so fallback glyphs never attract each other and their order does not matter.
func place(gs []*outlineGlyph, anchors []*line, offsets map[int]float64) (lines []*line, fallback, omitted []*outlineGlyph) {
	var pending []*outlineGlyph
	var inferredLines []*line
	for _, g := range gs {
		off, ok := offsets[g.shape]
		if !ok {
			pending = append(pending, g)
			continue
		}
		yb := g.y0 + off
		var best *line
		bestD := math.Inf(1)
		for _, a := range anchors {
			if math.Abs(a.y-yb) <= baselineTolerance {
				if d := hdist(g, a.seeds); best == nil || d < bestD {
					best, bestD = a, d
				}
			}
		}
		if best == nil {
			for _, l := range inferredLines {
				if math.Abs(l.y-yb) <= baselineTolerance {
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

	// Snapshot the targets. Their member slices keep their pass-1 length while
	// pass 2 appends to the lines.
	type target struct {
		line    *line
		members []*outlineGlyph
	}
	var targets []target
	for _, a := range anchors {
		targets = append(targets, target{a, slices.Concat(a.seeds, a.glyphs)})
	}
	for _, l := range inferredLines {
		l.em = emOf(l.glyphs)
		targets = append(targets, target{l, l.glyphs})
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
			omitted = append(omitted, g)
			continue
		}
		best.glyphs = append(best.glyphs, g)
		fallback = append(fallback, g)
	}

	for _, l := range slices.Concat(anchors, inferredLines) {
		if len(l.glyphs) > 0 {
			lines = append(lines, l)
		}
	}
	return lines, fallback, omitted
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
		pooled[groupOf(r)] = append(pooled[groupOf(r)], gaps[i]...)
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
// above the baseline is an apostrophe. Context only guesses, and each guess is
// reported: a rectangle beside a capital is I, otherwise l.
func wordSpan(w []*outlineGlyph, r *run, report *RecoveryReport) TextSpan {
	var b strings.Builder
	x0, x1 := math.Inf(1), math.Inf(-1)
	for i, g := range w {
		x0, x1 = math.Min(x0, g.x0), math.Max(x1, g.x1)
		label := g.label
		switch {
		case label == "," && g.y0-r.line.y > 0.25*r.em:
			label = "'"
		case g.isLookAlike():
			label = "l"
			if (i > 0 && isCapital(w[i-1])) || (i < len(w)-1 && isCapital(w[i+1])) {
				label = "I"
			}
			var alt []string
			for _, a := range lookAlikes {
				if a != label {
					alt = append(alt, a)
				}
			}
			report.Guessed = append(report.Guessed, GuessedGlyph{g.occurrence(), label, alt})
		}
		b.WriteString(label)
	}
	return TextSpan{X: x0, Y: r.line.y, EndX: x1, FontSize: r.em, Text: b.String()}
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
