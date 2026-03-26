package pdf

import (
	"strings"
	"testing"
)

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
