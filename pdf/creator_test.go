package pdf

import (
	"strings"
	"testing"
)

// testPDF creates a single-page PDF with the given text lines.
func testPDF(t *testing.T, lines ...string) []byte {
	t.Helper()
	c := NewCreator()
	page := c.NewPage(612, 792) // US Letter
	page.SetFont("Helvetica", 12)
	y := 750.0
	for _, line := range lines {
		page.DrawText(72, y, line)
		y -= 16
	}
	data, err := c.Build()
	if err != nil {
		t.Fatalf("creating test PDF: %v", err)
	}
	return data
}

// testMultiPagePDF creates a PDF with N pages, each with distinct text.
func testMultiPagePDF(t *testing.T, pageTexts ...string) []byte {
	t.Helper()
	c := NewCreator()
	for _, text := range pageTexts {
		page := c.NewPage(612, 792)
		page.SetFont("Helvetica", 12)
		page.DrawText(72, 750, text)
	}
	data, err := c.Build()
	if err != nil {
		t.Fatalf("creating test PDF: %v", err)
	}
	return data
}

func TestCreatorSinglePage(t *testing.T) {
	data := testPDF(t, "Hello World", "Second line")

	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("opening created PDF: %v", err)
	}

	if doc.NumPages() != 1 {
		t.Errorf("pages: got %d, want 1", doc.NumPages())
	}

	text, err := doc.Text()
	if err != nil {
		t.Fatalf("extracting text: %v", err)
	}
	if !strings.Contains(text, "Hello World") {
		t.Errorf("text missing 'Hello World', got: %s", text)
	}
	if !strings.Contains(text, "Second line") {
		t.Errorf("text missing 'Second line', got: %s", text)
	}
}

func TestCreatorMultiPage(t *testing.T) {
	data := testMultiPagePDF(t, "Page One Content", "Page Two Content", "Page Three Content")

	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}

	if doc.NumPages() != 3 {
		t.Errorf("pages: got %d, want 3", doc.NumPages())
	}

	for i, want := range []string{"Page One Content", "Page Two Content", "Page Three Content"} {
		text, _ := doc.Page(i).Text()
		if !strings.Contains(text, want) {
			t.Errorf("page %d missing %q", i, want)
		}
	}
}

func TestCreatorFonts(t *testing.T) {
	c := NewCreator()
	page := c.NewPage(612, 792)
	page.SetFont("Helvetica-Bold", 18)
	page.DrawText(72, 700, "Bold Title")
	page.SetFont("Times-Roman", 12)
	page.DrawText(72, 680, "Normal body text")

	data, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}

	doc, _ := OpenBytes(data)
	text, _ := doc.Text()
	if !strings.Contains(text, "Bold Title") {
		t.Error("missing Bold Title")
	}
	if !strings.Contains(text, "Normal body text") {
		t.Error("missing body text")
	}
}

func TestCreatorTextWidth(t *testing.T) {
	c := NewCreator()
	page := c.NewPage(612, 792)
	page.SetFont("Helvetica", 12)

	w := page.TextWidth("Hello")
	if w <= 0 || w > 100 {
		t.Errorf("unexpected text width: %.2f", w)
	}

	// Courier is monospaced — all chars same width.
	page.SetFont("Courier", 12)
	w1 := page.TextWidth("iii")
	w2 := page.TextWidth("MMM")
	if w1 != w2 {
		t.Errorf("Courier should be monospaced: 'iii'=%.2f, 'MMM'=%.2f", w1, w2)
	}
}

func TestCreatorDrawShapes(t *testing.T) {
	c := NewCreator()
	page := c.NewPage(612, 792)
	page.SetColor(0, 0, 0)
	page.DrawRect(50, 700, 200, 50)
	page.FillRect(50, 600, 200, 50, 0.9, 0.9, 0.9)
	page.DrawLine(50, 550, 250, 550, 1)

	data, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}

	// Verify it's a valid PDF.
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if doc.NumPages() != 1 {
		t.Errorf("pages: got %d, want 1", doc.NumPages())
	}
}
