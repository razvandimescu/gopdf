package pdf

import (
	"strings"
	"testing"
)

// Removal has one acceptance test and it is mechanical: reopen the output and
// look for the text. Everything else here is about what removal must not
// disturb — the position of the words that stay, the other pages, the rest of
// the operator's own state.

// contentPDF builds a one-page PDF whose content stream is exactly content,
// with Helvetica available as /F1.
func contentPDF(t *testing.T, content string) []byte {
	t.Helper()
	return buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		fontRef := w.AllocRef()
		w.WriteObject(fontRef, Dict{
			"Type": Name("Font"), "Subtype": Name("Type1"), "BaseFont": Name("Helvetica"),
		})
		contentRef := w.AllocRef()
		w.WriteStream(contentRef, Dict{}, []byte(content))
		return Dict{
			"Type": Name("Page"), "Parent": pagesRef,
			"MediaBox":  Array{0, 0, 612, 792},
			"Resources": Dict{"Font": Dict{Name("F1"): fontRef}},
			"Contents":  contentRef,
		}
	})
}

// removeText applies query removal to data and returns the resulting document.
func removeText(t *testing.T, data []byte, query string) *Document {
	t.Helper()
	ed := NewEditor(data)
	ed.RemoveText(query)
	out, err := ed.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	doc, err := OpenBytes(out)
	if err != nil {
		t.Fatalf("opening result: %v", err)
	}
	return doc
}

func docText(t *testing.T, doc *Document) string {
	t.Helper()
	text, err := doc.Text()
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	return text
}

func TestRemoveTextDeletesTheGlyphs(t *testing.T) {
	const secret = "123-45-6789"
	data := testPDF(t, "Name: Jane Doe", "SSN "+secret+" (on file)")

	doc := removeText(t, data, secret)

	if text := docText(t, doc); strings.Contains(text, secret) {
		t.Errorf("extraction still returns the removed text: %q", text)
	}
	if hits := doc.Search(secret); len(hits) != 0 {
		t.Errorf("search still finds the removed text: %+v", hits)
	}
	// The bytes themselves, not just the decoding of them: a stream that still
	// holds the digits is one a different parser could still read. They could
	// be written either way round, as a literal string or a hex one.
	content := string(mustContent(t, doc.reader, doc.pages[0]))
	if strings.Contains(content, secret) || strings.Contains(content, hexEncode([]byte(secret))) {
		t.Errorf("the removed codes are still in the content stream:\n%s", content)
	}

	if text := docText(t, doc); !strings.Contains(text, "Jane Doe") || !strings.Contains(text, "on file") {
		t.Errorf("removal took surrounding text with it: %q", text)
	}
}

// The surviving text must not reflow: a removed run leaves a kerning number
// worth exactly the advance it had, so what follows stays where the reader
// last saw it.
func TestRemoveTextLeavesSurvivorsInPlace(t *testing.T) {
	// Three separate operators, so the survivor's own origin is reported
	// exactly rather than estimated from a span's average character width.
	data := contentPDF(t, "BT /F1 12 Tf 72 750 Td (Total ) Tj (123456) Tj ( due now) Tj ET")

	before, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	after := removeText(t, data, "123456")

	origin := func(doc *Document, word string) TextSpan {
		t.Helper()
		spans, _ := doc.Page(0).TextSpans()
		for _, s := range spans {
			if strings.Contains(s.Text, word) {
				return s
			}
		}
		t.Fatalf("no span containing %q", word)
		return TextSpan{}
	}

	want, got := origin(before, "due"), origin(after, "due")
	if diff := got.X - want.X; diff > 0.01 || diff < -0.01 {
		t.Errorf("text after the removal moved by %.3f pt", diff)
	}
	if diff := got.EndX - want.EndX; diff > 0.01 || diff < -0.01 {
		t.Errorf("the pen ended %.3f pt from where it did before", diff)
	}
}

// A match that crosses the elements of a TJ array — justified text, where the
// space between two words is a kerning number rather than a space glyph.
func TestRemoveTextAcrossOneOperatorsElements(t *testing.T) {
	data := contentPDF(t, "BT /F1 12 Tf 72 750 Td [(Jane) -250 (Doe) -250 (stays)] TJ ET")

	doc := removeText(t, data, "Jane Doe")

	text := docText(t, doc)
	if strings.Contains(text, "Jane") || strings.Contains(text, "Doe") {
		t.Errorf("both halves of the match should be gone: %q", text)
	}
	if !strings.Contains(text, "stays") {
		t.Errorf("the third element was not part of the match: %q", text)
	}
}

