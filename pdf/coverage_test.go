package pdf

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func openDoc(t *testing.T) *Document {
	t.Helper()
	data := testPDF(t, "Test Document", "Hello World")
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func writeTempPDF(t *testing.T) string {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "test.pdf")
	if err := os.WriteFile(tmp, testPDF(t, "Temp"), 0644); err != nil {
		t.Fatal(err)
	}
	return tmp
}

func TestRotation_Default(t *testing.T) {
	doc := openDoc(t)
	if doc.Page(0).Rotation() != 0 {
		t.Errorf("expected rotation 0, got %d", doc.Page(0).Rotation())
	}
}

func TestMediaBox_Default(t *testing.T) {
	doc := openDoc(t)
	mb := doc.Page(0).MediaBox()
	// testPDF uses US Letter (612x792)
	if mb[2] != 612 || mb[3] != 792 {
		t.Errorf("expected US Letter mediabox, got %v", mb)
	}
}

func TestPage_Nil(t *testing.T) {
	doc := openDoc(t)
	if doc.Page(-1) != nil || doc.Page(999) != nil {
		t.Error("out-of-bounds Page should return nil")
	}
}

func TestHelveticaTextWidth(t *testing.T) {
	w := HelveticaTextWidth("Hello", 12)
	if w <= 0 || w > 100 {
		t.Errorf("unexpected width %f for 'Hello' at 12pt", w)
	}
	if HelveticaTextWidth("", 12) != 0 {
		t.Error("empty string should have zero width")
	}
}

func TestStdFontWidths_Aliases(t *testing.T) {
	// Common font aliases (ArialMT, TimesNewRomanPSMT, etc.) should resolve to metrics.
	tests := []struct {
		name    string
		wantNil bool
	}{
		{"Helvetica", false},
		{"ArialMT", false},
		{"Arial-BoldMT", false},
		{"Arial-ItalicMT", false},
		{"Helvetica-Bold", false},
		{"Helvetica-Oblique", false},
		{"Times-Roman", false},
		{"TimesNewRomanPSMT", false},
		{"Courier", false},
		{"CourierNewPSMT", false},
		{"ABCDEF+ArialMT", false}, // subset prefix
		{"UnknownFont", true},
	}
	for _, tt := range tests {
		w := StdFontWidths(tt.name)
		if tt.wantNil && w != nil {
			t.Errorf("StdFontWidths(%q) should be nil", tt.name)
		}
		if !tt.wantNil && w == nil {
			t.Errorf("StdFontWidths(%q) returned nil, want metrics", tt.name)
		}
	}
	// Arial should return identical widths to Helvetica.
	helv := StdFontWidths("Helvetica")
	arial := StdFontWidths("ArialMT")
	if helv == nil || arial == nil {
		t.Fatal("nil widths")
	}
	for code, hw := range helv {
		if arial[code] != hw {
			t.Errorf("ArialMT width[%d] = %f, want %f", code, arial[code], hw)
		}
	}

	// Times-Italic and Times-BoldItalic intentionally alias the upright
	// Times metrics (see stdfonts.go). Pin via pointer identity on the
	// internal accessor so the approximation isn't "fixed" by accident.
	pin := func(alias, target string) {
		t.Helper()
		ap := reflect.ValueOf(stdFontWidths(alias)).Pointer()
		tp := reflect.ValueOf(stdFontWidths(target)).Pointer()
		if ap == 0 || tp == 0 {
			t.Fatalf("%s or %s resolved to nil", alias, target)
		}
		if ap != tp {
			t.Errorf("%s should alias %s widths (intentional approximation)", alias, target)
		}
	}
	pin("Times-Italic", "Times-Roman")
	pin("Times-BoldItalic", "Times-Bold")

	// Public API must return a fresh copy that the caller can mutate
	// without corrupting subsequent calls.
	w1 := StdFontWidths("Helvetica")
	w1[0x41] = 999
	w2 := StdFontWidths("Helvetica")
	if w2[0x41] == 999 {
		t.Error("StdFontWidths must return a defensive copy, not the shared map")
	}
}

func TestParseBfRange_NoOverflow(t *testing.T) {
	// Range ending at 0xFFFF must not cause uint16 wraparound.
	m := make(map[uint16]string)
	parseBfRange("<FFFD> <FFFF> [<0041> <0042> <0043>]", m)
	if len(m) != 3 {
		t.Fatalf("expected 3 mappings, got %d", len(m))
	}
	if m[0xFFFD] != "A" {
		t.Errorf("0xFFFD: got %q, want A", m[0xFFFD])
	}
	if m[0xFFFE] != "B" {
		t.Errorf("0xFFFE: got %q, want B", m[0xFFFE])
	}
	if m[0xFFFF] != "C" {
		t.Errorf("0xFFFF: got %q, want C", m[0xFFFF])
	}
}

func TestParseBfRange_ContiguousNoOverflow(t *testing.T) {
	// Contiguous range ending at 0xFFFF.
	m := make(map[uint16]string)
	parseBfRange("<FFFE> <FFFF> <0058>", m)
	if len(m) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(m))
	}
	if m[0xFFFE] != "X" {
		t.Errorf("0xFFFE: got %q, want X", m[0xFFFE])
	}
	if m[0xFFFF] != "Y" {
		t.Errorf("0xFFFF: got %q, want Y", m[0xFFFF])
	}
}

