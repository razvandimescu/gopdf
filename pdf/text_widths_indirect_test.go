package pdf

import (
	"bytes"
	"fmt"
	"math"
	"testing"
)

// buildRawPDFFromBodies assembles a PDF from already-formatted object bodies (each
// including its own "N 0 obj ... endobj\n" wrapper) plus a matching
// classic (non-stream) xref table and trailer. Building offsets this way,
// rather than hand-computing byte positions, keeps the fixture correct as
// object bodies change.
func buildRawPDFFromBodies(bodies []string, rootObj int) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")

	offsets := make([]int64, len(bodies))
	for i, b := range bodies {
		offsets[i] = int64(buf.Len())
		buf.WriteString(b)
	}

	xrefOffset := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(bodies)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF",
		len(bodies)+1, rootObj, xrefOffset)

	return buf.Bytes()
}

// buildIndirectWidthsPDF builds a single-page PDF whose Type1 font
// dictionary stores /Widths and /FirstChar as indirect references — the
// pattern pdfTeX/hyperref commonly emit. Regression fixture for the bug
// where Dict accessors (Array/Int/Float) never resolved indirect
// references, silently dropping the width table and falling back to a
// flat 0.6em guess for every glyph (issue #705 in web-researcher-mcp).
// /FirstChar is an indirect int (object 6), /Widths an indirect array
// (object 7).
func buildIndirectWidthsPDF() []byte {
	content := "BT /F1 100 Tf 72 700 Td (A) Tj ET"

	bodies := []string{
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n",
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n",
		"3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] " +
			"/Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>\nendobj\n",
		"4 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /CustomFont1 " +
			"/FirstChar 6 0 R /LastChar 65 /Widths 7 0 R >>\nendobj\n",
		fmt.Sprintf("5 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(content), content),
		"6 0 obj\n65\nendobj\n",
		"7 0 obj\n[2000]\nendobj\n",
	}
	return buildRawPDFFromBodies(bodies, 1)
}

// buildIndirectCIDWidthsPDF builds a single-page PDF with a Type0/CID font
// whose /DW, /W, and /DescendantFonts are all indirect references —
// same defect class as buildIndirectWidthsPDF, different (composite-font)
// code path in the same function.
func buildIndirectCIDWidthsPDF() []byte {
	content := "BT /F1 100 Tf 72 700 Td <0000> Tj ET"

	bodies := []string{
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n",
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n",
		"3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] " +
			"/Resources << /Font << /F1 4 0 R >> >> /Contents 9 0 R >>\nendobj\n",
		"4 0 obj\n<< /Type /Font /Subtype /Type0 /BaseFont /CustomCID " +
			"/Encoding /Identity-H /DescendantFonts 8 0 R >>\nendobj\n",
		"5 0 obj\n<< /Type /Font /Subtype /CIDFontType2 /BaseFont /CustomCID " +
			"/DW 6 0 R /W 7 0 R >>\nendobj\n",
		"6 0 obj\n1000\nendobj\n",
		"7 0 obj\n[0 [2000]]\nendobj\n",
		"8 0 obj\n[5 0 R]\nendobj\n",
		fmt.Sprintf("9 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(content), content),
	}
	return buildRawPDFFromBodies(bodies, 1)
}

func TestFontWidths_IndirectSimpleFontWidthsArray(t *testing.T) {
	doc, err := OpenBytes(buildIndirectWidthsPDF())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	spans, err := doc.Page(0).TextSpans()
	if err != nil {
		t.Fatalf("TextSpans: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d: %+v", len(spans), spans)
	}

	// FirstChar (obj 6) resolves to 65 ('A'); Widths[0] (obj 7) is 2000/1000em.
	// At Tf 100, glyph advance = 2.0 * 100 = 200pt. Pre-fix, the indirect
	// /Widths and /FirstChar never resolved, so the font's width table stayed
	// empty and every glyph fell back to the hardcoded 0.6em default,
	// producing an advance of 60pt instead of 200pt.
	got := spans[0].EndX - spans[0].X
	want := 200.0
	if math.Abs(got-want) > 0.01 {
		t.Errorf("glyph advance = %v, want %v (indirect /Widths or /FirstChar not resolved)", got, want)
	}
}

func TestFontWidths_IndirectCIDWidths(t *testing.T) {
	doc, err := OpenBytes(buildIndirectCIDWidthsPDF())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	spans, err := doc.Page(0).TextSpans()
	if err != nil {
		t.Fatalf("TextSpans: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d: %+v", len(spans), spans)
	}

	// CID 0's /W entry (obj 7, reached through the indirect /DescendantFonts
	// array obj 8) is 2000/1000em; at Tf 100, advance = 200pt. Pre-fix, the
	// indirect /W and /DW were never resolved, so cidCharWidth fell back to
	// the hardcoded 0.6em default (60pt) for every CID.
	got := spans[0].EndX - spans[0].X
	want := 200.0
	if math.Abs(got-want) > 0.01 {
		t.Errorf("glyph advance = %v, want %v (indirect /W, /DW, or /DescendantFonts not resolved)", got, want)
	}
}
