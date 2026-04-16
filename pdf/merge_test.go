package pdf

import (
	"errors"
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

func TestMergeNegativePageIndex(t *testing.T) {
	data := testMultiPagePDF(t, "First", "Middle", "Last")

	m := NewMerger()
	m.Add(data, -1) // last page only
	merged, err := m.Merge()
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	doc, _ := OpenBytes(merged)
	if doc.NumPages() != 1 {
		t.Errorf("page count: got %d, want 1", doc.NumPages())
	}

	text, _ := doc.Text()
	if !strings.Contains(text, "Last") {
		t.Error("should contain 'Last' (page -1)")
	}
	if strings.Contains(text, "First") {
		t.Error("should not contain 'First'")
	}
}

func TestMergeNegativePageRange(t *testing.T) {
	data := testMultiPagePDF(t, "P1", "P2", "P3", "P4")

	m := NewMerger()
	m.Add(data, 0, -2, -1) // first, second-to-last, last
	merged, err := m.Merge()
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	doc, _ := OpenBytes(merged)
	if doc.NumPages() != 3 {
		t.Errorf("page count: got %d, want 3", doc.NumPages())
	}

	text, _ := doc.Text()
	if !strings.Contains(text, "P1") || !strings.Contains(text, "P3") || !strings.Contains(text, "P4") {
		t.Errorf("missing expected pages, got: %s", text)
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

func TestMergeWithOptions_NoLimit(t *testing.T) {
	pdf1 := testMultiPagePDF(t, "Page A", "Page B")
	pdf2 := testPDF(t, "Page C")

	m := NewMerger()
	m.Add(pdf1)
	m.Add(pdf2)

	res, err := m.MergeWithOptions(MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalPages != 3 || res.IncludedPages != 3 {
		t.Errorf("pages: total=%d included=%d, want 3/3", res.TotalPages, res.IncludedPages)
	}

	doc, err := OpenBytes(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	if doc.NumPages() != 3 {
		t.Errorf("doc pages: got %d, want 3", doc.NumPages())
	}
}

func TestMergeWithOptions_FailUnderLimit(t *testing.T) {
	data := testPDF(t, "Small")

	m := NewMerger()
	m.Add(data)

	res, err := m.MergeWithOptions(MergeOptions{
		MaxSize:          10 * 1024 * 1024, // 10 MB — way over a tiny PDF
		OversizeBehavior: OversizeFail,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IncludedPages != 1 {
		t.Errorf("included: %d, want 1", res.IncludedPages)
	}
}

func TestMergeWithOptions_FailOverLimit(t *testing.T) {
	data := testMultiPagePDF(t, "A", "B", "C", "D", "E")

	m := NewMerger()
	m.Add(data)

	res, err := m.MergeWithOptions(MergeOptions{
		MaxSize:          1, // 1 byte — impossible
		OversizeBehavior: OversizeFail,
	})
	if err == nil {
		t.Fatal("expected OversizeError")
	}

	var ose *OversizeError
	if !errors.As(err, &ose) {
		t.Fatalf("expected *OversizeError, got %T: %v", err, err)
	}
	if ose.MaxSize != 1 {
		t.Errorf("MaxSize: %d, want 1", ose.MaxSize)
	}
	if ose.EstimatedSize <= 1 {
		t.Errorf("EstimatedSize should be > 1, got %d", ose.EstimatedSize)
	}
	if res == nil || res.TotalPages != 5 {
		t.Errorf("result should report TotalPages=5, got %v", res)
	}
	if res.Data != nil {
		t.Error("Data should be nil on OversizeError")
	}
}

func TestMergeWithOptions_Truncate(t *testing.T) {
	// Create 5 identical PDFs to merge — then set a limit that fits some but not all.
	var pdfs [][]byte
	for i := 0; i < 5; i++ {
		pdfs = append(pdfs, testPDF(t, "Content from file "+string(rune('A'+i))))
	}

	// First, measure the unrestricted merge size.
	m1 := NewMerger()
	for _, p := range pdfs {
		m1.Add(p)
	}
	full, err := m1.Merge()
	if err != nil {
		t.Fatal(err)
	}

	// Set limit to ~60% of full size — should truncate some pages.
	limit := int64(float64(len(full)) * 0.6)

	m2 := NewMerger()
	for _, p := range pdfs {
		m2.Add(p)
	}
	res, err := m2.MergeWithOptions(MergeOptions{
		MaxSize:          limit,
		OversizeBehavior: OversizeTruncate,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TotalPages != 5 {
		t.Errorf("TotalPages: %d, want 5", res.TotalPages)
	}
	if res.IncludedPages >= 5 {
		t.Errorf("IncludedPages should be < 5, got %d", res.IncludedPages)
	}
	if res.IncludedPages == 0 {
		t.Error("IncludedPages should be > 0")
	}
	if int64(len(res.Data)) > limit {
		t.Errorf("output %d bytes exceeds limit %d", len(res.Data), limit)
	}

	// Verify the truncated output is a valid PDF.
	doc, err := OpenBytes(res.Data)
	if err != nil {
		t.Fatalf("truncated PDF invalid: %v", err)
	}
	if doc.NumPages() != res.IncludedPages {
		t.Errorf("doc pages %d != IncludedPages %d", doc.NumPages(), res.IncludedPages)
	}
}

func TestMergeWithOptions_TruncateNothingFits(t *testing.T) {
	data := testPDF(t, "Hello")

	m := NewMerger()
	m.Add(data)

	res, err := m.MergeWithOptions(MergeOptions{
		MaxSize:          1,
		OversizeBehavior: OversizeTruncate,
	})
	if err == nil {
		t.Fatal("expected error when no pages fit")
	}
	var ose *OversizeError
	if !errors.As(err, &ose) {
		t.Fatalf("expected *OversizeError, got %T", err)
	}
	if res == nil || res.IncludedPages != 0 {
		t.Errorf("expected 0 IncludedPages, got %v", res)
	}
}

func TestMergeWithOptions_ShrinkDedup(t *testing.T) {
	// Create two identical PDFs — shrink should deduplicate shared streams.
	data := testPDF(t, "Shared content across files")

	mNormal := NewMerger()
	mNormal.Add(data)
	mNormal.Add(data)
	normalResult, err := mNormal.Merge()
	if err != nil {
		t.Fatal(err)
	}

	mShrink := NewMerger()
	mShrink.Add(data)
	mShrink.Add(data)
	shrinkResult, err := mShrink.MergeWithOptions(MergeOptions{
		MaxSize:          int64(len(normalResult) * 2), // generous limit
		OversizeBehavior: OversizeShrink,
	})
	if err != nil {
		t.Fatal(err)
	}
	if shrinkResult.IncludedPages != 2 {
		t.Errorf("IncludedPages: %d, want 2", shrinkResult.IncludedPages)
	}

	// Shrink (with dedup) should produce a smaller or equal output.
	if len(shrinkResult.Data) > len(normalResult) {
		t.Errorf("shrink (%d bytes) should be <= normal (%d bytes)",
			len(shrinkResult.Data), len(normalResult))
	}

	// Verify the shrunk output is valid.
	doc, err := OpenBytes(shrinkResult.Data)
	if err != nil {
		t.Fatal(err)
	}
	if doc.NumPages() != 2 {
		t.Errorf("pages: %d, want 2", doc.NumPages())
	}
	text, _ := doc.Text()
	if !strings.Contains(text, "Shared content") {
		t.Errorf("missing content in shrunk PDF, got: %s", text)
	}
}

func TestMergeWithOptions_ShrinkOverLimit(t *testing.T) {
	data := testPDF(t, "Content")

	m := NewMerger()
	m.Add(data)

	_, err := m.MergeWithOptions(MergeOptions{
		MaxSize:          1,
		OversizeBehavior: OversizeShrink,
	})
	if err == nil {
		t.Fatal("expected OversizeError")
	}
	var ose *OversizeError
	if !errors.As(err, &ose) {
		t.Fatalf("expected *OversizeError, got %T", err)
	}
}

func TestMergeWithOptions_BackwardCompat(t *testing.T) {
	// Merge() should produce identical results to MergeWithOptions with zero opts.
	data := testMultiPagePDF(t, "X", "Y")

	m1 := NewMerger()
	m1.Add(data)
	old, err := m1.Merge()
	if err != nil {
		t.Fatal(err)
	}

	m2 := NewMerger()
	m2.Add(data)
	res, err := m2.MergeWithOptions(MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Both should produce valid 2-page PDFs.
	doc1, _ := OpenBytes(old)
	doc2, _ := OpenBytes(res.Data)
	if doc1.NumPages() != doc2.NumPages() {
		t.Errorf("page counts differ: Merge()=%d, MergeWithOptions()=%d",
			doc1.NumPages(), doc2.NumPages())
	}
}
