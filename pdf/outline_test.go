package pdf

import (
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// fillsOf returns the fills of a one-page PDF whose content stream is content.
func fillsOf(t *testing.T, content string) []filledPath {
	t.Helper()
	return fillsOfPDF(t, contentPDF(t, content))
}

func fillsOfPDF(t *testing.T, data []byte) []filledPath {
	t.Helper()
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	fills, err := pageFills(doc.pages[0], doc.reader)
	if err != nil {
		t.Fatal(err)
	}
	return fills
}

// repeatingOutlines is 15 L and 15 T glyphs: enough repetition to look like
// outlined text.
func repeatingOutlines() string {
	var b strings.Builder
	for i := range 15 {
		b.WriteString(glyphL(float64(20+10*i), 700, 0))
		b.WriteString(glyphT(float64(20+10*i), 680))
	}
	return b.String()
}

// glyphL draws an L-shaped outline with its corner at (x, y) in absolute
// coordinates. jitter moves the inner vertices, the way snapping to a device
// grid moves an outline drawn at a different sub-pixel position.
func glyphL(x, y, jitter float64) string {
	return fmt.Sprintf("%g %g m %g %g l %g %g l %g %g l %g %g l %g %g l h f\n",
		x, y, x+4, y, x+4, y+1, x+1+jitter, y+1, x+1+jitter, y+8, x, y+8)
}

// glyphLTranslated draws the same outline the other way producers emit it: at
// the origin, positioned by a translating cm.
func glyphLTranslated(x, y, jitter float64) string {
	return fmt.Sprintf("q 1 0 0 1 %g %g cm %s Q\n", x, y, glyphL(0, 0, jitter))
}

func glyphT(x, y float64) string {
	return fmt.Sprintf("%g %g m %g %g l %g %g l %g %g l %g %g l %g %g l %g %g l %g %g l h f\n",
		x+2, y, x+3, y, x+3, y+7, x+5, y+7, x+5, y+8, x, y+8, x, y+7, x+2, y+7)
}

func TestPathCaptureNormalisesEquivalentGeometry(t *testing.T) {
	equivalent := [][]string{
		{
			"10 20 30 40 re f",
			"10 20 m 40 20 l 40 60 l 10 60 l h f",
			"10 20 m 40 20 l 40 60 l 10 60 l 10 20 l h f", // closing edge drawn explicitly
			"10 20 m 40 20 l 40 60 l 10 60 l f",           // no h: the fill closes it
		},
		{"0 0 m 5 5 10 10 v f", "0 0 m 0 0 5 5 10 10 c f"},
		{"0 0 m 1 1 10 10 y f", "0 0 m 1 1 10 10 10 10 c f"},
		{"0 0 m 5 5 m 9 9 l 5 9 l f", "5 5 m 9 9 l 5 9 l f"},                         // a subpath with no segments draws nothing
		{"0 0 m 10 0 l 10 10 l h 0 0 m 0 10 l f", "0 0 m 10 0 l 10 10 l h 0 10 l f"}, // after h, a segment starts a new subpath at the old start
	}
	for _, group := range equivalent {
		want := fillsOf(t, group[0])
		if len(want) != 1 {
			t.Fatalf("%q: got %d fills, want 1", group[0], len(want))
		}
		for _, content := range group[1:] {
			if got := fillsOf(t, content); !reflect.DeepEqual(got, want) {
				t.Errorf("%q normalised to\n%+v\nwant, as for %q,\n%+v", content, got, group[0], want)
			}
		}
	}
}

func TestPathCaptureRecordsOnlyFills(t *testing.T) {
	cases := []struct {
		content string
		fills   int
		evenOdd bool
	}{
		{"0 0 10 10 re f", 1, false},
		{"0 0 10 10 re F", 1, false},
		{"0 0 10 10 re f*", 1, true},
		{"0 0 10 10 re B", 1, false},
		{"0 0 10 10 re b*", 1, true},
		{"0 0 10 10 re W f", 1, false}, // clipping does not stop the fill
		{"0 0 10 10 re W n", 0, false}, // a clip paints nothing
		{"0 0 10 10 re n", 0, false},
		{"0 0 m 10 10 l S", 0, false},
		{"0 0 10 10 re s", 0, false},
		{"10 10 l 20 20 l f", 0, false}, // no current point: malformed, dropped
		{"10 10 re f", 0, false},        // too few operands: malformed, ignored
	}
	for _, c := range cases {
		fills := fillsOf(t, c.content)
		if len(fills) != c.fills {
			t.Errorf("%q: got %d fills, want %d", c.content, len(fills), c.fills)
			continue
		}
		if c.fills == 1 && fills[0].evenOdd != c.evenOdd {
			t.Errorf("%q: evenOdd = %v, want %v", c.content, fills[0].evenOdd, c.evenOdd)
		}
	}
}

func TestPathCaptureAppliesCTM(t *testing.T) {
	cases := []struct {
		content    string
		x0, y0, x1 float64
	}{
		{"2 0 0 2 100 100 cm 0 0 5 5 re f", 100, 100, 110},
		{"q 2 0 0 2 0 0 cm Q 0 0 5 5 re f", 0, 0, 5}, // Q restores the CTM
	}
	for _, c := range cases {
		fills := fillsOf(t, c.content)
		if len(fills) != 1 {
			t.Fatalf("%q: got %d fills", c.content, len(fills))
		}
		x0, y0, x1, _ := fills[0].bounds()
		if !approx(x0, c.x0) || !approx(y0, c.y0) || !approx(x1, c.x1) {
			t.Errorf("%q: bounds start (%g, %g) to x %g, want (%g, %g) to x %g", c.content, x0, y0, x1, c.x0, c.y0, c.x1)
		}
	}
}

// A fill inside nested Form XObjects reaches the page through both forms'
// matrices and the CTM in force where the outer form was drawn.
func TestPathCaptureCarriesFormFillsToThePage(t *testing.T) {
	data := buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		inner, outer, contentRef := w.AllocRef(), w.AllocRef(), w.AllocRef()
		w.WriteStream(inner, Dict{
			"Type": Name("XObject"), "Subtype": Name("Form"),
			"BBox": Array{0, 0, 10, 10}, "Matrix": Array{1, 0, 0, 1, 10, 0},
		}, []byte("0 0 1 1 re f"))
		w.WriteStream(outer, Dict{
			"Type": Name("XObject"), "Subtype": Name("Form"),
			"BBox": Array{0, 0, 100, 100}, "Matrix": Array{2, 0, 0, 2, 0, 0},
			"Resources": Dict{"XObject": Dict{Name("Fm2"): inner}},
		}, []byte("/Fm2 Do"))
		w.WriteStream(contentRef, Dict{}, []byte("1 0 0 1 100 200 cm /Fm1 Do"))
		return Dict{
			"Type": Name("Page"), "Parent": pagesRef, "MediaBox": Array{0, 0, 612, 792},
			"Resources": Dict{"XObject": Dict{Name("Fm1"): outer}},
			"Contents":  contentRef,
		}
	})
	fills := fillsOfPDF(t, data)
	if len(fills) != 1 {
		t.Fatalf("got %d fills, want 1", len(fills))
	}
	x0, y0, x1, y1 := fills[0].bounds()
	if !approx(x0, 120) || !approx(y0, 200) || !approx(x1, 122) || !approx(y1, 202) {
		t.Errorf("form fill at (%g, %g)-(%g, %g), want (120, 200)-(122, 202)", x0, y0, x1, y1)
	}
}

