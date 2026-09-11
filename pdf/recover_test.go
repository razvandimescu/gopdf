package pdf

import (
	"context"
	"errors"
	"fmt"
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
// one. A labeller can tell the characters apart from GlyphShape.Path alone.
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
	var out []string
	for li, line := range lines {
		y := 740 - 20*float64(li)
		for ci, cell := range strings.Split(line, "\t") {
			x := 40 + 100*float64(ci)
			for _, word := range strings.Fields(cell) {
				for _, c := range strings.ReplaceAll(word, "I", "l") {
					n := len(out)
					out = append(out, synthGlyph(c, x, y+0.12*float64(n%3-1), n%2 == 0))
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
func synthLabeller(_ context.Context, shapes []GlyphShape) (map[int]string, error) {
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

func recoverText(t *testing.T, doc *Document, label GlyphLabeler) (string, RecoveryReport) {
	t.Helper()
	report, err := doc.RecoverOutlines(context.Background(), label)
	if err != nil {
		t.Fatal(err)
	}
	return docText(t, doc), report
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
		if len(g.Alternatives) != 2 {
			t.Errorf("guess %+v: want the other two look-alikes as alternatives", g)
		}
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

func TestRecoverOutlinesMergesWithRealText(t *testing.T) {
	doc := synthDoc(t, synthGlyphs(synthText), "BT /F1 9.72 Tf 300 740 Td (Total) Tj ET")
	text, _ := recoverText(t, doc, synthLabeller)
	if first, _, _ := strings.Cut(text, "\n"); first != "HELLO WORLD, TAKE ONE DEAL"+strings.Repeat(" ", 10)+"Total" {
		t.Errorf("first line %q: want the recovered words and the real one together", first)
	}
}

func TestRecoverOutlinesFeedsTables(t *testing.T) {
	doc := synthDoc(t, synthGlyphs([]string{"HEAD\tAREA", "OAK\tTEN", "ELK\tONE", "DEER\tTWO", "HARE\tNONE"}), "")
	_, report := recoverText(t, doc, synthLabeller)
	// One word per cell leaves no word spaces to find a valley between.
	if !report.SpacingFallback {
		t.Error("spacing found a valley among intra-word gaps alone")
	}
	table, err := doc.Page(0).FindTable(&TableOpts{Headers: []string{"HEAD", "AREA"}})
	if err != nil || table == nil {
		t.Fatalf("no table: %v", err)
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
	report, err := doc.RecoverOutlines(context.Background(), func(ctx context.Context, shapes []GlyphShape) (map[int]string, error) {
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
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(synthText, "")
	unlabeled := strings.Count(all, "K")
	placed := 0
	for p := range doc.NumPages() {
		spans, _ := doc.Page(p).TextSpans()
		for _, s := range spans {
			placed += len([]rune(strings.ReplaceAll(s.Text, " ", "")))
		}
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
	if text := docText(t, doc); strings.ContainsAny(text, "WK") {
		t.Errorf("rejected or unlabelled glyphs reached the text:\n%s", text)
	}
}

func TestRecoverOutlinesIsAllOrNothing(t *testing.T) {
	doc := synthDoc(t, synthGlyphs(synthText), "BT /F1 12 Tf 72 100 Td (real) Tj ET")
	before := docText(t, doc)

	failing := errors.New("labeller down")
	if _, err := doc.RecoverOutlines(context.Background(), func(context.Context, []GlyphShape) (map[int]string, error) {
		return nil, failing
	}); !errors.Is(err, failing) {
		t.Errorf("labeller error: got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := doc.RecoverOutlines(ctx, synthLabeller); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context: got %v", err)
	}
	if _, err := doc.RecoverOutlines(context.Background(), func(context.Context, []GlyphShape) (map[int]string, error) {
		return map[int]string{999: "x"}, nil
	}); err == nil {
		t.Error("a label for a shape that does not exist was accepted")
	}
	if text := docText(t, doc); text != before {
		t.Errorf("failed recovery changed the text:\n%s\nwant\n%s", text, before)
	}

	first, r1 := recoverText(t, doc, synthLabeller)
	second, r2 := recoverText(t, doc, synthLabeller)
	if second != first || !reflect.DeepEqual(r1, r2) {
		t.Errorf("a second recovery differs from the first:\n%s\nwant\n%s", second, first)
	}
}

func TestRecoverOutlinesWithoutCandidates(t *testing.T) {
	doc := synthDoc(t, nil, "BT /F1 12 Tf 72 700 Td (Only text) Tj ET")
	text, report := recoverText(t, doc, func(context.Context, []GlyphShape) (map[int]string, error) {
		t.Error("labeller called with no candidates")
		return nil, nil
	})
	if text != "Only text" || report.Glyphs != 0 {
		t.Errorf("text %q, report %+v", text, report)
	}
}

func TestRemovalNeverSeesRecoveredText(t *testing.T) {
	data := contentPDF(t, strings.Join(synthGlyphs(synthText), "\n"))
	ed := NewEditor(data)
	doc, err := ed.Document()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.RecoverOutlines(context.Background(), synthLabeller); err != nil {
		t.Fatal(err)
	}
	if len(doc.Search("HELLO")) == 0 {
		t.Fatal("recovered text is not searchable")
	}
	ed.RemoveText("HELLO")
	out, err := ed.Apply()
	if err != nil {
		t.Fatal(err)
	}
	after, err := OpenBytes(out)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(mustContent(t, after.reader, after.pages[0])), string(mustContent(t, doc.reader, doc.pages[0])); got != want {
		t.Errorf("removal changed outlines it cannot delete:\n%s", got)
	}
}

func TestGlyphSheet(t *testing.T) {
	doc := synthDoc(t, synthGlyphs(synthText), "")
	_, shapes, err := doc.outlineGlyphs()
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := GlyphSheet(shapes)
	if err != nil {
		t.Fatal(err)
	}
	drawn, err := OpenBytes(sheet)
	if err != nil {
		t.Fatal(err)
	}
	h, err := drawn.Page(0).OutlineHint()
	if err != nil {
		t.Fatal(err)
	}
	text := docText(t, drawn)
	for _, s := range shapes {
		if !strings.Contains(text, fmt.Sprint(s.ID)) {
			t.Errorf("sheet does not name shape %d:\n%s", s.ID, text)
		}
	}
	if h.Candidates != len(shapes) || h.Shapes != len(shapes) {
		t.Errorf("sheet draws %d glyphs in %d shapes, want each of the %d shapes once", h.Candidates, h.Shapes, len(shapes))
	}
}
