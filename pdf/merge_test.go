package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
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

// Adobe flags PDFs with 21-byte xref entries as damaged and disables Save As.
// ISO 32000-1 §7.5.4 mandates exactly 20 bytes per entry; the bug pattern is
// a stray space before CR LF (e.g. "0000000000 65535 f \r\n").
func TestXrefEntriesAreTwentyBytes(t *testing.T) {
	merged, err := MergeBytes(testPDF(t, "A"), testMultiPagePDF(t, "B", "C"))
	if err != nil {
		t.Fatalf("MergeBytes: %v", err)
	}
	xrefStart := bytes.LastIndex(merged, []byte("\nxref\n"))
	trailerStart := bytes.LastIndex(merged, []byte("\ntrailer\n"))
	if xrefStart < 0 || trailerStart < 0 || trailerStart < xrefStart {
		t.Fatal("xref/trailer markers not found")
	}
	section := merged[xrefStart:trailerStart]
	if bytes.Contains(section, []byte(" n \r\n")) || bytes.Contains(section, []byte(" f \r\n")) {
		t.Error("xref entry has trailing space before CR LF (21 bytes)")
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
	if ose.Size <= 1 {
		t.Errorf("EstimatedSize should be > 1, got %d", ose.Size)
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

func TestMergeWithOptions_TruncateWithDedup(t *testing.T) {
	// Merge 10 copies of the same PDF. Truncate now applies dedup + strip,
	// so identical streams are shared and more pages should fit compared to
	// the raw (fail-mode) size.
	data := testPDF(t, "Identical content for dedup")

	// Fail mode: raw estimated size (no optimization).
	mFail := NewMerger()
	for i := 0; i < 10; i++ {
		mFail.Add(data)
	}
	_, failErr := mFail.MergeWithOptions(MergeOptions{
		MaxSize:          1,
		OversizeBehavior: OversizeFail,
	})
	var failOse *OversizeError
	if !errors.As(failErr, &failOse) {
		t.Fatalf("expected OversizeError from fail, got %T", failErr)
	}
	rawSize := failOse.Size

	// Truncate: set limit to ~50% of raw size. With dedup, more pages fit.
	limit := rawSize / 2
	mTrunc := NewMerger()
	for i := 0; i < 10; i++ {
		mTrunc.Add(data)
	}
	res, err := mTrunc.MergeWithOptions(MergeOptions{
		MaxSize:          limit,
		OversizeBehavior: OversizeTruncate,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TotalPages != 10 {
		t.Errorf("TotalPages: %d, want 10", res.TotalPages)
	}
	// With dedup, identical streams are shared — should fit significantly more
	// than 5 pages (50% of raw would fit ~5 without optimization).
	if res.IncludedPages <= 5 {
		t.Errorf("dedup should allow >5 pages at 50%% raw limit, got %d", res.IncludedPages)
	}
	if int64(len(res.Data)) > limit {
		t.Errorf("output %d bytes exceeds limit %d", len(res.Data), limit)
	}

	doc, err := OpenBytes(res.Data)
	if err != nil {
		t.Fatalf("truncated+dedup PDF invalid: %v", err)
	}
	if doc.NumPages() != res.IncludedPages {
		t.Errorf("doc pages %d != IncludedPages %d", doc.NumPages(), res.IncludedPages)
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

// testJPEGPDF creates a single-page PDF with an embedded JPEG image of the given dimensions.
func testJPEGPDF(t *testing.T, width, height int) []byte {
	t.Helper()
	// Create a colorful image so JPEG compression has something to work with.
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8(x * 255 / width),
				G: uint8(y * 255 / height),
				B: uint8((x + y) * 255 / (width + height)),
				A: 255,
			})
		}
	}
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	jpegData := jpegBuf.Bytes()

	w := NewWriter()
	pagesRef := w.AllocRef()
	catalogRef := w.AllocRef()

	imgRef := w.AllocRef()
	mustWrite := func(ref Ref, obj any) {
		if err := w.WriteObject(ref, obj); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(imgRef, &Stream{
		Dict: Dict{
			"Type":             Name("XObject"),
			"Subtype":          Name("Image"),
			"Width":            width,
			"Height":           height,
			"ColorSpace":       Name("DeviceRGB"),
			"BitsPerComponent": 8,
			"Filter":           Name("DCTDecode"),
			"Length":           len(jpegData),
		},
		Data: jpegData,
	})

	contentRef := w.AllocRef()
	content := fmt.Sprintf("q %d 0 0 %d 0 0 cm /Img1 Do Q", width, height)
	if err := w.WriteStream(contentRef, Dict{}, []byte(content)); err != nil {
		t.Fatal(err)
	}

	pageRef := w.AllocRef()
	mustWrite(pageRef, Dict{
		"Type":      Name("Page"),
		"Parent":    pagesRef,
		"MediaBox":  Array{0, 0, float64(width), float64(height)},
		"Contents":  contentRef,
		"Resources": Dict{"XObject": Dict{"Img1": imgRef}},
	})
	mustWrite(pagesRef, Dict{
		"Type":  Name("Pages"),
		"Kids":  Array{pageRef},
		"Count": 1,
	})
	mustWrite(catalogRef, Dict{
		"Type":  Name("Catalog"),
		"Pages": pagesRef,
	})

	data, err := w.Finish(catalogRef)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestMergeWithOptions_ShrinkJPEGRecompression(t *testing.T) {
	// Create PDFs with large high-quality JPEG images.
	pdf1 := testJPEGPDF(t, 800, 600)
	pdf2 := testJPEGPDF(t, 800, 600)

	// Merge without optimization to get the raw size.
	mRaw := NewMerger()
	mRaw.Add(pdf1)
	mRaw.Add(pdf2)
	rawData, err := mRaw.Merge()
	if err != nil {
		t.Fatal(err)
	}

	// Shrink with a limit that's ~50% of raw — should trigger JPEG recompression.
	limit := int64(len(rawData)) / 2
	mShrink := NewMerger()
	mShrink.Add(pdf1)
	mShrink.Add(pdf2)
	res, err := mShrink.MergeWithOptions(MergeOptions{
		MaxSize:          limit,
		OversizeBehavior: OversizeShrink,
	})
	if err != nil {
		t.Fatalf("expected shrink to fit via JPEG recompression, got: %v", err)
	}
	if res.IncludedPages != 2 {
		t.Errorf("IncludedPages: %d, want 2", res.IncludedPages)
	}
	if int64(len(res.Data)) > limit {
		t.Errorf("output %d bytes exceeds limit %d", len(res.Data), limit)
	}

	doc, err := OpenBytes(res.Data)
	if err != nil {
		t.Fatalf("shrunk PDF invalid: %v", err)
	}
	if doc.NumPages() != 2 {
		t.Errorf("pages: %d, want 2", doc.NumPages())
	}

	t.Logf("raw=%d  shrunk=%d  limit=%d  savings=%.0f%%",
		len(rawData), len(res.Data), limit,
		100*(1-float64(len(res.Data))/float64(len(rawData))))
}
