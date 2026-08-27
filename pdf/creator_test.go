package pdf

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
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
	page.SetStrokeColor(0.5, 0.5, 0.5)
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

// testPNG encodes a 2x2 PNG; when translucent, one pixel is partly transparent so
// the embedded XObject carries an SMask.
func testPNG(t *testing.T, translucent bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for i := range 4 {
		img.Set(i%2, i/2, color.NRGBA{R: 200, G: 100, B: 50, A: 255})
	}
	if translucent {
		img.Set(0, 0, color.NRGBA{R: 200, G: 100, B: 50, A: 128})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding test PNG: %v", err)
	}
	return buf.Bytes()
}

func TestCreatorDrawImage(t *testing.T) {
	img, err := LoadImageBytes(testPNG(t, true))
	if err != nil {
		t.Fatalf("loading test image: %v", err)
	}

	// The same image on two pages must be written once and referenced twice.
	c := NewCreator()
	c.NewPage(200, 100).DrawImage(img, 10, 20, 80, 60)
	c.NewPage(200, 100).DrawImage(img, 0, 0, 200, 100)
	data, err := c.Build()
	if err != nil {
		t.Fatalf("building: %v", err)
	}

	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("opening created PDF: %v", err)
	}
	if doc.NumPages() != 2 {
		t.Fatalf("pages: got %d, want 2", doc.NumPages())
	}

	var refs []Ref
	for i, page := range doc.pages {
		res, ok := doc.reader.ResolveDict(page["Resources"])
		if !ok {
			t.Fatalf("page %d: no Resources dict", i)
		}
		xobj, ok := doc.reader.ResolveDict(res["XObject"])
		if !ok {
			t.Fatalf("page %d: Resources has no XObject dict", i)
		}
		ref, ok := xobj["Im1"].(Ref)
		if !ok {
			t.Fatalf("page %d: XObject has no /Im1 reference, got %v", i, xobj)
		}
		refs = append(refs, ref)

		content, err := doc.reader.PageContent(page)
		if err != nil {
			t.Fatalf("page %d: reading content: %v", i, err)
		}
		if !strings.Contains(string(content), "/Im1 Do") {
			t.Errorf("page %d: content draws no image: %s", i, content)
		}
	}
	if refs[0] != refs[1] {
		t.Errorf("image written twice: page 0 has %v, page 1 has %v", refs[0], refs[1])
	}

	// Placement: "w 0 0 h x y cm" positions the image by its lower-left corner.
	content, _ := doc.reader.PageContent(doc.pages[0])
	if want := "80.0000 0.0000 0.0000 60.0000 10.0000 20.0000 cm"; !strings.Contains(string(content), want) {
		t.Errorf("content missing %q: %s", want, content)
	}

	// Translucency travels with the image as a grayscale soft mask.
	stream, ok := doc.reader.Resolve(refs[0]).(*Stream)
	if !ok {
		t.Fatalf("image XObject %v does not resolve to a stream", refs[0])
	}
	xdict := stream.Dict
	if xdict["Subtype"] != Name("Image") || xdict["Width"] != 2 {
		t.Errorf("image XObject = %v, want a 2-wide /Image", xdict)
	}
	smask, ok := doc.reader.Resolve(xdict["SMask"]).(*Stream)
	if !ok {
		t.Fatalf("translucent image has no SMask stream, got %v", xdict)
	}
	if smask.Dict["ColorSpace"] != Name("DeviceGray") {
		t.Errorf("SMask ColorSpace = %v, want DeviceGray", smask.Dict["ColorSpace"])
	}
}

func TestCreatorDrawImageOpaqueAndNil(t *testing.T) {
	img, err := LoadImageBytes(testPNG(t, false))
	if err != nil {
		t.Fatalf("loading test image: %v", err)
	}
	c := NewCreator()
	page := c.NewPage(200, 100)
	page.DrawImage(nil, 0, 0, 10, 10) // ignored, not a panic
	page.DrawImage(img, 0, 0, 200, 100)
	data, err := c.Build()
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("opening created PDF: %v", err)
	}
	res, _ := doc.reader.ResolveDict(doc.pages[0]["Resources"])
	xobj, _ := doc.reader.ResolveDict(res["XObject"])
	if len(xobj) != 1 {
		t.Fatalf("XObject dict = %v, want exactly one entry", xobj)
	}
	stream, ok := doc.reader.Resolve(xobj["Im1"]).(*Stream)
	if !ok {
		t.Fatalf("image XObject does not resolve to a stream: %v", xobj["Im1"])
	}
	if _, has := stream.Dict["SMask"]; has {
		t.Errorf("opaque image carries an SMask: %v", stream.Dict)
	}
}