// On a rotated page a fill lands where a text span drawn at the same point
// does: both are reported in displayed space.
func TestPathCaptureFollowsPageRotation(t *testing.T) {
	data := buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		fontRef, contentRef := w.AllocRef(), w.AllocRef()
		w.WriteObject(fontRef, Dict{"Type": Name("Font"), "Subtype": Name("Type1"), "BaseFont": Name("Helvetica")})
		w.WriteStream(contentRef, Dict{}, []byte("100 200 1 1 re f BT /F1 10 Tf 100 200 Td (A) Tj ET"))
		return Dict{
			"Type": Name("Page"), "Parent": pagesRef, "MediaBox": Array{0, 0, 612, 792}, "Rotate": 90,
			"Resources": Dict{"Font": Dict{Name("F1"): fontRef}},
			"Contents":  contentRef,
		}
	})
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	fills, err := pageFills(doc.pages[0], doc.reader)
	if err != nil || len(fills) != 1 {
		t.Fatalf("got %d fills, err %v", len(fills), err)
	}
	spans := mustSpans(t, doc.Page(0))
	first := fills[0].segs[0].pts[0]
	if !approx(first[0], 200) || !approx(first[1], 512) {
		t.Errorf("fill starts at (%g, %g), want (200, 512)", first[0], first[1])
	}
	if len(spans) != 1 || !approx(spans[0].X, first[0]) || !approx(spans[0].Y, first[1]) {
		t.Errorf("text drawn at the same point is at %+v, fill at %v", spans, first)
	}
}

