package pdf

import (
	"strings"
	"testing"
)

func TestRotateOverlaySpace(t *testing.T) {
	const content = "q 1 0 0 1 0 0 cm /Im Do Q\n"

	// Unrotated pages are left untouched.
	for _, r := range []int{0, 360, -360} {
		if got := rotateOverlaySpace(r, 595, 842, content); got != content {
			t.Errorf("rotation %d: expected passthrough, got %q", r, got)
		}
	}

	// Empty content never gets wrapped.
	if got := rotateOverlaySpace(90, 595, 842, ""); got != "" {
		t.Errorf("empty content: got %q", got)
	}

	cases := map[int]string{
		90:  "q 0 1 -1 0 595.0000 0 cm\n",
		180: "q -1 0 0 -1 595.0000 842.0000 cm\n",
		270: "q 0 -1 1 0 0 842.0000 cm\n",
	}
	for rot, prefix := range cases {
		got := rotateOverlaySpace(rot, 595, 842, content)
		if !strings.HasPrefix(got, prefix) {
			t.Errorf("rotation %d: want prefix %q, got %q", rot, prefix, got)
		}
		if !strings.HasSuffix(got, content+"Q\n") {
			t.Errorf("rotation %d: content not wrapped, got %q", rot, got)
		}
		// Normalized rotation must match its canonical form.
		if alt := rotateOverlaySpace(rot+360, 595, 842, content); alt != got {
			t.Errorf("rotation %d not normalized: %q != %q", rot, alt, got)
		}
	}
}

func TestOverlayIsolatesExistingCTM(t *testing.T) {
	// A page whose content applies a top-level vertical-flip CTM and never
	// resets it — as top-left-origin generators (e.g. Chromium/Skia) emit.
	data := buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		contentRef := w.AllocRef()
		w.WriteStream(contentRef, Dict{}, []byte("1 0 0 -1 0 792 cm\nBT /F1 12 Tf 0 0 Td (flipped) Tj ET"))
		return Dict{
			"Type": Name("Page"), "Parent": pagesRef,
			"MediaBox": Array{0, 0, 612, 792}, "Contents": contentRef,
		}
	})

	ed := NewEditor(data)
	ed.AddImage(ImageOverlay{Page: 0, Image: &Image{Width: 1, Height: 1, rgb: []byte{255, 0, 0}},
		CX: 306, CY: 396, Width: 100, Height: 100, Rotation: 45, Opacity: 1})
	out, err := ed.Apply()
	if err != nil {
		t.Fatal(err)
	}

	r, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	pages, _ := r.Pages()
	s := string(mustContent(t, r, pages[0]))

	flip := strings.Index(s, "1 0 0 -1 0 792 cm")
	draw := strings.Index(s, "/Im_gopdf")
	if flip < 0 || draw < 0 {
		t.Fatalf("missing flip (%d) or overlay draw (%d) in: %q", flip, draw, s)
	}
	// The existing flip must be closed by a Q before the overlay is drawn,
	// so the watermark is not flipped along with the page content.
	closing := strings.Index(s[flip:], "Q")
	if closing < 0 || flip+closing > draw {
		t.Errorf("existing CTM not isolated before overlay; content: %q", s)
	}
}

func TestOverlayBalancesMalformedContent(t *testing.T) {
	// Source content with a graphics-stack underflow (extra Q) and an
	// unbalanced EMC — as seen when a generator splits content into chunks
	// and one chunk goes missing. The rewritten stream must be well-formed.
	data := buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		contentRef := w.AllocRef()
		w.WriteStream(contentRef, Dict{}, []byte("q 1 0 0 1 0 0 cm Q Q EMC Q"))
		return Dict{
			"Type": Name("Page"), "Parent": pagesRef,
			"MediaBox": Array{0, 0, 612, 792}, "Contents": contentRef,
		}
	})

	ed := NewEditor(data)
	ed.AddImage(ImageOverlay{Page: 0, Image: &Image{Width: 1, Height: 1, rgb: []byte{0, 0, 0}},
		CX: 100, CY: 100, Width: 10, Height: 10, Opacity: 1})
	out, err := ed.Apply()
	if err != nil {
		t.Fatal(err)
	}

	r, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	pages, _ := r.Pages()
	c := mustContent(t, r, pages[0])
	qf, qm, mf, mm := scanContentNesting(c)
	if qf != 0 || qm < 0 || mf != 0 || mm < 0 {
		t.Errorf("output not well-formed: q/Q final=%d min=%d, MC final=%d min=%d\n%s",
			qf, qm, mf, mm, c)
	}
}

