package pdf

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// seedRow is five H seeds with their bottoms at y, from x = 0.
func seedRow(y float64) []*outlineGlyph {
	var gs []*outlineGlyph
	for i := range 5 {
		x := 6 * float64(i)
		gs = append(gs, &outlineGlyph{x0: x, y0: y, x1: x + 4.6, y1: y + 7, shape: 0, label: "H"})
	}
	return gs
}

func TestOffsetsNeedAnUnambiguousLine(t *testing.T) {
	round := func() []*outlineGlyph {
		var gs []*outlineGlyph
		for range 3 {
			gs = append(gs, &outlineGlyph{x0: 30, y0: 99.9, x1: 34, y1: 105, shape: 1, label: "o"})
		}
		return gs
	}
	one := append(seedRow(100), round()...)
	if off, ok := learnOffsets([][]*outlineGlyph{one}, [][]*line{anchoredLines(one)}).offset[1]; !approx(off, 0.1) || !ok {
		t.Errorf("beside one baseline: offset %v, %v; want 0.1", off, ok)
	}
	// A two-font row: a second baseline 0.8 pt away, its seeds as near.
	two := append(append(seedRow(100), seedRow(100.8)...), round()...)
	anchors := anchoredLines(two)
	if len(anchors) != 2 {
		t.Fatalf("got %d anchored lines, want 2", len(anchors))
	}
	if off, ok := learnOffsets([][]*outlineGlyph{two}, [][]*line{anchors}).offset[1]; ok {
		t.Errorf("between two baselines: learned offset %v, want none", off)
	}
}

// A fallback glyph may join a line within reach of any glyph known to be on
// it: its seeds as well as the glyphs pass 1 placed. Measuring from the
// placed glyphs alone lost the end of a line whose placed glyphs clustered at
// its start.
func TestFallbackReachesFromSeedsAndPlacedGlyphs(t *testing.T) {
	var gs []*outlineGlyph
	for _, x := range []float64{0, 20, 40, 60, 80} { // seeds with no offset of their own
		gs = append(gs, &outlineGlyph{x0: x, y0: 100, x1: x + 4.6, y1: 107, shape: 0, label: "H"})
	}
	placed := func(x float64) *outlineGlyph {
		return &outlineGlyph{x0: x, y0: 99.9, x1: x + 4, y1: 105, shape: 1, label: "o"}
	}
	nearSeed := &outlineGlyph{index: 1, x0: 110, y0: 100, x1: 114, y1: 105, shape: 2, label: "e"}
	nearPlaced := &outlineGlyph{index: 2, x0: 170, y0: 100, x1: 174, y1: 105, shape: 2, label: "e"}
	gs = append(gs, placed(0), placed(150), nearSeed, nearPlaced)

	anchors := anchoredLines(gs)
	if len(anchors) != 1 {
		t.Fatalf("got %d anchored lines, want 1", len(anchors))
	}
	// 3 em is about 29 pt: nearSeed is 25 pt from a seed and 106 pt from the
	// first placed glyph; nearPlaced is 16 pt from the second and 85 pt from
	// any seed.
	pl := place(gs, anchors, learned{offset: map[int]float64{1: 0.1}}, nil)
	if len(pl.omitted) != 0 || len(pl.lines) != 1 {
		t.Fatalf("got %d lines and omitted %v; want everything on the one line", len(pl.lines), pl.omitted)
	}
	for _, g := range []*outlineGlyph{nearSeed, nearPlaced} {
		if !slices.Contains(pl.lines[0].glyphs, g) {
			t.Errorf("glyph at x %g is not on the line", g.x0)
		}
	}
}

// scaledGlyph draws c's synthetic outline k times its size, with its baseline
// at (x, y).
func scaledGlyph(c rune, k, x, y float64) string {
	return fmt.Sprintf("q %g 0 0 %g %g %g cm %s Q", k, k, x, y, synthGlyph(c, 0, 0, false))
}

