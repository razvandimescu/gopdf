package pdf

import (
	"strings"
	"testing"
)

// fontPDF builds a one-page PDF whose content stream is content, with font
// available as /F1.
func fontPDF(t *testing.T, font Dict, content string) []byte {
	t.Helper()
	return buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		fontRef, contentRef := w.AllocRef(), w.AllocRef()
		w.WriteObject(fontRef, font)
		w.WriteStream(contentRef, Dict{}, []byte(content))
		return fontPage(pagesRef, fontRef, contentRef)
	})
}

// A code is read through the font's encoding, whether the font names it or
// implies it by naming none, and under a ToUnicode map or Differences that
// leave the code out. A code the encoding does not define reads as nothing.
func TestCodesDecodeThroughTheEncoding(t *testing.T) {
	const show = "BT /F1 12 Tf 72 700 Td (\x27\xb3\xb0\xc9\x81) Tj ET"
	font := func(subtype string, encoding any) Dict {
		d := Dict{"Type": Name("Font"), "Subtype": Name(subtype), "BaseFont": Name("Helvetica")}
		if encoding != nil {
			d["Encoding"] = encoding
		}
		return d
	}

	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"StandardEncoding, implied", fontPDF(t, font("Type1", nil), show), "’‡"},
		{"WinAnsiEncoding", fontPDF(t, font("Type1", Name("WinAnsiEncoding")), show), "'³°É•"},
		{"MacRomanEncoding", fontPDF(t, font("Type1", Name("MacRomanEncoding")), show), "'≥∞…Å"},
		{"WinAnsiEncoding, implied by TrueType", fontPDF(t, font("TrueType", nil), show), "'³°É•"},
		{"ToUnicode lacking them", cmapPDF(t, "<41> <0041>", show), "’‡"},
		{"Differences lacking them", fontPDF(t, font("Type1", Dict{
			"BaseEncoding": Name("WinAnsiEncoding"),
			"Differences":  Array{1, Name("f_f_i"), Name("one.oldstyle"), Name("g12")},
		}), "BT /F1 12 Tf 72 700 Td (\x01\x02\x03\x27\xb3\xb0\xc9\x81) Tj ET"), "ffi1'³°É•"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := soleSpan(t, tc.data).Text; got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// Every glyph name the base encodings use is one the glyph list knows.
func TestBaseEncodingGlyphNamesAreListed(t *testing.T) {
	for encoding, names := range baseEncodings {
		for code, name := range names {
			if _, ok := glyphMap[name]; !ok {
				t.Errorf("%s 0x%02X: %q is not in the glyph list", encoding, code, name)
			}
		}
	}
}

// Image data is binary and holds EI by chance. Taking a false EI for the
// image's end sends the lexer into the data, and refusing a true one sends the
// rest of the stream into the image; either way the page's text after it was
// lost.
func TestInlineImageEnds(t *testing.T) {
	// 15 bytes holding an EI that reads as content for three operators.
	const falseEI = "\x01 EI Q q Q >\xff\xff\xff"
	for _, tc := range []struct{ name, image string }{
		{"unfiltered gray", "BI /W 15 /H 1 /BPC 8 /CS /G ID " + falseEI + " EI Q"},
		{"unfiltered RGB", "BI /W 5 /H 1 /BPC 8 /CS /RGB ID " + falseEI + " EI Q"},
		{"unfiltered mask, rows padded to bytes", "BI /IM true /W 20 /H 5 ID " + falseEI + " EI Q"},
		{"filtered, binary after a false EI", "BI /F /Fl ID \x01\fEI\x00>\x96\x02 EI Q"},
		{"filtered, an operator then binary after a false EI", "BI /F /Fl ID \x01 EI n >\x96 EI Q"},
		{"filtered, a string after a false EI", "BI /F /Fl ID \x01 EI (\x96\x02 EI Q"},
		{"filtered, a UTF-8 comment after EI", "BI /F /Fl ID \x80 EI Q % \xe2\x80\x94 note\n"},
		{"filtered, a UTF-8 name after EI", "BI /F /Fl ID \x80 EI Q /Caf\xc3\xa9 BMC"},
		{"filtered, another image after EI", "BI /F /Fl ID \x80 EI Q BI /F /Fl ID \x81 EI"},
		{"filtered, a long comment after EI", "BI /F /Fl ID \x80 EI Q % " + strings.Repeat("x", 300) + "\n"},
		{"filtered, a long property list after EI", "BI /F /Fl ID \x80 EI Q /Span <</ActualText (" + strings.Repeat("x", 300) + ")>> BDC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The image sits in a scaled q…Q, so text read with the Q lost is misplaced.
			data := contentPDF(t, "q 2 0 0 2 0 0 cm "+tc.image+"\nBT /F1 12 Tf 72 700 Td (after the image) Tj ET")
			if got := soleSpan(t, data); got.Text != "after the image" || got.X != 72 {
				t.Errorf("got %q at x=%g", got.Text, got.X)
			}
		})
	}
}

// glyphRunPDF draws "Revision" one string per glyph, as generators that
// position every glyph do, on a page with /Rotate 90: the text matrix turns
// it back, so it reads left to right as displayed.
func glyphRunPDF(t *testing.T) []byte {
	t.Helper()
	return buildRawPDF(t, func(w *Writer, pagesRef Ref) Dict {
		fontRef, contentRef := w.AllocRef(), w.AllocRef()
		w.WriteObject(fontRef, Dict{
			"Type": Name("Font"), "Subtype": Name("Type1"), "BaseFont": Name("Helvetica"),
		})
		w.WriteStream(contentRef, Dict{}, []byte(
			"BT /F1 12 Tf 0 1 -1 0 300 100 Tm [(R)(e)(v)(i)(s)(i)(o)(n)] TJ ET"))
		page := fontPage(pagesRef, fontRef, contentRef)
		page["Rotate"] = 90
		return page
	})
}

// A span ends where the pen left it, in the space it starts in. Carried out of
// the space it was drawn in by its start alone, it ended where it began, and
// a word drawn a glyph at a time read as "R e v i s i o n".
func TestGlyphRunsEndWhereThePenLeft(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"rotated page", glyphRunPDF(t)},
		{"form XObject", formPDF(t, "BT /F1 12 Tf 10 10 Td [(R)(e)(v)(i)(s)(i)(o)(n)] TJ ET", [2]float64{200, 0})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := OpenBytes(tc.data)
			if err != nil {
				t.Fatal(err)
			}
			if text := docText(t, doc); strings.TrimSpace(text) != "Revision" {
				t.Errorf("got %q, want Revision", text)
			}
		})
	}
}