// A match that crosses two operators, and the ' and " forms, which carry state
// of their own that the replacement has to set again.
func TestRemoveTextAcrossOperators(t *testing.T) {
	data := contentPDF(t, "BT /F1 12 Tf 14 TL 72 750 Td (keep) Tj\n"+
		"T* (secret one) Tj\n"+
		"(secret two) '\n"+
		"3 1 (secret three) \"\n"+
		"(keep last) Tj ET")

	doc := removeText(t, data, "secret")

	text := docText(t, doc)
	if strings.Contains(text, "secret") {
		t.Errorf("removal missed an occurrence: %q", text)
	}
	for _, want := range []string{"one", "two", "three", "keep", "keep last"} {
		if !strings.Contains(text, want) {
			t.Errorf("removal took %q with it: %q", want, text)
		}
	}

	// The " operator's operands were the word and character spacing; dropping
	// them would respace every line after it.
	content := string(mustContent(t, doc.reader, doc.pages[0]))
	if !strings.Contains(content, "3 Tw 1 Tc") {
		t.Errorf("the \" operator's spacing was not preserved:\n%s", content)
	}
}

func TestRemoveTextReachesFormXObjects(t *testing.T) {
	data := buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		fontRef := w.AllocRef()
		w.WriteObject(fontRef, Dict{
			"Type": Name("Font"), "Subtype": Name("Type1"), "BaseFont": Name("Helvetica"),
		})
		resources := Dict{"Font": Dict{Name("F1"): fontRef}}

		formRef := w.AllocRef()
		w.WriteStream(formRef, Dict{
			"Type": Name("XObject"), "Subtype": Name("Form"),
			"BBox":      Array{0, 0, 300, 50},
			"Resources": resources,
		}, []byte("BT /F1 12 Tf 0 0 Td (letterhead SECRET) Tj ET"))

		contentRef := w.AllocRef()
		// The same form twice: one object, drawn in two places.
		w.WriteStream(contentRef, Dict{}, []byte(
			"q 1 0 0 1 72 700 cm /Fm Do Q\nq 1 0 0 1 72 300 cm /Fm Do Q\n"))

		return Dict{
			"Type": Name("Page"), "Parent": pagesRef,
			"MediaBox": Array{0, 0, 612, 792},
			"Resources": Dict{
				"Font":    Dict{Name("F1"): fontRef},
				"XObject": Dict{Name("Fm"): formRef},
			},
			"Contents": contentRef,
		}
	})

	doc := removeText(t, data, "SECRET")

	text := docText(t, doc)
	if strings.Contains(text, "SECRET") {
		t.Errorf("text inside the form survived: %q", text)
	}
	if n := strings.Count(text, "letterhead"); n != 2 {
		t.Errorf("the form should still draw twice, got %d occurrences: %q", n, text)
	}
}

func TestRemoveRegionDeletesWhatItCovers(t *testing.T) {
	data := testPDF(t, "keep this line", "delete this line")

	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	hits := doc.Search("delete this line")
	if len(hits) == 0 {
		t.Fatal("nothing to remove")
	}

	ed := NewEditor(data)
	ed.RemoveRegion(hits[0].Page, hits[0].Rect)
	out, err := ed.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	result, err := OpenBytes(out)
	if err != nil {
		t.Fatal(err)
	}

	text := docText(t, result)
	if strings.Contains(text, "delete") {
		t.Errorf("the covered line survived: %q", text)
	}
	if !strings.Contains(text, "keep this line") {
		t.Errorf("a line outside the region was removed: %q", text)
	}
}

// Rectangles are in displayed space, so on a rotated page they only land on
// the right glyphs if removal maps the page's own coordinates through /Rotate.
// The rectangle here is built by hand: Page.Search reports rotated text as if
// it still ran left to right, so its rectangles cannot answer this.
func TestRemoveRegionOnRotatedPage(t *testing.T) {
	data := buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		fontRef := w.AllocRef()
		w.WriteObject(fontRef, Dict{
			"Type": Name("Font"), "Subtype": Name("Type1"), "BaseFont": Name("Helvetica"),
		})
		contentRef := w.AllocRef()
		w.WriteStream(contentRef, Dict{}, []byte(
			"BT /F1 12 Tf 72 750 Td (rotated secret) Tj ET\n"+
				"BT /F1 12 Tf 72 700 Td (elsewhere) Tj ET"))
		return Dict{
			"Type": Name("Page"), "Parent": pagesRef,
			"MediaBox":  Array{0, 0, 612, 792},
			"Rotate":    90,
			"Resources": Dict{"Font": Dict{Name("F1"): fontRef}},
			"Contents":  contentRef,
		}
	})

	// Quarter-turned, the baseline at user-space y=750 stands at displayed
	// x=750, and the text runs down from displayed y = 612-72 = 540.
	ed := NewEditor(data)
	ed.RemoveRegion(0, Rect{X: 744, Y: 440, Width: 12, Height: 100})
	out, err := ed.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	doc, err := OpenBytes(out)
	if err != nil {
		t.Fatal(err)
	}

	text := docText(t, doc)
	if strings.Contains(text, "secret") {
		t.Errorf("the rotated page's rectangle missed its text: %q", text)
	}
	if !strings.Contains(text, "elsewhere") {
		t.Errorf("the rectangle reached a second baseline: %q", text)
	}
}