func mustContent(t *testing.T, r *Reader, page Dict) []byte {
	t.Helper()
	c, err := r.PageContent(page)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSearchText(t *testing.T) {
	data := testPDF(t, "Invoice Total: $500", "Company: Acme Corp")

	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}

	results := doc.Search("Acme Corp")
	if len(results) == 0 {
		t.Fatal("expected to find 'Acme Corp'")
	}
	r := results[0]
	if r.Page != 0 {
		t.Errorf("expected page 0, got %d", r.Page)
	}
	if r.Rect.Width <= 0 || r.Rect.Height <= 0 {
		t.Errorf("expected positive rect, got %+v", r.Rect)
	}
}

func TestSearchMultipleResults(t *testing.T) {
	data := testPDF(t,
		"Item: Widget A - Price: $10",
		"Item: Widget B - Price: $20",
		"Item: Widget C - Price: $30",
	)

	doc, _ := OpenBytes(data)
	results := doc.Search("Widget")
	if len(results) < 3 {
		t.Errorf("expected at least 3 results, got %d", len(results))
	}
}

func TestSearchNotFound(t *testing.T) {
	data := testPDF(t, "Hello World")
	doc, _ := OpenBytes(data)

	results := doc.Search("NONEXISTENT_STRING")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchAcrossPages(t *testing.T) {
	data := testMultiPagePDF(t, "First page text", "Second page target", "Third page text")
	doc, _ := OpenBytes(data)

	results := doc.Search("target")
	if len(results) == 0 {
		t.Fatal("expected to find 'target'")
	}
	if results[0].Page != 1 {
		t.Errorf("expected page 1, got %d", results[0].Page)
	}
}

func TestTextOverlay(t *testing.T) {
	data := testPDF(t, "Original Content")

	ed := NewEditor(data)
	ed.AddText(TextOverlay{
		Page:     0,
		X:        100,
		Y:        50,
		Text:     "APPROVED",
		FontSize: 24,
		R:        0, G: 0.5, B: 0,
	})

	result, err := ed.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	doc, err := OpenBytes(result)
	if err != nil {
		t.Fatalf("opening result: %v", err)
	}

	if doc.NumPages() != 1 {
		t.Errorf("page count: got %d, want 1", doc.NumPages())
	}

	text, _ := doc.Page(0).Text()
	if !strings.Contains(text, "APPROVED") {
		t.Error("overlay text 'APPROVED' not found")
	}
	if !strings.Contains(text, "Original Content") {
		t.Error("original text missing")
	}
}

func TestRedactText(t *testing.T) {
	data := testPDF(t, "Secret: ABC123", "Public info here")

	ed := NewEditor(data)
	err := ed.RedactText("ABC123", 0, 0, 0)
	if err != nil {
		t.Fatalf("RedactText: %v", err)
	}

	if len(ed.redactions) == 0 {
		t.Fatal("expected redaction regions")
	}

	result, err := ed.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	doc, err := OpenBytes(result)
	if err != nil {
		t.Fatalf("opening result: %v", err)
	}
	if doc.NumPages() != 1 {
		t.Errorf("page count: got %d, want 1", doc.NumPages())
	}
}

func TestRedactRegion(t *testing.T) {
	data := testPDF(t, "Some content on the page")

	ed := NewEditor(data)
	ed.Redact(RedactRegion{
		Page: 0,
		Rect: Rect{X: 50, Y: 740, Width: 200, Height: 20},
		R:    1, G: 0, B: 0,
	})

	result, err := ed.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	doc, err := OpenBytes(result)
	if err != nil {
		t.Fatalf("opening result: %v", err)
	}
	if doc.NumPages() != 1 {
		t.Errorf("page count: got %d, want 1", doc.NumPages())
	}
}

// buildRawPDF creates a single-page PDF from a page Dict built by the caller.
// Handles Pages/Catalog boilerplate; the caller controls page content.
func buildRawPDF(t *testing.T, pageFn func(w *Writer, pagesRef Ref) Dict) []byte {
	t.Helper()
	w := NewWriter()
	pagesRef := w.AllocRef()
	catalogRef := w.AllocRef()
	pageRef := w.AllocRef()

	pd := pageFn(w, pagesRef)
	w.WriteObject(pageRef, pd)
	w.WriteObject(pagesRef, Dict{
		"Type": Name("Pages"), "Kids": Array{pageRef}, "Count": 1,
	})
	w.WriteObject(catalogRef, Dict{"Type": Name("Catalog"), "Pages": pagesRef})
	data, err := w.Finish(catalogRef)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// buildOverlayTestPDF creates a single-page PDF with configurable indirection
// for Resources and Font. When refResources/refFont is true, that dict is
// stored as an indirect object (Ref); otherwise it's inline.
func buildOverlayTestPDF(t *testing.T, refResources, refFont bool) []byte {
	t.Helper()
	return buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		fontRef := w.AllocRef()
		contentRef := w.AllocRef()

		w.WriteObject(fontRef, Dict{
			"Type": Name("Font"), "Subtype": Name("Type1"), "BaseFont": Name("Helvetica"),
		})

		var fontDictVal any
		if refFont {
			fontDictRef := w.AllocRef()
			w.WriteObject(fontDictRef, Dict{Name("F1"): fontRef})
			fontDictVal = fontDictRef
		} else {
			fontDictVal = Dict{Name("F1"): fontRef}
		}

		resDict := Dict{"Font": fontDictVal}
		var resVal any
		if refResources {
			resRef := w.AllocRef()
			w.WriteObject(resRef, resDict)
			resVal = resRef
		} else {
			resVal = resDict
		}

		w.WriteStream(contentRef, Dict{}, []byte("BT /F1 12 Tf 72 750 Td (Original Text) Tj ET"))
		return Dict{
			"Type": Name("Page"), "Parent": pagesRef,
			"MediaBox":  Array{0, 0, 612, 792},
			"Resources": resVal, "Contents": contentRef,
		}
	})
}

