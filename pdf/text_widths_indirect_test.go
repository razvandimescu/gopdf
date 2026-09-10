package pdf

import (
	"math"
	"testing"
)

// buildIndirectWidthsPDF builds a Type1 font whose /Widths and /FirstChar are
// indirect references — the pattern pdfTeX and hyperref commonly emit.
// Regression fixture for the bug where Dict accessors never resolved indirect
// references, silently dropping the width table and falling back to a flat
// 0.6em guess for every glyph.
func buildIndirectWidthsPDF(t *testing.T) []byte {
	t.Helper()
	return buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		fontRef, contentRef := w.AllocRef(), w.AllocRef()
		firstCharRef, widthsRef := w.AllocRef(), w.AllocRef()

		w.WriteObject(firstCharRef, 65)
		w.WriteObject(widthsRef, Array{2000})
		w.WriteObject(fontRef, Dict{
			"Type": Name("Font"), "Subtype": Name("Type1"), "BaseFont": Name("CustomFont1"),
			"FirstChar": firstCharRef, "LastChar": 65, "Widths": widthsRef,
		})
		w.WriteStream(contentRef, Dict{}, []byte("BT /F1 100 Tf 72 700 Td (A) Tj ET"))

		return fontPage(pagesRef, fontRef, contentRef)
	})
}

// buildIndirectCIDWidthsPDF builds a Type0/CID font whose /DW, /W and
// /DescendantFonts are all indirect references — same defect class as
// buildIndirectWidthsPDF, different (composite-font) code path.
//
// The page shows two CIDs: CID 0, which /W covers at 2000/1000em, and CID 1,
// which falls through to /DW. /DW is 500 rather than the 1000 the spec
// defaults to, because extractText seeds dw with that default — a fixture
// declaring /DW 1000 reads the same whether the reference resolved or not.
func buildIndirectCIDWidthsPDF(t *testing.T) []byte {
	t.Helper()
	return buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		fontRef, contentRef := w.AllocRef(), w.AllocRef()
		cidRef, descendantsRef := w.AllocRef(), w.AllocRef()
		dwRef, wRef := w.AllocRef(), w.AllocRef()

		w.WriteObject(dwRef, 500)
		w.WriteObject(wRef, Array{0, Array{2000}})
		w.WriteObject(cidRef, Dict{
			"Type": Name("Font"), "Subtype": Name("CIDFontType2"), "BaseFont": Name("CustomCID"),
			"DW": dwRef, "W": wRef,
		})
		w.WriteObject(descendantsRef, Array{cidRef})
		w.WriteObject(fontRef, Dict{
			"Type": Name("Font"), "Subtype": Name("Type0"), "BaseFont": Name("CustomCID"),
			"Encoding": Name("Identity-H"), "DescendantFonts": descendantsRef,
		})
		w.WriteStream(contentRef, Dict{}, []byte("BT /F1 100 Tf 72 700 Td <00000001> Tj ET"))

		return fontPage(pagesRef, fontRef, contentRef)
	})
}

// buildBrokenDWPDF builds a Type0/CID font whose /DW is unusable: dw is
// either a reference to an object that is never written, or a value of the
// wrong type. Neither carries a width, so PDF 32000-1 table 117's default of
// 1000 must survive.
func buildBrokenDWPDF(t *testing.T, dw any) []byte {
	t.Helper()
	return buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		fontRef, cidRef, contentRef := w.AllocRef(), w.AllocRef(), w.AllocRef()

		w.WriteObject(cidRef, Dict{
			"Type": Name("Font"), "Subtype": Name("CIDFontType2"), "BaseFont": Name("CustomCID"),
			"DW": dw,
		})
		w.WriteObject(fontRef, Dict{
			"Type": Name("Font"), "Subtype": Name("Type0"), "BaseFont": Name("CustomCID"),
			"Encoding": Name("Identity-H"), "DescendantFonts": Array{cidRef},
		})
		w.WriteStream(contentRef, Dict{}, []byte("BT /F1 100 Tf 72 700 Td <0000> Tj ET"))

		return fontPage(pagesRef, fontRef, contentRef)
	})
}

