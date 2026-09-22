package pdf

import (
	"fmt"
	"math"
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

// scaledTf sets Tf 327.68 and scales it to 9.36pt through the CTM.
const scaledTf = "q 0.75 0 0 0.75 0 0 cm 0.038086 0 0 0.038086 0 0 cm " +
	"BT /F1 327.68 Tf 1 0 0 1 2520 24500 Tm "

// A word gap is judged against the em as drawn along the baseline, and
// redaction has to judge it the same way, or Page.Search finds a phrase
// RemoveText cannot.
func TestWordGapsFollowTheDrawnSize(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
	}{
		// 0.7pt of tracking between letters at 8pt, and a word gap after them.
		{"tracked letters",
			"BT /F1 8 Tf 72 700 Td [(S) -88 (s) -88 (_) -88 (4) -88 (0) -300 (m) -88 (m)] TJ ET",
			"Ss_40 mm"},
		// A 2.8pt gap is a word break at 9.36pt.
		{"size scaled by the CTM", scaledTf + "[(1.) -300 (System)] TJ ET Q", "1. System"},
		// A 3pt gap on a 12pt-wide em, which is 24pt tall.
		{"stretched vertically",
			"q 1 0 0 2 0 0 cm BT /F1 12 Tf 72 300 Td [(Hello) -250 (World)] TJ ET Q",
			"Hello World"},
		// A 1.5pt gap on an em condensed to 6pt wide.
		{"condensed by Tz",
			"BT /F1 12 Tf 50 Tz 72 700 Td [(Hello) -250 (World)] TJ ET",
			"Hello World"},
		// Where a MediaBox centred on the origin puts half the page.
		{"left of the origin",
			"BT /F1 12 Tf -500 700 Td [(RAI981) -300 (WCH981)] TJ ET",
			"RAI981 WCH981"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := contentPDF(t, tc.content)
			doc, err := OpenBytes(data)
			if err != nil {
				t.Fatal(err)
			}
			if text := strings.TrimSpace(docText(t, doc)); text != tc.want {
				t.Fatalf("got %q, want %q", text, tc.want)
			}
			if text := docText(t, removeText(t, data, tc.want)); strings.TrimSpace(text) != "" {
				t.Errorf("removal assembled the page differently and left %q", text)
			}
		})
	}
}

func TestFontSizeIsTheDrawnSize(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    float64
	}{
		{"scaled by the CTM", scaledTf + "(System) Tj ET Q", 9.36},
		{"rotated by the text matrix", "BT /F1 12 Tf 0 1 -1 0 300 100 Tm (System) Tj ET", 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := soleSpan(t, contentPDF(t, tc.content)).FontSize; math.Abs(got-tc.want) > 0.01 {
				t.Errorf("FontSize %.3f, want %.2f", got, tc.want)
			}
		})
	}
}

// Text drawn at an angle reads along its own baseline, a glyph at a time or
// not, and removal assembles it the same way.
func TestRotatedTextReadsAlongItsBaseline(t *testing.T) {
	const level = "BT /F1 10 Tf 72 750 Td (Level) Tj ET "
	const labels = "[(S)(A)(N)(-)(8)(6)(1) -300 (PP00-837)] TJ ET"
	for _, tc := range []struct{ name, tm string }{
		{"up the page", "0 1 -1 0 300 100 Tm "},
		{"down the page", "0 -1 1 0 300 700 Tm "},
		{"upside down", "-1 0 0 -1 500 400 Tm "},
		{"slanted", "0.866 0.5 -0.5 0.866 100 100 Tm "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := contentPDF(t, level+"BT /F1 10 Tf "+tc.tm+labels)
			doc, err := OpenBytes(data)
			if err != nil {
				t.Fatal(err)
			}
			if text, want := docText(t, doc), "Level\nSAN-861 PP00-837"; strings.TrimSpace(text) != want {
				t.Fatalf("got %q, want %q", text, want)
			}
			if text := docText(t, removeText(t, data, "SAN-861 PP00-837")); strings.TrimSpace(text) != "Level" {
				t.Errorf("removal assembled the page differently and left %q", text)
			}
		})
	}
}