func TestDict_String(t *testing.T) {
	d := Dict{"Key": "value"}
	s, ok := d.String("Key")
	if !ok || s != "value" {
		t.Errorf("String: got %q, %v", s, ok)
	}
	if _, ok = d.String("Missing"); ok {
		t.Error("missing key should return false")
	}
}

func TestDict_Stream(t *testing.T) {
	st := &Stream{Dict: Dict{}, Data: []byte("test")}
	d := Dict{"S": st}
	got, ok := d.Stream("S")
	if !ok || got != st {
		t.Error("Stream lookup failed")
	}
	if _, ok = d.Stream("Missing"); ok {
		t.Error("missing key should return false")
	}
}

func TestDict_Float(t *testing.T) {
	d := Dict{"F": 3.14, "I": 42}
	if f, ok := d.Float("F"); !ok || f != 3.14 {
		t.Errorf("Float(F): %v, %v", f, ok)
	}
	if f, ok := d.Float("I"); !ok || f != 42 {
		t.Errorf("Float(I): %v, %v", f, ok)
	}
	if _, ok := d.Float("Missing"); ok {
		t.Error("missing key should return false")
	}
}

func TestAsInt(t *testing.T) {
	if asInt(42) != 42 {
		t.Error("int")
	}
	if asInt(3.7) != 3 {
		t.Error("float64")
	}
	if asInt("x") != 0 {
		t.Error("string should return 0")
	}
}

func TestParser_Lexer(t *testing.T) {
	p := NewParser([]byte("123"))
	if p.Lexer() == nil {
		t.Error("Lexer should not be nil")
	}
}

func TestReader_Trailer_XRef(t *testing.T) {
	data := testPDF(t, "Hello")
	r, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	tr := r.Trailer()
	if tr == nil {
		t.Error("Trailer should not be nil")
	}
	if _, ok := tr.Ref("Root"); !ok {
		t.Error("Trailer missing Root ref")
	}
	if len(r.XRef()) == 0 {
		t.Error("XRef should have entries")
	}
}

// TestReader_TrailingPadding verifies we tolerate large amounts of trailing
// garbage after %%EOF (some uploaders zero-pad PDFs to a sector boundary).
func TestReader_TrailingPadding(t *testing.T) {
	data := testPDF(t, "Hello")
	padded := append(append([]byte{}, data...), make([]byte, 60*1024)...)
	r, err := Open(padded)
	if err != nil {
		t.Fatalf("Open with trailing padding: %v", err)
	}
	if r.Trailer() == nil {
		t.Error("Trailer should not be nil after padded read")
	}
}