func TestGlyphCandidates(t *testing.T) {
	cases := []struct {
		content string
		want    bool
	}{
		{"0 0 6 8 re f", true},
		{"0 0 1 11.5 re f", true},  // a bar glyph: aspect 11.5
		{"0 0 4 1 re f", true},     // a hyphen
		{"0 0 1 13 re f", false},   // too thin for its length: a rule
		{"0 0 500 1 re f", false},  // a ruling line
		{"0 0 100 20 re f", false}, // a cell background
		{"0 0 6 0 re f", false},    // no area
	}
	for _, c := range cases {
		fills := fillsOf(t, c.content)
		if len(fills) != 1 {
			t.Fatalf("%q: got %d fills", c.content, len(fills))
		}
		if got := fills[0].isGlyphCandidate(); got != c.want {
			t.Errorf("%q: isGlyphCandidate = %v, want %v", c.content, got, c.want)
		}
	}
}

// sameShapes reports, for every pair of fills, whether clustering put them
// together — the partition itself, independent of how shapes are numbered.
func sameShapes(fills []filledPath) map[[2][2]float64]bool {
	ids, _ := clusterShapes(fills)
	pairs := make(map[[2][2]float64]bool)
	for i := range fills {
		for j := range fills {
			pairs[[2][2]float64{fills[i].segs[0].pts[0], fills[j].segs[0].pts[0]}] = ids[i] == ids[j]
		}
	}
	return pairs
}

func TestClusterShapesAbsorbsGridJitter(t *testing.T) {
	var b strings.Builder
	for i := range 10 {
		x, y := float64(20+10*i), 700.0
		jitter := 0.12 * float64(i%2) // one step of a 600 dpi grid
		if i%3 == 0 {
			b.WriteString(glyphLTranslated(x, y, jitter))
		} else {
			b.WriteString(glyphL(x, y, jitter))
		}
		b.WriteString(glyphT(x, y-20))
	}
	fills := fillsOf(t, b.String())
	if _, shapes := clusterShapes(fills); shapes != 2 {
		t.Errorf("got %d shapes for jittered L and T glyphs in both emission styles, want 2", shapes)
	}

	// Reversing the drawing order must not change which glyphs share a shape.
	reversed := slices.Clone(fills)
	slices.Reverse(reversed)
	if !reflect.DeepEqual(sameShapes(fills), sameShapes(reversed)) {
		t.Error("reversing the drawing order changed the partition")
	}
}

// Every member of a shape must lie within tolerance of every other, not only
// of the first one seen. Drawn in this order, 0.4 is within 0.25 of the first
// outline (0.2) but not of the second (0); a rule comparing against the first
// member alone would merge all three.
func TestClusterShapesBoundsTheSpread(t *testing.T) {
	fills := fillsOf(t, glyphL(10, 10, 0.2)+glyphL(30, 10, 0)+glyphL(50, 10, 0.4))
	ids, shapes := clusterShapes(fills)
	if shapes != 2 || ids[0] != ids[1] || ids[2] == ids[0] {
		t.Errorf("got shape IDs %v, want the first two together and the third apart", ids)
	}
}

func TestClusterShapesSeparatesFillRules(t *testing.T) {
	fills := fillsOf(t, "0 0 m 8 0 l 8 8 l 0 8 l h 2 2 m 6 2 l 6 6 l 2 6 l h f "+
		"20 0 m 28 0 l 28 8 l 20 8 l h 22 2 m 26 2 l 26 6 l 22 6 l h f*")
	if _, shapes := clusterShapes(fills); shapes != 2 {
		t.Errorf("the same outline under f and f* gave %d shapes, want 2", shapes)
	}
}

func outlineHint(t *testing.T, data []byte) OutlineHint {
	t.Helper()
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	h, err := doc.Page(0).OutlineHint()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestOutlineHint(t *testing.T) {
	var varied, few strings.Builder
	for i := range 25 {
		// 25 different outlines: each rectangle is a different size.
		fmt.Fprintf(&varied, "%d 600 %g 8 re f\n", 20+10*i, 1+0.5*float64(i))
	}
	for i := range 10 {
		few.WriteString(glyphL(float64(20+10*i), 700, 0))
	}
	outlined := repeatingOutlines() + "0 0 612 1 re f 50 50 200 40 re f" // a rule and a background are not candidates

	cases := []struct {
		name     string
		data     []byte
		want     OutlineHint
		possible bool
	}{
		{"repeating outlines", contentPDF(t, outlined), OutlineHint{Candidates: 30, Shapes: 2}, true},
		{"no repetition", contentPDF(t, varied.String()), OutlineHint{Candidates: 25, Shapes: 25}, false},
		{"too few", contentPDF(t, few.String()), OutlineHint{Candidates: 10, Shapes: 1}, false},
		{"real text", testPDF(t, "Invoice 42"), OutlineHint{}, false},
	}
	for _, c := range cases {
		got := outlineHint(t, c.data)
		if got != c.want || got.Possible() != c.possible {
			t.Errorf("%s: got %+v possible=%v, want %+v possible=%v", c.name, got, got.Possible(), c.want, c.possible)
		}
	}
}

// A page whose content cannot be read is an error, not an empty hint.
func TestOutlineHintReportsUnreadableContent(t *testing.T) {
	data := buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		return Dict{"Type": Name("Page"), "Parent": pagesRef, "MediaBox": Array{0, 0, 612, 792}, "Contents": 7}
	})
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Page(0).OutlineHint(); err == nil {
		t.Error("got no error for a page whose Contents is not a stream")
	}
}