// A long baseline between whole degrees is still one line: 40 glyphs drawn
// one at a time at 30.49° climb 2pt off a 30° baseline, twice the tolerance.
func TestSlantedBaselineIsOneLine(t *testing.T) {
	cos, sin := math.Cos(30.49*math.Pi/180), math.Sin(30.49*math.Pi/180)
	var tj, want strings.Builder
	for i := range 40 {
		c := string(rune('A' + i%26))
		tj.WriteString("(" + c + ")")
		want.WriteString(c)
	}
	data := contentPDF(t, fmt.Sprintf("BT /F1 10 Tf %.5f %.5f %.5f %.5f 100 100 Tm [%s] TJ ET", cos, sin, -sin, cos, tj.String()))
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if text := strings.TrimSpace(docText(t, doc)); text != want.String() {
		t.Errorf("got %q, want %q", text, want.String())
	}
}

// Superscripts and subscripts are set on their line, however far off its
// baseline, and removal reads them there too. Text of the same size stays
// apart however close, and so does smaller text that does not sit against
// the line's own.
func TestRaisedTextJoinsItsLine(t *testing.T) {
	// Helvetica: "3m" at 11pt is 15.279 wide, "3" at 7pt 3.892, "H" at 11pt
	// 7.942, "Kit" at 15pt 17.505, "Ref" at 10pt 15.56.
	for _, tc := range []struct{ name, content, want string }{
		{"superscript",
			"BT /F1 11 Tf 72 700 Td (3m) Tj ET BT /F1 7 Tf 87.28 704 Td (3) Tj ET BT /F1 11 Tf 91.17 700 Td (/hr) Tj ET",
			"3m3/hr"},
		{"subscript",
			"BT /F1 11 Tf 72 700 Td (H) Tj ET BT /F1 7 Tf 79.95 697 Td (2) Tj ET BT /F1 11 Tf 83.85 700 Td (O) Tj ET",
			"H2O"},
		// "x" at 12pt is 6 wide, "2" at 8pt 4.448; the 3 is set on the 2.
		{"nested superscripts",
			"BT /F1 12 Tf 72 700 Td (x) Tj ET BT /F1 8 Tf 78 704 Td (2) Tj ET BT /F1 5 Tf 82.45 707 Td (3) Tj ET",
			"x23"},
		{"same size, close",
			"BT /F1 11 Tf 72 700 Td (Alpha) Tj ET BT /F1 11 Tf 72 704 Td (Beta) Tj ET",
			"Beta\nAlpha"},
		// "Alpha" at 11pt runs from 72 to 100.1: the note is drawn across it.
		// The line far below is out of the note's reach.
		{"drawn across the line",
			"BT /F1 11 Tf 72 700 Td (Alpha) Tj ET BT /F1 7 Tf 80 703 Td (note) Tj ET BT /F1 11 Tf 72 600 Td (Far) Tj ET",
			"note\nAlpha\nFar"},
		{"caption spaced off a heading",
			"BT /F1 15 Tf 72 700 Td (Kit) Tj ET BT /F1 10 Tf 96.5 697.2 Td (Sensor) Tj ET",
			"Kit\nSensor"},
		{"a line of its own that ends against larger text",
			"BT /F1 10 Tf 72 700 Td (Ref) Tj ET BT /F1 7 Tf 40 701.5 Td (the) Tj ET BT /F1 7 Tf 87.6 701.5 Td (about) Tj ET",
			"the about\nRef"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := contentPDF(t, tc.content)
			doc, err := OpenBytes(data)
			if err != nil {
				t.Fatal(err)
			}
			var lines []string
			for _, line := range strings.Split(strings.TrimSpace(docText(t, doc)), "\n") {
				lines = append(lines, strings.Join(strings.Fields(line), " "))
			}
			if got := strings.Join(lines, "\n"); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if !strings.Contains(tc.want, "\n") {
				if text := docText(t, removeText(t, data, tc.want)); strings.TrimSpace(text) != "" {
					t.Errorf("removal assembled the page differently and left %q", text)
				}
			}
		})
	}
}

// A line's Y is the baseline of the text it is set in, not of smaller text
// that shares it.
func TestLineYIsItsTextsBaseline(t *testing.T) {
	lines := BuildLines([]TextSpan{
		{X: 72, Y: 700.8, EndX: 76, FontSize: 7, Text: "a"},
		{X: 76, Y: 700, EndX: 100, FontSize: 11, Text: "Big"},
	})
	if len(lines) != 1 || lines[0].Y != 700 {
		t.Fatalf("got %+v, want one line at 700", lines)
	}
}
