package pdf

import "testing"

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
