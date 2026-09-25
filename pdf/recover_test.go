package pdf

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The synthetic tier: pages drawn from hand-written outlines. The expected
// text is the generator's input, so unlike most of the suite these tests do
// not read back what the code under test wrote.

// synthAlphabet gives each character its own outline: a box whose bottom edge
// is split into as many collinear segments as the character's index, plus
// one. A labeller can tell the characters apart from glyphShape.Path alone.
// 'l' comes first, so its outline is a bare rectangle, which I shares; ’ is an
// apostrophe that the labeller names ",", as a real labeller did.
const synthAlphabet = "lHELOWRDTAKNSpae-,’"

type synthMetric struct{ bottom, top, width float64 }

// synthMetrics are relative to the baseline, in points: capitals 7 pt tall,
// so an em of 9.72 pt.
var synthMetrics = map[rune]synthMetric{
	'l': {0, 7, 1}, 'O': {-0.12, 7.12, 4.6}, 'S': {-0.12, 7.12, 4.6},
	'p': {-2.1, 5.2, 4}, 'a': {-0.12, 5.2, 4}, 'e': {-0.12, 5.2, 4},
	'-': {2.5, 3.3, 2.4}, ',': {-1.4, 1, 1}, '’': {4.2, 6.6, 1},
}

func synthMetricOf(c rune) synthMetric {
	if m, ok := synthMetrics[c]; ok {
		return m
	}
	return synthMetric{0, 7, 4.6}
}

// synthGlyph draws c with its left edge at x and its baseline at y, either
// in absolute coordinates or at the origin under a translating cm.
func synthGlyph(c rune, x, y float64, translated bool) string {
	m := synthMetricOf(c)
	k := strings.IndexRune(synthAlphabet, c)
	pts := [][2]float64{{0, m.bottom}}
	for j := 1; j <= k+1; j++ {
		pts = append(pts, [2]float64{m.width * float64(j) / float64(k+1), m.bottom})
	}
	pts = append(pts, [2]float64{m.width, m.top}, [2]float64{0, m.top})

	var b strings.Builder
	ox, oy := x, y
	if translated {
		fmt.Fprintf(&b, "q 1 0 0 1 %.3f %.3f cm ", x, y)
		ox, oy = 0, 0
	}
	for i, p := range pts {
		op := "l"
		if i == 0 {
			op = "m"
		}
		fmt.Fprintf(&b, "%.3f %.3f %s ", ox+p[0], oy+p[1], op)
	}
	b.WriteString("h f")
	if translated {
		b.WriteString(" Q")
	}
	return b.String()
}

// synthGlyphs lays lines out as outlines, 1.4 pt between the glyphs of a
// word and 4 pt between words, jittering bottoms by a 600 dpi grid step and
// alternating the two emission styles. Tabs separate cells, 100 pt apart.
func synthGlyphs(lines []string) []string {
	return synthGlyphsWith(lines, synthGlyph)
}

// synthGlyphsWith lays lines out as synthGlyphs does, drawing each glyph with
// draw.
func synthGlyphsWith(lines []string, draw func(c rune, x, y float64, translated bool) string) []string {
	var out []string
	for li, line := range lines {
		y := 740 - 20*float64(li)
		for ci, cell := range strings.Split(line, "\t") {
			x := 40 + 100*float64(ci)
			for _, word := range strings.Fields(cell) {
				for _, c := range strings.ReplaceAll(word, "I", "l") {
					n := len(out)
					out = append(out, draw(c, x, y+0.12*float64(n%3-1), n%2 == 0))
					x += synthMetricOf(c).width + 1.4
				}
				x += 4 - 1.4
			}
		}
	}
	return out
}

var synthText = []string{
	"HELLO WORLD, TAKE ONE DEAL",
	"RE-LOAD pale TREK leap NEAR",
	"SEAL peal OAK ape KNEE",
	"IN THE AREA, pal HEN DEN",
	"TEN LAKES pea DRONE SLED",
	"WEEK lap ALE DOE ROAD",
	"DEAN’S NEW RED OAK DESK",
	"HEAD NORTH WEST TO TOWN",
	"ALL ARE WELL",
}