func TestDecodeASCIIHex(t *testing.T) {
	got, err := decodeASCIIHex([]byte("48656C6C6F>"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "Hello" {
		t.Errorf("got %q, want Hello", got)
	}
	got, _ = decodeASCIIHex([]byte("4>"))
	if len(got) != 1 || got[0] != 0x40 {
		t.Errorf("odd nibble: got %x", got)
	}
}

func TestPaethPredictor(t *testing.T) {
	tests := []struct {
		a, b, c, want byte
	}{
		{1, 2, 3, 1},     // p=0, pa=1 → a
		{0, 5, 0, 5},     // p=5, pb=0 → b
		{5, 0, 5, 0},     // p=0, pb=0 → b
		{10, 10, 10, 10}, // all equal → a
	}
	for _, tt := range tests {
		got := paethPredictor(tt.a, tt.b, tt.c)
		if got != tt.want {
			t.Errorf("paethPredictor(%d,%d,%d) = %d, want %d", tt.a, tt.b, tt.c, got, tt.want)
		}
	}
}

func TestDecodeActualText(t *testing.T) {
	if decodeActualText("hello") != "hello" {
		t.Error("plain text passthrough failed")
	}
	utf16 := string([]byte{0xFE, 0xFF, 0x00, 0x41, 0x00, 0x42})
	if decodeActualText(utf16) != "AB" {
		t.Errorf("UTF-16BE: got %q, want AB", decodeActualText(utf16))
	}
}

func TestGlyphToString(t *testing.T) {
	if glyphToString("space") != " " {
		t.Errorf("space glyph: got %q", glyphToString("space"))
	}
	if glyphToString("uni0041") != "A" {
		t.Errorf("uni0041: got %q", glyphToString("uni0041"))
	}
	if glyphToString("X") != "X" {
		t.Errorf("single char: got %q", glyphToString("X"))
	}
	if glyphToString("unknownglyph") != "unknownglyph" {
		t.Errorf("unknown: got %q", glyphToString("unknownglyph"))
	}
}

func TestDocument_Text(t *testing.T) {
	doc := openDoc(t)
	text, err := doc.Text()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Test Document") {
		t.Errorf("Text() = %q, missing 'Test Document'", text)
	}
}

func TestMergeFiles(t *testing.T) {
	tmp := writeTempPDF(t)
	merged, err := MergeFiles(tmp, tmp)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := OpenBytes(merged)
	if err != nil {
		t.Fatal(err)
	}
	if doc.NumPages() != 2 {
		t.Errorf("merged pages: got %d, want 2", doc.NumPages())
	}
}

func TestMerger_AddFile(t *testing.T) {
	tmp := writeTempPDF(t)
	m := NewMerger()
	if err := m.AddFile(tmp, 0); err != nil {
		t.Fatal(err)
	}
	result, err := m.Merge()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := OpenBytes(result)
	if err != nil {
		t.Fatal(err)
	}
	if doc.NumPages() != 1 {
		t.Errorf("pages: got %d, want 1", doc.NumPages())
	}
}

func TestNewEditorFromFile(t *testing.T) {
	tmp := writeTempPDF(t)
	ed, err := NewEditorFromFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ed.Apply()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := OpenBytes(result)
	if err != nil {
		t.Fatal(err)
	}
	if doc.NumPages() != 1 {
		t.Error("editor output should have 1 page")
	}
}

func TestBuildLines_Spacing(t *testing.T) {
	spans := []TextSpan{
		{X: 10, Y: 100, EndX: 50, FontSize: 12, Text: "Hello"},
		{X: 100, Y: 100, EndX: 140, FontSize: 12, Text: "World"},
		{X: 142, Y: 100, EndX: 145, FontSize: 12, Text: "!"},
		{X: 10, Y: 80, EndX: 60, FontSize: 12, Text: "Line Two"},
	}
	lines := BuildLines(spans)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	// Big gap (50→100) should produce multiple spaces; small gap (140→142) a single space.
	if !strings.Contains(lines[0].Text, "Hello") || !strings.Contains(lines[0].Text, "World") {
		t.Errorf("line 0 = %q, expected Hello...World", lines[0].Text)
	}
	if lines[1].Text != "Line Two" {
		t.Errorf("line 1 = %q, want 'Line Two'", lines[1].Text)
	}
}

func TestFindTable_AutoDetectPath(t *testing.T) {
	spans := []TextSpan{
		{X: 50, Y: 700, EndX: 80, FontSize: 12, Text: "A"},
		{X: 200, Y: 700, EndX: 230, FontSize: 12, Text: "B"},
		{X: 350, Y: 700, EndX: 380, FontSize: 12, Text: "C"},
		{X: 50, Y: 680, EndX: 80, FontSize: 12, Text: "1"},
		{X: 200, Y: 680, EndX: 230, FontSize: 12, Text: "2"},
		{X: 350, Y: 680, EndX: 380, FontSize: 12, Text: "3"},
		{X: 50, Y: 660, EndX: 80, FontSize: 12, Text: "4"},
		{X: 200, Y: 660, EndX: 230, FontSize: 12, Text: "5"},
		{X: 350, Y: 660, EndX: 380, FontSize: 12, Text: "6"},
		{X: 50, Y: 640, EndX: 80, FontSize: 12, Text: "7"},
		{X: 200, Y: 640, EndX: 230, FontSize: 12, Text: "8"},
		{X: 350, Y: 640, EndX: 380, FontSize: 12, Text: "9"},
	}
	tbl := FindTable(spans, nil)
	if tbl == nil {
		t.Fatal("auto-detect FindTable returned nil")
	}
	if len(tbl.Columns) != 3 {
		t.Errorf("columns: got %d, want 3", len(tbl.Columns))
	}
	if len(tbl.Rows) != 3 {
		t.Errorf("rows: got %d, want 3", len(tbl.Rows))
	}
}

func TestFindTables_NoHeaders_NoMatch(t *testing.T) {
	// Single-column text — auto-detect should return nil.
	spans := []TextSpan{
		{X: 50, Y: 700, EndX: 200, FontSize: 12, Text: "Just a paragraph"},
		{X: 50, Y: 680, EndX: 200, FontSize: 12, Text: "of text here"},
	}
	tables := FindTables(spans, nil)
	if len(tables) != 0 {
		t.Errorf("expected 0 tables, got %d", len(tables))
	}

	// With explicit headers that don't match.
	tables = FindTables(spans, &TableOpts{Headers: []string{"NoSuchHeader"}})
	if tables != nil {
		t.Errorf("expected nil, got %d tables", len(tables))
	}
}

func TestExtractText_PublicWrappers(t *testing.T) {
	data := testPDF(t, "Hello World")
	r, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := r.Pages()
	if err != nil || len(pages) == 0 {
		t.Fatal("no pages")
	}
	fonts := r.PageFonts(pages[0])
	content, err := r.PageContent(pages[0])
	if err != nil {
		t.Fatal(err)
	}

	spans1 := ExtractText(content, fonts, r)
	if len(spans1) == 0 {
		t.Error("ExtractText returned no spans")
	}

	spans2 := ExtractTextWithResources(content, fonts, r, nil)
	if len(spans2) != len(spans1) {
		t.Errorf("ExtractTextWithResources: %d spans, ExtractText: %d spans", len(spans2), len(spans1))
	}
}
