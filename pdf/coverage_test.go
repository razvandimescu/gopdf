package pdf

import (
	"os"
	"path/filepath"
	"testing"
)

// Tests targeting 0%-coverage functions to reach the 80% threshold.

func createTestPDF(t *testing.T) []byte {
	t.Helper()
	c := NewCreator()
	p := c.NewPage(595, 842)
	p.SetFont("Helvetica-Bold", 18)
	p.SetColor(0, 0, 0)
	p.SetStrokeColor(0.5, 0.5, 0.5)
	p.DrawText(72, 750, "Test Document")
	p.SetFont("Helvetica", 12)
	p.DrawText(72, 720, "Hello World")
	p.DrawLine(72, 710, 523, 710, 1)
	p.DrawRect(72, 680, 200, 20)
	p.FillRect(72, 650, 200, 20, 0.9, 0.9, 0.9)
	data, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRotation_Default(t *testing.T) {
	data := createTestPDF(t)
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Page(0).Rotation() != 0 {
		t.Errorf("expected rotation 0, got %d", doc.Page(0).Rotation())
	}
}

func TestMediaBox_Default(t *testing.T) {
	data := createTestPDF(t)
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	mb := doc.Page(0).MediaBox()
	if mb[2] != 595 || mb[3] != 842 {
		t.Errorf("expected A4 mediabox, got %v", mb)
	}
}

func TestPage_Nil(t *testing.T) {
	data := createTestPDF(t)
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
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

func TestDict_String(t *testing.T) {
	d := Dict{"Key": "value"}
	s, ok := d.String("Key")
	if !ok || s != "value" {
		t.Errorf("String: got %q, %v", s, ok)
	}
	_, ok = d.String("Missing")
	if ok {
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
	_, ok = d.Stream("Missing")
	if ok {
		t.Error("missing key should return false")
	}
}

func TestParser_Lexer(t *testing.T) {
	p := NewParser([]byte("123"))
	if p.Lexer() == nil {
		t.Error("Lexer should not be nil")
	}
}

func TestReader_Trailer_XRef(t *testing.T) {
	data := createTestPDF(t)
	r, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	if r.Trailer() == nil {
		t.Error("Trailer should not be nil")
	}
	if r.XRef() == nil {
		t.Error("XRef should not be nil")
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
	// Odd nibble
	got, _ = decodeASCIIHex([]byte("4>"))
	if len(got) != 1 || got[0] != 0x40 {
		t.Errorf("odd nibble: got %x", got)
	}
}

func TestPaethPredictor(t *testing.T) {
	tests := []struct {
		a, b, c, want byte
	}{
		{1, 2, 3, 1},     // p=0, pa=1, pb=2, pc=3 → a
		{0, 5, 0, 5},     // p=5, pa=5, pb=0, pc=5 → b (pb < pa)
		{5, 0, 5, 0},     // p=0, pa=5, pb=0, pc=5 → b (pb <= pc)
		{10, 10, 10, 10}, // p=10, all equal → a
	}
	for _, tt := range tests {
		got := paethPredictor(tt.a, tt.b, tt.c)
		if got != tt.want {
			t.Errorf("paethPredictor(%d,%d,%d) = %d, want %d", tt.a, tt.b, tt.c, got, tt.want)
		}
	}
}

func TestDecodeActualText(t *testing.T) {
	// Plain ASCII passthrough.
	if decodeActualText("hello") != "hello" {
		t.Error("plain text passthrough failed")
	}
	// UTF-16BE with BOM.
	utf16 := string([]byte{0xFE, 0xFF, 0x00, 0x41, 0x00, 0x42})
	if decodeActualText(utf16) != "AB" {
		t.Errorf("UTF-16BE: got %q, want AB", decodeActualText(utf16))
	}
}

func TestGlyphToString(t *testing.T) {
	// Known glyph
	if glyphToString("space") != " " {
		t.Errorf("space glyph: got %q", glyphToString("space"))
	}
	// uni prefix
	if glyphToString("uni0041") != "A" {
		t.Errorf("uni0041: got %q", glyphToString("uni0041"))
	}
	// Single char passthrough
	if glyphToString("X") != "X" {
		t.Errorf("single char: got %q", glyphToString("X"))
	}
	// Unknown multi-char
	if glyphToString("unknownglyph") != "unknownglyph" {
		t.Errorf("unknown: got %q", glyphToString("unknownglyph"))
	}
}

func TestMergeFiles(t *testing.T) {
	data := createTestPDF(t)
	tmp := filepath.Join(t.TempDir(), "test.pdf")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		t.Fatal(err)
	}
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
	data := createTestPDF(t)
	tmp := filepath.Join(t.TempDir(), "test.pdf")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		t.Fatal(err)
	}
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
	data := createTestPDF(t)
	tmp := filepath.Join(t.TempDir(), "test.pdf")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		t.Fatal(err)
	}
	ed, err := NewEditorFromFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ed.Apply()
	if err != nil {
		t.Fatal(err)
	}
	if len(result) == 0 {
		t.Error("empty result from editor")
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

func TestDocument_Text(t *testing.T) {
	data := createTestPDF(t)
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	text, err := doc.Text()
	if err != nil {
		t.Fatal(err)
	}
	if text == "" {
		t.Error("Text() returned empty string")
	}
}

func TestSetStrokeColor(t *testing.T) {
	c := NewCreator()
	p := c.NewPage(100, 100)
	p.SetStrokeColor(1, 0, 0)
	p.DrawRect(10, 10, 80, 80)
	data, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("empty PDF")
	}
}

func TestBuildLines_Spacing(t *testing.T) {
	// Exercise gap-based spacing in BuildLines.
	spans := []TextSpan{
		{X: 10, Y: 100, EndX: 50, FontSize: 12, Text: "Hello"},
		{X: 100, Y: 100, EndX: 140, FontSize: 12, Text: "World"}, // big gap → spaces
		{X: 142, Y: 100, EndX: 145, FontSize: 12, Text: "!"},     // small gap → single space
		{X: 10, Y: 80, EndX: 60, FontSize: 12, Text: "Line Two"}, // different Y → new line
	}
	lines := BuildLines(spans)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[1].Text == "" {
		t.Error("second line text is empty")
	}
}

func TestTextWidth(t *testing.T) {
	c := NewCreator()
	p := c.NewPage(100, 100)
	p.SetFont("Helvetica", 12)
	w := p.TextWidth("Hello")
	if w <= 0 {
		t.Errorf("TextWidth should be positive, got %f", w)
	}
}

func TestFindTable_AutoDetectPath(t *testing.T) {
	// Exercise the auto-detect branch of FindTable (nil headers).
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
	// FindTable with nil opts → auto-detect path
	tbl := FindTable(spans, nil)
	if tbl == nil {
		t.Fatal("auto-detect FindTable returned nil")
	}
}

func TestFindTables_ExplicitHeadersPath(t *testing.T) {
	// Exercise FindTables with headers (returns at most 1).
	spans := []TextSpan{
		{X: 50, Y: 700, EndX: 100, FontSize: 12, Text: "Name"},
		{X: 200, Y: 700, EndX: 250, FontSize: 12, Text: "Value"},
		{X: 50, Y: 680, EndX: 100, FontSize: 12, Text: "a"},
		{X: 200, Y: 680, EndX: 250, FontSize: 12, Text: "1"},
	}
	tables := FindTables(spans, &TableOpts{Headers: []string{"Name"}})
	if len(tables) != 1 {
		t.Errorf("expected 1 table, got %d", len(tables))
	}
}

func TestExtractText_PublicWrappers(t *testing.T) {
	data := createTestPDF(t)
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
	if len(spans2) == 0 {
		t.Error("ExtractTextWithResources returned no spans")
	}
}
