package pdf

import "testing"

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
	if off, ok := learnOffsets([][]*outlineGlyph{one}, [][]*line{anchoredLines(one)})[1]; !approx(off, 0.1) || !ok {
		t.Errorf("beside one baseline: offset %v, %v; want 0.1", off, ok)
	}
	// A two-font row: a second baseline 0.8 pt away, its seeds as near.
	two := append(append(seedRow(100), seedRow(100.8)...), round()...)
	anchors := anchoredLines(two)
	if len(anchors) != 2 {
		t.Fatalf("got %d anchored lines, want 2", len(anchors))
	}
	if off, ok := learnOffsets([][]*outlineGlyph{two}, [][]*line{anchors})[1]; ok {
		t.Errorf("between two baselines: learned offset %v, want none", off)
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