func TestTransferOffsets(t *testing.T) {
	// p at 1, 2 and 3 times its size, e, another outline, at twice, and a bare
	// rectangle at two sizes.
	fills := fillsOf(t, strings.Join([]string{
		scaledGlyph('p', 1, 100, 100), scaledGlyph('p', 2, 200, 100),
		scaledGlyph('p', 3, 300, 100), scaledGlyph('e', 2, 400, 100),
		"500 102.5 2.4 0.8 re f", "520 105 4.8 1.6 re f",
	}, "\n"))
	var gs []*outlineGlyph
	for i, f := range fills {
		g := &outlineGlyph{shape: i, fill: f, rect: f.isRect()}
		g.x0, g.y0, g.x1, g.y1 = f.bounds()
		gs = append(gs, g)
	}
	// An offset known to baselineTolerance at the donor's size is known to
	// that times the size ratio at the recipient's.
	cases := []struct {
		name     string
		own      map[int]float64
		want     map[int]transfer
		abstains []int
	}{
		{"scaled by the size ratio", map[int]float64{0: 2.1},
			map[int]transfer{1: {4.2, 2 * baselineTolerance}, 2: {6.3, 3 * baselineTolerance}}, []int{3, 5}},
		{"rectangles take no part", map[int]float64{4: -2.5}, nil, []int{5}},
		{"agreeing donors", map[int]float64{0: 2.1, 2: 6.3}, map[int]transfer{1: {4.2, 2 * baselineTolerance}}, nil},
		// At twice the size the donors say 4.2 and 2: no offset is better than
		// either.
		{"conflicting donors abstain", map[int]float64{0: 2.1, 2: 3}, nil, []int{1}},
		{"another outline donates nothing", map[int]float64{3: 0.12}, nil, []int{0, 1, 2}},
	}
	for _, c := range cases {
		got := transferOffsets(gs, c.own)
		if len(got) != len(c.want) {
			t.Errorf("%s: transferred %v, want %v", c.name, got, c.want)
		}
		for s, want := range c.want {
			if !approx(got[s].off, want.off) || !approx(got[s].tolerance, want.tolerance) {
				t.Errorf("%s: shape %d got %+v, want %+v", c.name, s, got[s], want)
			}
		}
		for _, s := range c.abstains {
			if off, ok := got[s]; ok {
				t.Errorf("%s: shape %d got offset %v, want none", c.name, s, off)
			}
		}
	}
}

// A transferred offset is matched to a baseline within the tolerance it was
// carried with. 0.4 pt is outside baselineTolerance but inside twice it.
func TestTransferredOffsetsKeepTheirTolerance(t *testing.T) {
	g := &outlineGlyph{index: 1, x0: 40, y0: 99.5, x1: 44, y1: 105, shape: 5, label: "o"}
	gs := append(seedRow(100), g)
	anchors := anchoredLines(gs)
	for _, c := range []struct {
		tolerance float64
		joins     bool
	}{{2 * baselineTolerance, true}, {baselineTolerance, false}} {
		pl := place(gs, anchors, learned{}, map[int]transfer{5: {0.1, c.tolerance}})
		if got := slices.Contains(anchors[0].glyphs, g); got != c.joins || len(pl.transferred) != 1 {
			t.Errorf("tolerance %v: joined the anchored line %v, want %v; transferred %d, want 1",
				c.tolerance, got, c.joins, len(pl.transferred))
		}
		anchors[0].glyphs = nil
	}
}

// histogram returns gaps at the centres of 0.02 em bins, counts[b] in bin b.
func histogram(counts map[int]int) []float64 {
	var v []float64
	for b, n := range counts {
		for range n {
			v = append(v, (float64(b)+0.5)*gapBin)
		}
	}
	return v
}

