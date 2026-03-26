package pdf

import (
	"os"
	"strings"
	"testing"
)

const testDir = "../example_out/"

func hasTestPDFs(t *testing.T) bool {
	t.Helper()
	if _, err := os.Stat(testDir + "Northgate Academy.pdf"); os.IsNotExist(err) {
		t.Skip("test PDFs not found in example_out/")
		return false
	}
	return true
}

func TestMergeTwoFiles(t *testing.T) {
	if !hasTestPDFs(t) {
		return
	}

	data1, _ := os.ReadFile(testDir + "Oakwood House_1.pdf")
	data2, _ := os.ReadFile(testDir + "Oakwood House_2.pdf")

	// Get original page counts.
	doc1, _ := OpenBytes(data1)
	doc2, _ := OpenBytes(data2)
	totalPages := doc1.NumPages() + doc2.NumPages()

	merged, err := MergeBytes(data1, data2)
	if err != nil {
		t.Fatalf("MergeBytes: %v", err)
	}

	// Verify output is valid PDF.
	doc, err := OpenBytes(merged)
	if err != nil {
		t.Fatalf("opening merged PDF: %v", err)
	}

	if doc.NumPages() != totalPages {
		t.Errorf("page count: got %d, want %d", doc.NumPages(), totalPages)
	}

	// Verify text is extractable.
	text, err := doc.Text()
	if err != nil {
		t.Fatalf("extracting text from merged: %v", err)
	}
	if !strings.Contains(text, "Oakwood House") {
		t.Error("merged text should contain 'Oakwood House'")
	}
}

func TestMergeSingleFile(t *testing.T) {
	if !hasTestPDFs(t) {
		return
	}

	data, _ := os.ReadFile(testDir + "Northgate Academy.pdf")
	origDoc, _ := OpenBytes(data)
	origText, _ := origDoc.Text()

	merged, err := MergeBytes(data)
	if err != nil {
		t.Fatalf("MergeBytes single: %v", err)
	}

	doc, err := OpenBytes(merged)
	if err != nil {
		t.Fatalf("opening merged: %v", err)
	}

	if doc.NumPages() != origDoc.NumPages() {
		t.Errorf("page count: got %d, want %d", doc.NumPages(), origDoc.NumPages())
	}

	mergedText, err := doc.Text()
	if err != nil {
		t.Fatalf("extracting text: %v", err)
	}

	// Check key content is preserved.
	for _, want := range []string{"Nova Facilities", "Northgate Academy", "QT10001", "SC-0001"} {
		if !strings.Contains(mergedText, want) {
			t.Errorf("merged text missing %q", want)
		}
	}
	_ = origText
}

func TestMergePageSelection(t *testing.T) {
	if !hasTestPDFs(t) {
		return
	}

	data, _ := os.ReadFile(testDir + "Oakwood House_1.pdf")
	origDoc, _ := OpenBytes(data)
	if origDoc.NumPages() < 2 {
		t.Skip("need multi-page PDF")
	}

	m := NewMerger()
	m.Add(data, 0) // first page only
	merged, err := m.Merge()
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	doc, err := OpenBytes(merged)
	if err != nil {
		t.Fatalf("opening merged: %v", err)
	}

	if doc.NumPages() != 1 {
		t.Errorf("page count: got %d, want 1", doc.NumPages())
	}
}

func TestMergeFiveFiles(t *testing.T) {
	if !hasTestPDFs(t) {
		return
	}

	files := []string{
		"P2 Wing 2 Showers.pdf",
		"M2001 WHB_.pdf",
		"DEPOT A- X6454AB.pdf",
		"DEPOT A SHOWER TRAY.pdf",
		"Depot B Southend 20001_NL1.pdf",
	}

	var allData [][]byte
	totalPages := 0
	for _, f := range files {
		data, err := os.ReadFile(testDir + f)
		if err != nil {
			t.Skipf("missing %s", f)
			return
		}
		doc, _ := OpenBytes(data)
		totalPages += doc.NumPages()
		allData = append(allData, data)
	}

	merged, err := MergeBytes(allData...)
	if err != nil {
		t.Fatalf("MergeBytes: %v", err)
	}

	doc, err := OpenBytes(merged)
	if err != nil {
		t.Fatalf("opening merged: %v", err)
	}

	if doc.NumPages() != totalPages {
		t.Errorf("page count: got %d, want %d", doc.NumPages(), totalPages)
	}

	text, _ := doc.Text()
	if !strings.Contains(text, "Delta Engineering") {
		t.Error("merged text should contain 'Delta Engineering'")
	}
}