// buildDifferencesPDF builds a Type1 font whose /Encoding remaps code 65 to
// /bullet, reaching the overlay either directly or through a reference.
func buildDifferencesPDF(t *testing.T, indirect bool) []byte {
	t.Helper()
	return buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		fontRef, contentRef := w.AllocRef(), w.AllocRef()

		var diffs any = Array{65, Name("bullet")}
		if indirect {
			diffRef := w.AllocRef()
			w.WriteObject(diffRef, diffs)
			diffs = diffRef
		}
		w.WriteObject(fontRef, Dict{
			"Type": Name("Font"), "Subtype": Name("Type1"), "BaseFont": Name("Helvetica"),
			"Encoding": Dict{
				"Type": Name("Encoding"), "BaseEncoding": Name("WinAnsiEncoding"),
				"Differences": diffs,
			},
		})
		w.WriteStream(contentRef, Dict{}, []byte("BT /F1 12 Tf 72 700 Td (A) Tj ET"))

		return fontPage(pagesRef, fontRef, contentRef)
	})
}

// fontPage returns a page dict binding /F1 to fontRef.
func fontPage(pagesRef, fontRef, contentRef Ref) Dict {
	return Dict{
		"Type": Name("Page"), "Parent": pagesRef,
		"MediaBox":  Array{0, 0, 612, 792},
		"Resources": Dict{"Font": Dict{Name("F1"): fontRef}},
		"Contents":  contentRef,
	}
}

// soleSpan returns the one span a fixture page carries.
func soleSpan(t *testing.T, data []byte) TextSpan {
	t.Helper()
	doc, err := OpenBytes(data)
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
	return spans[0]
}

func TestFontWidths_IndirectSimpleFontWidthsArray(t *testing.T) {
	// FirstChar resolves to 65 ('A') and Widths[0] to 2000/1000em, so at
	// Tf 100 the glyph advances 200pt. Pre-fix, the indirect /Widths and
	// /FirstChar never resolved, the width table stayed empty, and every
	// glyph fell back to the hardcoded 0.6em default: 60pt.
	span := soleSpan(t, buildIndirectWidthsPDF(t))
	got := span.EndX - span.X
	if want := 200.0; math.Abs(got-want) > 0.01 {
		t.Errorf("glyph advance = %v, want %v (indirect /Widths or /FirstChar not resolved)", got, want)
	}
}

func TestFontWidths_IndirectCIDWidths(t *testing.T) {
	// Both widths are reached through the indirect /DescendantFonts array:
	// CID 0 from /W at 2000/1000em (200pt at Tf 100) and CID 1 from /DW at
	// 500/1000em (50pt). Pre-fix, neither resolved and both glyphs took the
	// hardcoded 0.6em default, for 120pt.
	span := soleSpan(t, buildIndirectCIDWidthsPDF(t))
	got := span.EndX - span.X
	if want := 250.0; math.Abs(got-want) > 0.01 {
		t.Errorf("run advance = %v, want %v (indirect /W, /DW, or /DescendantFonts not resolved)", got, want)
	}
}

// A /DW that cannot be read is not a /DW of zero. A resolver that reports
// success for any non-nil value coerces both of these to 0 and overwrites the
// caller's default, collapsing every CID without a /W entry onto one X.
func TestFontWidths_UnreadableDWKeepsSpecDefault(t *testing.T) {
	cases := map[string]any{
		"reference to nothing": Ref{Num: 9999},
		"wrong type":           Name("Bogus"),
	}
	for name, dw := range cases {
		t.Run(name, func(t *testing.T) {
			// Default /DW of 1000/1000em at Tf 100 advances 100pt.
			span := soleSpan(t, buildBrokenDWPDF(t, dw))
			got := span.EndX - span.X
			if want := 100.0; math.Abs(got-want) > 0.01 {
				t.Errorf("glyph advance = %v, want %v (unreadable /DW overwrote the 1000 default)", got, want)
			}
		})
	}
}

// The encoding overlay is subject to the same indirection as the width
// tables, and dropping it yields the wrong characters rather than merely the
// wrong spacing.
func TestFontEncoding_IndirectDifferences(t *testing.T) {
	for name, indirect := range map[string]bool{"direct": false, "indirect": true} {
		t.Run(name, func(t *testing.T) {
			got := soleSpan(t, buildDifferencesPDF(t, indirect)).Text
			if want := "•"; got != want {
				t.Errorf("text = %q, want %q (/Differences overlay not applied)", got, want)
			}
		})
	}
}