func TestFindValley(t *testing.T) {
	// Intra-word gaps peak near 0.1 em and word spaces at 0.38 em, as on the
	// measured document; between them, bin 12 holds one gap.
	counts := map[int]int{2: 50, 3: 50, 4: 50, 5: 50, 6: 50, 7: 5, 8: 4, 9: 3, 10: 2, 11: 2, 12: 1,
		13: 2, 14: 3, 15: 5, 16: 10, 17: 20, 18: 30, 19: 40, 20: 30, 21: 20}
	if centre, clear := findValley(histogram(counts)); !approx(centre, 0.25) || !clear {
		t.Errorf("valley %v, clear %v; want 0.25, clear", centre, clear)
	}

	counts[12] = 9 // more than a fifth of the word-space mode
	for b := 7; b <= 16; b++ {
		counts[b] = max(counts[b], 9)
	}
	if _, clear := findValley(histogram(counts)); clear {
		t.Error("a crowded valley was clear")
	}

	// With nothing between the modes, the valley is the empty bin nearest the
	// word spaces; it needs 30 of them above it to be clear.
	sparse := map[int]int{5: 100, 20: 40}
	if centre, clear := findValley(histogram(sparse)); !approx(centre, 0.39) || !clear {
		t.Errorf("empty between: valley %v, clear %v; want 0.39, clear", centre, clear)
	}
	sparse[20] = 20
	if _, clear := findValley(histogram(sparse)); clear {
		t.Error("20 word spaces made a clear valley")
	}
}

// A wide-sidebearing glyph such as 1 leaves wide gaps inside words. Its own
// correction decides them even beside a shape too rare to have one; the
// valley alone would call the first gap a word break.
func TestSpacingUsesOneSidedEvidence(t *testing.T) {
	sp := spacing{valley: 0.25, clear: true, r: map[int]float64{1: 0.2}, l: map[int]float64{2: 0}, mr: 0.05, ml: 0}
	cases := []struct {
		g         wordGap
		wantBreak bool
	}{
		{wordGap{1, 99, 0.3}, false}, // 0.3 - (0.2 + 0) is inside the margin
		{wordGap{1, 99, 0.4}, true},
		{wordGap{98, 99, 0.3}, true}, // no evidence on either side: the valley decides
		{wordGap{98, 2, 0.1}, false}, // 0.1 - (0.05 + 0)
	}
	for _, c := range cases {
		if got := sp.isBreak(c.g); got != c.wantBreak {
			t.Errorf("%+v: break %v, want %v", c.g, got, c.wantBreak)
		}
	}
}

// One rectangle stands for l and I; the letters around it decide, or its
// neighbours guess. The second list is the gate: words whose case must not
// decide, whatever the guess makes of them.
func TestLookAlikesByCase(t *testing.T) {
	decide := func(word string) (string, []Evidence) {
		var w []*outlineGlyph
		for _, c := range word {
			g := &outlineGlyph{label: string(c)}
			if c == 'l' || c == 'I' {
				g.label, g.rect = "l", true // the labeller's one name for the outline
			}
			w = append(w, g)
		}
		var b strings.Builder
		var by []Evidence
		for i, g := range w {
			label := g.label
			if g.isLookAlike() {
				var e Evidence
				label, e = lookAlike(w, i)
				by = append(by, e)
			}
			b.WriteString(label)
		}
		return b.String(), by
	}
	for _, word := range []string{"Please", "Old", "Detalii", "INVOICE", "BOLI", "IOM", "IL20", "RO00INGB", "LIST-Price"} {
		if got, by := decide(word); got != word || slices.Contains(by, ByNeighbour) {
			t.Errorf("%s: got %s by %v, want each rectangle decided by case", word, got, by)
		}
	}
	for _, word := range []string{"OpenAI", "myItem", "getItemList", "InfoDesk", "PowerShell", "I", "All", "Ideal", "Itemised", "lever", "value"} {
		if got, by := decide(word); slices.Contains(by, ByCase) {
			t.Errorf("%s: got %s by %v; its case must not decide it", word, got, by)
		}
	}
}