// verifyOverlayFonts checks that the output PDF's page has both F1 and the
// overlay font in its Resources/Font dict.
func verifyOverlayFonts(t *testing.T, result []byte) {
	t.Helper()
	reader, err := Open(result)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := reader.Pages()
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("page count: got %d, want 1", len(pages))
	}
	resObj, ok := reader.ResolveDict(pages[0]["Resources"])
	if !ok {
		t.Fatal("page has no Resources dict")
	}
	fontDictObj, ok := reader.ResolveDict(resObj["Font"])
	if !ok {
		t.Fatal("Resources has no Font dict")
	}
	if _, ok := fontDictObj[Name("F1")]; !ok {
		keys := make([]string, 0, len(fontDictObj))
		for k := range fontDictObj {
			keys = append(keys, string(k))
		}
		t.Errorf("original font F1 lost; Font dict keys: %v", keys)
	}
	if _, ok := fontDictObj[Name("F_gopdf_overlay")]; !ok {
		t.Error("overlay font F_gopdf_overlay missing")
	}
}

// TestOverlayPreservesRefResources covers all combinations of inline vs
// indirect Resources and Font dicts. Regression test for a bug where
// ensureOverlayFont replaced Ref-typed Resources with an empty Dict.
func TestOverlayPreservesRefResources(t *testing.T) {
	tests := []struct {
		name         string
		refResources bool
		refFont      bool
	}{
		{"both_refs", true, true},
		{"ref_resources_inline_font", true, false},
		{"inline_resources_ref_font", false, true},
		{"both_inline", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := buildOverlayTestPDF(t, tt.refResources, tt.refFont)
			ed := NewEditor(data)
			ed.AddText(TextOverlay{
				Page: 0, X: 72, Y: 700, Text: "OVERLAY", FontSize: 14,
			})
			result, err := ed.Apply()
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			verifyOverlayFonts(t, result)
		})
	}
}

// TestOverlayNoResources verifies overlay works on a page with no Resources.
func TestOverlayNoResources(t *testing.T) {
	data := buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		contentRef := w.AllocRef()
		w.WriteStream(contentRef, Dict{}, []byte("% empty content"))
		return Dict{
			"Type": Name("Page"), "Parent": pagesRef,
			"MediaBox": Array{0, 0, 612, 792},
			"Contents": contentRef,
		}
	})

	ed := NewEditor(data)
	ed.AddText(TextOverlay{
		Page: 0, X: 72, Y: 700, Text: "STAMP", FontSize: 14,
	})
	result, err := ed.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	reader, err := Open(result)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := reader.Pages()
	if err != nil {
		t.Fatal(err)
	}
	resObj, ok := reader.ResolveDict(pages[0]["Resources"])
	if !ok {
		t.Fatal("page has no Resources dict after overlay")
	}
	fontDictObj, ok := reader.ResolveDict(resObj["Font"])
	if !ok {
		t.Fatal("Resources has no Font dict after overlay")
	}
	if _, ok := fontDictObj[Name("F_gopdf_overlay")]; !ok {
		t.Error("overlay font missing")
	}
}

func TestOverlayAndRedactCombined(t *testing.T) {
	data := testPDF(t, "Old Reference: REF-001")

	ed := NewEditor(data)
	ed.RedactText("REF-001", 1, 1, 1) // white redaction
	ed.AddText(TextOverlay{
		Page: 0, X: 72, Y: 600,
		Text: "NEW-REF-999", FontSize: 12,
	})

	result, err := ed.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	doc, _ := OpenBytes(result)
	text, _ := doc.Page(0).Text()
	if !strings.Contains(text, "NEW-REF-999") {
		t.Error("overlay text not found")
	}
}
