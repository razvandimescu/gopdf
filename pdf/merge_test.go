package pdf

import (
	"strings"
	"testing"
)

func TestMergeTwoFiles(t *testing.T) {
	pdf1 := testMultiPagePDF(t, "Doc1 Page1", "Doc1 Page2")
	pdf2 := testPDF(t, "Doc2 Content")

	merged, err := MergeBytes(pdf1, pdf2)
	if err != nil {
		t.Fatalf("MergeBytes: %v", err)
	}

	doc, err := OpenBytes(merged)
	if err != nil {
		t.Fatalf("opening merged: %v", err)
	}

	if doc.NumPages() != 3 {
		t.Errorf("page count: got %d, want 3", doc.NumPages())
	}

	text, _ := doc.Text()
	for _, want := range []string{"Doc1 Page1", "Doc1 Page2", "Doc2 Content"} {
		if !strings.Contains(text, want) {
			t.Errorf("merged text missing %q", want)
		}
	}
}

func TestMergeSingleFile(t *testing.T) {
	data := testPDF(t, "Hello World", "Line Two")

	merged, err := MergeBytes(data)
	if err != nil {
		t.Fatalf("MergeBytes: %v", err)
	}

	doc, err := OpenBytes(merged)
	if err != nil {
		t.Fatalf("opening merged: %v", err)
	}

	if doc.NumPages() != 1 {
		t.Errorf("page count: got %d, want 1", doc.NumPages())
	}

	text, _ := doc.Text()
	if !strings.Contains(text, "Hello World") {
		t.Error("missing 'Hello World'")
	}
}

func TestMergePageSelection(t *testing.T) {
	data := testMultiPagePDF(t, "Page A", "Page B", "Page C")

	m := NewMerger()
	m.Add(data, 0, 2) // pages 0 and 2 only
	merged, err := m.Merge()
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	doc, err := OpenBytes(merged)
	if err != nil {
		t.Fatalf("opening merged: %v", err)
	}

	if doc.NumPages() != 2 {
		t.Errorf("page count: got %d, want 2", doc.NumPages())
	}

	text, _ := doc.Text()
	if !strings.Contains(text, "Page A") {
		t.Error("missing Page A")
	}
	if !strings.Contains(text, "Page C") {
		t.Error("missing Page C")
	}
	if strings.Contains(text, "Page B") {
		t.Error("should not contain Page B (not selected)")
	}
}

func TestMergeFiveFiles(t *testing.T) {
	var pdfs [][]byte
	for i := 0; i < 5; i++ {
		pdfs = append(pdfs, testPDF(t, "Content from file "+string(rune('A'+i))))
	}

	merged, err := MergeBytes(pdfs...)
	if err != nil {
		t.Fatalf("MergeBytes: %v", err)
	}

	doc, err := OpenBytes(merged)
	if err != nil {
		t.Fatalf("opening merged: %v", err)
	}

	if doc.NumPages() != 5 {
		t.Errorf("page count: got %d, want 5", doc.NumPages())
	}

	text, _ := doc.Text()
	for _, c := range "ABCDE" {
		want := "Content from file " + string(c)
		if !strings.Contains(text, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestMergeInvalidPDF(t *testing.T) {
	_, err := MergeBytes([]byte("not a pdf"))
	if err == nil {
		t.Error("expected error for invalid PDF")
	}
}
