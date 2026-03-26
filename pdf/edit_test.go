package pdf

import (
	"os"
	"strings"
	"testing"
)

func TestSearchText(t *testing.T) {
	if !hasTestPDFs(t) {
		return
	}

	data, _ := os.ReadFile(testDir + "King David Sixth Form.pdf")
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}

	results := doc.Search("Optimus Facilities")
	if len(results) == 0 {
		t.Fatal("expected to find 'Optimus Facilities'")
	}
	r := results[0]
	if r.Page != 0 {
		t.Errorf("expected page 0, got %d", r.Page)
	}
	if r.Rect.Width <= 0 || r.Rect.Height <= 0 {
		t.Errorf("expected positive rect, got %+v", r.Rect)
	}
	t.Logf("Found %q at page %d, rect: (%.1f, %.1f, %.1f, %.1f)",
		r.Text, r.Page, r.Rect.X, r.Rect.Y, r.Rect.Width, r.Rect.Height)
}

func TestSearchMultipleResults(t *testing.T) {
	if !hasTestPDFs(t) {
		return
	}

	data, _ := os.ReadFile(testDir + "Joseph Wright Shower Block Cathedral Road Derby DE1 3PA.pdf")
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}

	results := doc.Search("250603/WH")
	if len(results) < 2 {
		t.Errorf("expected at least 2 results for '250603/WH', got %d", len(results))
	}
	for i, r := range results {
		t.Logf("  Result %d: page %d at (%.1f, %.1f)", i, r.Page, r.Rect.X, r.Rect.Y)
	}
}

func TestSearchNotFound(t *testing.T) {
	if !hasTestPDFs(t) {
		return
	}

	data, _ := os.ReadFile(testDir + "King David Sixth Form.pdf")
	doc, _ := OpenBytes(data)

	results := doc.Search("THIS_STRING_DOES_NOT_EXIST_IN_THE_PDF")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestTextOverlay(t *testing.T) {
	if !hasTestPDFs(t) {
		return
	}

	data, _ := os.ReadFile(testDir + "King David Sixth Form.pdf")

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

	// Verify the output is valid and has the overlay text.
	doc, err := OpenBytes(result)
	if err != nil {
		t.Fatalf("opening result: %v", err)
	}

	if doc.NumPages() != 2 {
		t.Errorf("page count: got %d, want 2", doc.NumPages())
	}

	text, _ := doc.Page(0).Text()
	if !strings.Contains(text, "APPROVED") {
		t.Error("overlay text 'APPROVED' not found in output")
	}
	// Original content should still be there.
	if !strings.Contains(text, "Optimus Facilities") {
		t.Error("original text 'Optimus Facilities' missing from output")
	}
}

func TestRedactText(t *testing.T) {
	if !hasTestPDFs(t) {
		return
	}

	data, _ := os.ReadFile(testDir + "King David Sixth Form.pdf")

	ed := NewEditor(data)
	err := ed.RedactText("MG74703", 0, 0, 0) // black redaction
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

	// Verify the output is valid.
	doc, err := OpenBytes(result)
	if err != nil {
		t.Fatalf("opening result: %v", err)
	}
	if doc.NumPages() != 2 {
		t.Errorf("page count: got %d, want 2", doc.NumPages())
	}

	t.Logf("Redacted %d regions, output %d bytes", len(ed.redactions), len(result))
}

func TestRedactRegion(t *testing.T) {
	if !hasTestPDFs(t) {
		return
	}

	data, _ := os.ReadFile(testDir + "King David Sixth Form.pdf")

	ed := NewEditor(data)
	ed.Redact(RedactRegion{
		Page: 0,
		Rect: Rect{X: 100, Y: 700, Width: 200, Height: 20},
		R:    1, G: 0, B: 0, // red
	})

	result, err := ed.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	doc, err := OpenBytes(result)
	if err != nil {
		t.Fatalf("opening result: %v", err)
	}
	if doc.NumPages() != 2 {
		t.Errorf("page count: got %d, want 2", doc.NumPages())
	}
}

func TestOverlayAndRedactCombined(t *testing.T) {
	if !hasTestPDFs(t) {
		return
	}

	data, _ := os.ReadFile(testDir + "King David Sixth Form.pdf")

	ed := NewEditor(data)
	// Redact the quotation ref.
	ed.RedactText("MG74703", 1, 1, 1) // white redaction
	// Overlay replacement text.
	ed.AddText(TextOverlay{
		Page:     0,
		X:        100,
		Y:        750,
		Text:     "REDACTED-REF",
		FontSize: 12,
	})

	result, err := ed.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	doc, err := OpenBytes(result)
	if err != nil {
		t.Fatalf("opening result: %v", err)
	}

	text, _ := doc.Page(0).Text()
	if !strings.Contains(text, "REDACTED-REF") {
		t.Error("overlay text not found")
	}
}