// synthLabeller reads each shape's character from its outline, as a vision
// model would read it from a picture: it cannot tell l from I, and it names
// the apostrophe ",".
func synthLabeller(_ context.Context, shapes []glyphShape) (map[int]string, error) {
	labels := map[int]string{}
	for _, s := range shapes {
		c := []rune(synthAlphabet)[strings.Count(s.Path, " l ")-4]
		if c == '’' {
			c = ','
		}
		labels[s.ID] = string(c)
	}
	return labels, nil
}

func synthDoc(t *testing.T, glyphs []string, extra string) *Document {
	t.Helper()
	doc, err := OpenBytes(contentPDF(t, strings.Join(glyphs, "\n")+"\n"+extra))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// recoverSpans recovers a one-page document's outlined text.
func recoverSpans(t *testing.T, doc *Document, label glyphLabeler) ([]TextSpan, recoveryReport) {
	t.Helper()
	a, report, err := doc.computeRecovery(context.Background(), label)
	if err != nil {
		t.Fatal(err)
	}
	return a.spans[0], report
}

func recoverText(t *testing.T, doc *Document, label glyphLabeler) (string, recoveryReport) {
	t.Helper()
	spans, report := recoverSpans(t, doc, label)
	return spansText(spans), report
}

func spansText(spans []TextSpan) string {
	var lines []string
	for _, l := range BuildLines(spans) {
		lines = append(lines, l.Text)
	}
	return strings.Join(lines, "\n")
}

func TestRecoverOutlinesReadsText(t *testing.T) {
	glyphs := synthGlyphs(synthText)
	text, report := recoverText(t, synthDoc(t, glyphs, ""), synthLabeller)

	want := strings.ReplaceAll(strings.Join(synthText, "\n"), "’", "'")
	if text != want {
		t.Errorf("recovered\n%s\nwant\n%s", text, want)
	}
	if report.Glyphs != len(glyphs) || report.Shapes != len([]rune(synthAlphabet)) ||
		len(report.Omitted) != 0 || report.SpacingFallback {
		t.Errorf("report %+v: want %d glyphs in %d shapes, none omitted, fitted spacing",
			report, len(glyphs), len([]rune(synthAlphabet)))
	}
	// The hyphen, the two commas and the apostrophe are too rare to learn an
	// offset, so the fallback places them.
	if len(report.FallbackPlaced) != 4 {
		t.Errorf("fallback placed %d glyphs, want 4: %+v", len(report.FallbackPlaced), report.FallbackPlaced)
	}
	// Every rectangle is a guess: I beside N, l in the five lowercase words.
	var chosen []string
	for _, g := range report.Guessed {
		chosen = append(chosen, g.Chosen)
	}
	if want := []string{"l", "l", "l", "I", "l", "l"}; !reflect.DeepEqual(chosen, want) {
		t.Errorf("guessed %q, want %q in document order", chosen, want)
	}

	// Drawing the page in the opposite order changes nothing.
	reversed := slices.Clone(glyphs)
	slices.Reverse(reversed)
	if again, _ := recoverText(t, synthDoc(t, reversed, ""), synthLabeller); again != text {
		t.Errorf("reversed drawing order recovered\n%s\nwant\n%s", again, text)
	}
}

// A heading at twice the body size has too few glyphs to learn offsets or
// anchor a line. Its outlines are the body's at another size, so it takes
// their offsets, scaled. The labeller names the heading in capitals, as a
// small-caps face would draw it: transfer moves offsets, never labels.
func TestRecoverOutlinesTransfersOffsetsAcrossSizes(t *testing.T) {
	glyphs := synthGlyphs(synthText)
	x := 40.0
	for _, c := range "pale" {
		glyphs = append(glyphs, scaledGlyph(c, 2, x, 790))
		x += 2*synthMetricOf(c).width + 2.8
	}
	capitalsWhenLarge := func(ctx context.Context, shapes []glyphShape) (map[int]string, error) {
		labels, _ := synthLabeller(ctx, shapes)
		for _, s := range shapes {
			if s.Height > 10 {
				labels[s.ID] = strings.ToUpper(labels[s.ID])
			}
		}
		return labels, nil
	}
	spans, report := recoverSpans(t, synthDoc(t, glyphs, ""), capitalsWhenLarge)
	text := spansText(spans)

	if first, _, _ := strings.Cut(text, "\n"); first != "PALE" {
		t.Errorf("first line %q, want the heading PALE; text:\n%s", first, text)
	}
	// l is a bare rectangle, which transfers nothing: it joins by fallback.
	if len(report.TransferPlaced) != 3 || len(report.Omitted) != 0 {
		t.Errorf("%d transfer-placed, %d omitted; want p, a and e placed by transfer, none omitted",
			len(report.TransferPlaced), len(report.Omitted))
	}
	for _, s := range spans {
		if s.Text == "PALE" && math.Abs(s.Y-790) > baselineTolerance {
			t.Errorf("heading baseline %v, want 790", s.Y)
		}
	}
}

// synthWord draws s k times the synthetic size from x, with its baseline at y,
// and returns the glyphs and the x after the last one.
func synthWord(s string, k, x, y float64) ([]string, float64) {
	var out []string
	for _, c := range s {
		if c == ' ' {
			x += k * (4 - 1.4)
			continue
		}
		out = append(out, scaledGlyph(c, k, x, y))
		x += k * (synthMetricOf(c).width + 1.4)
	}
	return out, x
}

func spanNamed(t *testing.T, spans []TextSpan, text string) TextSpan {
	t.Helper()
	for _, s := range spans {
		if s.Text == text {
			return s
		}
	}
	t.Fatalf("no span %q", text)
	return TextSpan{}
}

// Many faces draw the apostrophe with the comma's outline, raised. The
// comma's offset then predicts a baseline in the middle of the apostrophe's
// own line, and the apostrophe would start a line of its own.
func TestRecoverOutlinesRehomesAnApostropheDrawnAsAComma(t *testing.T) {
	lines := append(slices.Clone(synthText), "TREK, SEAL, LOAD, NEAR") // enough commas to learn from
	commaOutline := func(c rune, x, y float64, translated bool) string {
		if c == '’' {
			return synthGlyph(',', x, y+5.6, translated)
		}
		return synthGlyph(c, x, y, translated)
	}
	text, report := recoverText(t, synthDoc(t, synthGlyphsWith(lines, commaOutline), ""), synthLabeller)

	if !strings.Contains(text, "\nDEAN'S NEW RED OAK DESK\n") || strings.Count(text, "\n") != len(lines)-1 {
		t.Errorf("recovered\n%s\nwant DEAN'S whole, on its line, and no line for the apostrophe", text)
	}
	if len(report.Rehomed) != 1 {
		t.Errorf("rehomed %d glyphs, want the apostrophe", len(report.Rehomed))
	}
}

// The same apostrophe closing a word, where the glyph to its right is too rare
// to have an offset of its own. Nothing sits to its right until the fallback
// places that glyph, so re-homing can only see it on a second look, after
// pass 2.
func TestRecoverOutlinesRehomesAfterTheFallback(t *testing.T) {
	lines := append(slices.Clone(synthText), "TREK, SEAL, LOAD, NEAR", "WELL NOW READ’-")
	commaOutline := func(c rune, x, y float64, translated bool) string {
		if c == '’' {
			return synthGlyph(',', x, y+5.6, translated)
		}
		return synthGlyph(c, x, y, translated)
	}
	text, report := recoverText(t, synthDoc(t, synthGlyphsWith(lines, commaOutline), ""), synthLabeller)

	if !strings.Contains(text, "WELL NOW READ'-") || strings.Count(text, "\n") != len(lines)-1 {
		t.Errorf("recovered\n%s\nwant READ'- whole, on its line, and no line for the apostrophe", text)
	}
	if len(report.Rehomed) != 2 {
		t.Errorf("rehomed %d glyphs, want both apostrophes", len(report.Rehomed))
	}
}

// Two baselines 0.8 pt apart both take the apostrophe into their body, and
// both gain a member to its right only in pass 2. The second look therefore
// finds two homes and no way to choose, and with no pass 3 left to defer to it
// leaves the glyph on the line pass 1 gave it.
func TestRecoverOutlinesLeavesAnAmbiguousGlyphAlone(t *testing.T) {
	lines := append(slices.Clone(synthText), "TREK, SEAL, LOAD, NEAR")
	glyphs := synthGlyphsWith(lines, func(c rune, x, y float64, translated bool) string {
		if c == '’' {
			return synthGlyph(',', x, y+5.6, translated)
		}
		return synthGlyph(c, x, y, translated)
	})
	add := func(s string, y float64) float64 {
		gs, end := synthWord(s, 1, 40, y)
		glyphs = append(glyphs, gs...)
		return end
	}
	end := add("HEAD LAND", 500)
	add("NEAR DEAL", 500.8)
	// The bar to the right of the apostrophe on each line is a bare rectangle
	// at a size of its own: it takes no transferred offset, is too rare to
	// learn one, and is no seed, so only the fallback places it. Until then
	// neither line has a member to the apostrophe's right.
	glyphs = append(glyphs, synthGlyph(',', end+2, 505.6, false),
		scaledGlyph('l', 2, end+5, 500), scaledGlyph('l', 2, end+10, 500.8))
	barsWhenLarge := func(ctx context.Context, shapes []glyphShape) (map[int]string, error) {
		labels, _ := synthLabeller(ctx, shapes)
		for _, s := range shapes {
			if s.Height > 10 {
				labels[s.ID] = "|"
			}
		}
		return labels, nil
	}

	spans, report := recoverSpans(t, synthDoc(t, glyphs, ""), barsWhenLarge)

	if s := spanNamed(t, spans, ","); math.Abs(s.Y-505.6) > baselineTolerance {
		t.Errorf("ambiguous glyph moved to baseline %v, want the line pass 1 gave it, 505.6", s.Y)
	}
	if len(report.Rehomed) != 1 {
		t.Errorf("rehomed %d glyphs, want only the apostrophe of DEAN'S", len(report.Rehomed))
	}
}

// The acceptance gate for re-homing: lines of one shape inside or near
// another line's body, which must stay where their own offsets put them.
// Each is kept by one guard alone.
func TestRecoverOutlinesKeepsLinesThatBelong(t *testing.T) {
	glyphs := synthGlyphs(synthText)
	add := func(s string, k, x, y float64) float64 {
		gs, end := synthWord(s, k, x, y)
		glyphs = append(glyphs, gs...)
		return end
	}
	// A mixed-size row: a body-size cell 0.8 pt below a larger font's
	// baseline, between two of its cells. Kept as other text.
	x := add("HEAD", 1.2, 40, 500)
	x = add("O", 1, x+10, 499.2)
	add("DEAL", 1.2, x+10, 500)
	// A superscript, a smaller outline raised between two words. Kept as
	// other text: its offset is transferred.
	x = add("HEAD TOWN", 1, 40, 460)
	x = add("e", 0.6, x+0.5, 463.2)
	add("WEST", 1, x+4, 460)
	// A cell beside a line rather than within it, its baseline 0.6 em higher.
	// Kept as not between the line's glyphs.
	x = add("OO", 1, 40, 425.8)
	add("HEAD TOWN", 1, x+10, 420)
	// A line 40 pt below the last row, under a gap in it. Kept as outside the
	// row's body.
	add("OOO", 1, 52, 380)

	spans, report := recoverSpans(t, synthDoc(t, glyphs, ""), synthLabeller)
	for _, c := range []struct {
		text     string
		baseline float64
	}{{"O", 499.2}, {"e", 463.2}, {"OO", 425.8}, {"OOO", 380}} {
		if s := spanNamed(t, spans, c.text); math.Abs(s.Y-c.baseline) > baselineTolerance {
			t.Errorf("%q moved to baseline %v, want %v", c.text, s.Y, c.baseline)
		}
	}
	if len(report.Rehomed) != 0 {
		t.Errorf("rehomed %d glyphs, want none", len(report.Rehomed))
	}
}

func TestRecoverOutlinesLinesUpWithRealText(t *testing.T) {
	doc := synthDoc(t, synthGlyphs(synthText), "BT /F1 9.72 Tf 300 740 Td (Total) Tj ET")
	written, err := doc.Page(0).TextSpans()
	if err != nil {
		t.Fatal(err)
	}
	recovered, _ := recoverSpans(t, doc, synthLabeller)
	if first, _, _ := strings.Cut(spansText(append(written, recovered...)), "\n"); first != "HELLO WORLD, TAKE ONE DEAL"+strings.Repeat(" ", 10)+"Total" {
		t.Errorf("first line %q: want the recovered words and the real one together", first)
	}
}

func TestRecoverOutlinesFeedsTables(t *testing.T) {
	spans, report := recoverSpans(t, synthDoc(t, synthGlyphs([]string{"HEAD\tAREA", "OAK\tTEN", "ELK\tONE", "DEER\tTWO", "HARE\tNONE"}), ""), synthLabeller)
	// One word per cell leaves no word spaces to find a valley between.
	if !report.SpacingFallback {
		t.Error("spacing found a valley among intra-word gaps alone")
	}
	table := FindTable(spans, &TableOpts{Headers: []string{"HEAD", "AREA"}})
	if table == nil {
		t.Fatal("no table")
	}
	var rows [][]string
	for _, r := range table.Rows {
		var cells []string
		for _, c := range r.Cells {
			cells = append(cells, c.Text)
		}
		rows = append(rows, cells)
	}
	if want := [][]string{{"OAK", "TEN"}, {"ELK", "ONE"}, {"DEER", "TWO"}, {"HARE", "NONE"}}; !reflect.DeepEqual(rows, want) {
		t.Errorf("rows %q, want %q", rows, want)
	}
}

func TestRecoverOutlinesAccountsForEveryGlyph(t *testing.T) {
	// A stray hyphen far from any line has nowhere to go.
	doc := synthDoc(t, synthGlyphs(synthText), synthGlyph('-', 400, 300, false))
	spans, report := recoverSpans(t, doc, func(ctx context.Context, shapes []glyphShape) (map[int]string, error) {
		labels, _ := synthLabeller(ctx, shapes)
		for id, l := range labels {
			switch l {
			case "W":
				labels[id] = "" // not text
			case "K":
				delete(labels, id) // left unlabelled
			}
		}
		return labels, nil
	})
	all := strings.Join(synthText, "")
	unlabeled := strings.Count(all, "K")
	placed := 0
	for _, s := range spans {
		placed += len([]rune(strings.ReplaceAll(s.Text, " ", "")))
	}
	if report.Rejected != strings.Count(all, "W") || len(report.Unlabeled) != 1 {
		t.Errorf("rejected %d glyphs and left %v unlabelled; want %d rejected and one shape unlabelled",
			report.Rejected, report.Unlabeled, strings.Count(all, "W"))
	}
	if len(report.Omitted) != 1 || report.Omitted[0].Box.X != 400 || report.Omitted[0].Page != 0 {
		t.Errorf("omitted %+v, want the stray hyphen at x = 400", report.Omitted)
	}
	if placed+report.Rejected+unlabeled+len(report.Omitted) != report.Glyphs {
		t.Errorf("%d placed + %d rejected + %d unlabelled + %d omitted != %d glyphs",
			placed, report.Rejected, unlabeled, len(report.Omitted), report.Glyphs)
	}
	if text := spansText(spans); strings.ContainsAny(text, "WK") {
		t.Errorf("rejected or unlabelled glyphs reached the text:\n%s", text)
	}
}

func TestRecoverOutlinesReturnsLabellerErrors(t *testing.T) {
	doc := synthDoc(t, synthGlyphs(synthText), "")

	failing := errors.New("labeller down")
	if _, _, err := doc.computeRecovery(context.Background(), func(context.Context, []glyphShape) (map[int]string, error) {
		return nil, failing
	}); !errors.Is(err, failing) {
		t.Errorf("labeller error: got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := doc.computeRecovery(ctx, synthLabeller); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context: got %v", err)
	}
	if _, _, err := doc.computeRecovery(context.Background(), func(context.Context, []glyphShape) (map[int]string, error) {
		return map[int]string{999: "x"}, nil
	}); err == nil {
		t.Error("a label for a shape that does not exist was accepted")
	}
}

func TestRecoverOutlinesWithoutCandidates(t *testing.T) {
	doc := synthDoc(t, nil, "BT /F1 12 Tf 72 700 Td (Only text) Tj ET")
	spans, report := recoverSpans(t, doc, func(context.Context, []glyphShape) (map[int]string, error) {
		t.Error("labeller called with no candidates")
		return nil, nil
	})
	if len(spans) != 0 || report.Glyphs != 0 {
		t.Errorf("recovered %v, report %+v", spans, report)
	}
}