func TestRemoveTextOnlyTouchesPagesThatMatch(t *testing.T) {
	data := testMultiPagePDF(t, "page one keeps its text", "page two has a secret", "page three keeps its text")

	doc := removeText(t, data, "secret")

	if doc.NumPages() != 3 {
		t.Fatalf("page count: got %d, want 3", doc.NumPages())
	}
	for i, want := range []string{"page one keeps its text", "", "page three keeps its text"} {
		text, err := doc.Page(i).Text()
		if err != nil {
			t.Fatal(err)
		}
		if want != "" && text != want {
			t.Errorf("page %d: got %q, want %q", i, text, want)
		}
	}
	if text, _ := doc.Page(1).Text(); strings.Contains(text, "secret") {
		t.Errorf("page 1 still has the text: %q", text)
	}
}

// Removal and the black box are separate calls on purpose: one deletes, the
// other marks. Used together they must not interfere.
func TestRemoveTextComposesWithRedactText(t *testing.T) {
	const secret = "confidential"
	data := testPDF(t, "marked as "+secret+" here")

	ed := NewEditor(data)
	ed.RemoveText(secret)
	if err := ed.RedactText(secret, 0, 0, 0); err != nil {
		t.Fatalf("RedactText: %v", err)
	}
	out, err := ed.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	doc, err := OpenBytes(out)
	if err != nil {
		t.Fatal(err)
	}

	if text := docText(t, doc); strings.Contains(text, secret) {
		t.Errorf("text survived: %q", text)
	}
	content := string(mustContent(t, doc.reader, doc.pages[0]))
	if !strings.Contains(content, " re f Q") {
		t.Errorf("the covering rectangle was not drawn:\n%s", content)
	}
}

func TestRemoveTextWithoutAMatchChangesNothing(t *testing.T) {
	data := testPDF(t, "nothing to see here")

	ed := NewEditor(data)
	ed.RemoveText("absent")
	out, err := ed.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	doc, err := OpenBytes(out)
	if err != nil {
		t.Fatal(err)
	}

	original, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	want := string(mustContent(t, original.reader, original.pages[0]))
	if got := string(mustContent(t, doc.reader, doc.pages[0])); !strings.Contains(got, want) {
		t.Errorf("content stream was rewritten:\ngot  %q\nwant it to contain %q", got, want)
	}
}

func TestFormatOperand(t *testing.T) {
	// Exponent notation is not valid PDF syntax, and a number written that way
	// would end the operation the reader was in the middle of.
	cases := map[float64]string{
		0:            "0",
		-0.0001:      "0",
		1:            "1",
		-5670:        "-5670",
		1.0 / 3.0:    "0.333",
		0.0000001234: "0",
		-1e21:        "-1000000000000000000000",
	}
	for in, want := range cases {
		if got := formatOperand(in); got != want {
			t.Errorf("formatOperand(%v) = %q, want %q", in, got, want)
		}
	}
}

// A removed run is paid for with a kerning number, so a kern and a glyph
// advance have to travel the same path: both are text-space distances, and a
// text matrix carrying a scale moves the pen by the transformed amount.
func TestKernAdvancesThroughTheTextMatrix(t *testing.T) {
	// Helvetica's A is 667/1000 em, so at 12pt doubled by the Tm it is 16.008
	// wide; the -1000 kern is one em of text space, 24pt once doubled.
	data := contentPDF(t, "BT /F1 12 Tf 2 0 0 2 100 700 Tm (A) Tj [-1000] TJ (B) Tj ET")

	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	spans, _ := doc.Page(0).TextSpans()
	for _, span := range spans {
		if span.Text != "B" {
			continue
		}
		if diff := span.X - 140.008; diff > 0.01 || diff < -0.01 {
			t.Errorf("B sits at %.3f, want 140.008", span.X)
		}
		return
	}
	t.Fatal("no span for B")
}

// One code can stand for several characters — a ligature, most often. The
// match is in characters and the deletion is in codes, so the two have to be
// tied together even when they do not correspond one to one.
func TestRemoveTextMapsCharactersBackToCodes(t *testing.T) {
	data := buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		toUnicodeRef := w.AllocRef()
		w.WriteStream(toUnicodeRef, Dict{}, []byte(
			"1 beginbfchar\n<01> <00660066>\nendbfchar\n"))

		fontRef := w.AllocRef()
		w.WriteObject(fontRef, Dict{
			"Type": Name("Font"), "Subtype": Name("Type1"), "BaseFont": Name("Helvetica"),
			"ToUnicode": toUnicodeRef,
		})

		contentRef := w.AllocRef()
		w.WriteStream(contentRef, Dict{}, []byte(
			"BT /F1 12 Tf 72 750 Td (o) Tj <01> Tj (ice) Tj ET"))

		return Dict{
			"Type": Name("Page"), "Parent": pagesRef,
			"MediaBox":  Array{0, 0, 612, 792},
			"Resources": Dict{"Font": Dict{Name("F1"): fontRef}},
			"Contents":  contentRef,
		}
	})

	original, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if text := docText(t, original); !strings.Contains(text, "office") {
		t.Fatalf("the ligature did not decode: %q", text)
	}

	doc := removeText(t, data, "ff")
	if text := docText(t, doc); strings.Contains(text, "ff") {
		t.Errorf("the code behind the characters survived: %q", text)
	}
}