// Collecting paths rides along with text extraction; it must not change a
// single span. Checked on synthetic content mixing both, on the committed
// fixtures, and on the private corpus when it is present.
func TestPathCaptureLeavesTextUnchanged(t *testing.T) {
	check := func(t *testing.T, name string, doc *Document) {
		t.Helper()
		for i, page := range doc.pages {
			want := ExtractPageText(page, doc.reader)
			got, err := extractPage(page, doc.reader, &pathCollector{})
			if err != nil {
				t.Fatalf("%s page %d: %v", name, i, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s page %d: spans changed when paths were collected", name, i)
			}
		}
	}

	mixed, err := OpenBytes(contentPDF(t, glyphL(20, 700, 0)+
		"BT /F1 12 Tf 72 650 Td (Total) Tj 40 0 Td (42.00) Tj ET 0 0 612 1 re f 72 640 m 200 640 l S "+glyphLTranslated(40, 700, 0.12)))
	if err != nil {
		t.Fatal(err)
	}
	check(t, "mixed", mixed)

	checkFiles := func(t *testing.T, paths []string) {
		for _, path := range paths {
			doc, err := OpenFile(path)
			if err != nil {
				continue // unreadable or encrypted fixtures are covered elsewhere
			}
			check(t, filepath.Base(path), doc)
		}
	}
	fixtures, _ := filepath.Glob("testdata/*.pdf")
	if len(fixtures) == 0 {
		t.Fatal("no committed fixtures in testdata")
	}
	checkFiles(t, fixtures)
	t.Run("corpus", func(t *testing.T) { checkFiles(t, realPDFs(t)) })
}

// Removal walks the same content without collecting paths. It must delete the
// real text it is asked for and leave outlines, and the rest of the stream,
// exactly as they were.
func TestRemovalLeavesOutlinesAlone(t *testing.T) {
	outlines := repeatingOutlines()
	content := outlines + "BT /F1 12 Tf 72 600 Td (SECRET) Tj ET"
	data := contentPDF(t, content)
	before := outlineHint(t, data)

	doc := removeText(t, data, "SECRET")
	if text := docText(t, doc); strings.Contains(text, "SECRET") {
		t.Errorf("removal left the text: %q", text)
	}
	after := string(mustContent(t, doc.reader, doc.pages[0]))
	if !strings.Contains(after, outlines) {
		t.Errorf("removal changed the outline operators:\n%s", after)
	}
	if h, _ := doc.Page(0).OutlineHint(); h != before {
		t.Errorf("outline hint after removal = %+v, want %+v", h, before)
	}

	// A query no real text matches changes nothing, whatever the outlines
	// happen to spell.
	untouched := removeText(t, data, "LT")
	if got := string(mustContent(t, untouched.reader, untouched.pages[0])); got != content {
		t.Errorf("removing unmatched text changed the content:\n%s", got)
	}
}

// The motivating file, from the private corpus: five pages drawn entirely as
// outlines after one page of real text. 3,703 is the glyph count the
// prototype found by fill colour; the geometric filter must find the same.
func TestIntegration_OutlinedRFQ(t *testing.T) {
	doc := openTestPDF(t, "outlined_rfq.pdf")
	total := 0
	for i := range doc.NumPages() {
		h, err := doc.Page(i).OutlineHint()
		if err != nil {
			t.Fatal(err)
		}
		total += h.Candidates
		if want := i > 0; h.Possible() != want {
			t.Errorf("page %d: %+v possible=%v, want %v", i+1, h, h.Possible(), want)
		}
		if text, _ := doc.Page(i).Text(); i > 0 && strings.TrimSpace(text) != "" {
			t.Errorf("page %d: extraction now returns text %q; phase 1 must not change it", i+1, text)
		}
	}
	if total != 3703 {
		t.Errorf("got %d glyph candidates, want 3703", total)
	}
}
