package pdf

import (
	"fmt"
	"strings"
)

// Creator builds a PDF document from scratch.
type Creator struct {
	pages []*PageBuilder
}

// NewCreator returns a new empty Creator.
func NewCreator() *Creator {
	return &Creator{}
}

// NewPage adds a blank page with the given dimensions (in points).
// Common sizes: A4 = (595, 842), US Letter = (612, 792).
func (c *Creator) NewPage(width, height float64) *PageBuilder {
	pb := &PageBuilder{
		width:    width,
		height:   height,
		font:     "Helvetica",
		fontSize: 12,
	}
	c.pages = append(c.pages, pb)
	return pb
}

// Build produces the final PDF bytes.
func (c *Creator) Build() ([]byte, error) {
	if len(c.pages) == 0 {
		return nil, fmt.Errorf("no pages")
	}

	w := NewWriter()
	pagesRef := w.AllocRef()
	catalogRef := w.AllocRef()

	var pageRefs []Ref

	for _, pb := range c.pages {
		// Build the font resources dict.
		fontResources := make(Dict)
		for name, baseFont := range pb.usedFonts {
			fontResources[Name(name)] = Dict{
				"Type":     Name("Font"),
				"Subtype":  Name("Type1"),
				"BaseFont": Name(baseFont),
			}
		}

		resources := Dict{}
		if len(fontResources) > 0 {
			resources["Font"] = fontResources
		}

		// Write content stream.
		contentRef := w.AllocRef()
		contentData := []byte(pb.buf.String())
		if err := w.WriteStream(contentRef, Dict{}, contentData); err != nil {
			return nil, fmt.Errorf("writing content stream: %w", err)
		}

		// Write page dict.
		pageRef := w.AllocRef()
		pageDict := Dict{
			"Type":      Name("Page"),
			"Parent":    pagesRef,
			"MediaBox":  Array{0, 0, pb.width, pb.height},
			"Contents":  contentRef,
			"Resources": resources,
		}
		if err := w.WriteObject(pageRef, pageDict); err != nil {
			return nil, fmt.Errorf("writing page: %w", err)
		}
		pageRefs = append(pageRefs, pageRef)
	}

	// Pages node.
	kids := make(Array, len(pageRefs))
	for i, ref := range pageRefs {
		kids[i] = ref
	}
	if err := w.WriteObject(pagesRef, Dict{
		"Type":  Name("Pages"),
		"Kids":  kids,
		"Count": len(pageRefs),
	}); err != nil {
		return nil, err
	}

	// Catalog.
	if err := w.WriteObject(catalogRef, Dict{
		"Type":  Name("Catalog"),
		"Pages": pagesRef,
	}); err != nil {
		return nil, err
	}

	return w.Finish(catalogRef)
}

// PageBuilder accumulates drawing operations for a single page.
type PageBuilder struct {
	width, height float64
	buf           strings.Builder
	font          string
	fontSize      float64
	usedFonts     map[string]string // resource name → BaseFont name
	fontCounter   int
	fontMap       map[string]string // BaseFont → resource name
}

// SetFont sets the current font and size. Supported: Helvetica, Helvetica-Bold,
// Times-Roman, Times-Bold, Courier, Courier-Bold.
func (pb *PageBuilder) SetFont(baseFont string, size float64) {
	resName := pb.ensureFont(baseFont)
	pb.font = baseFont
	pb.fontSize = size
	fmt.Fprintf(&pb.buf, "BT /%s %.1f Tf ET\n", resName, size)
}

// SetColor sets the fill color (RGB, 0-1 range).
func (pb *PageBuilder) SetColor(r, g, b float64) {
	fmt.Fprintf(&pb.buf, "%.3f %.3f %.3f rg\n", r, g, b)
}

// SetStrokeColor sets the stroke color (RGB, 0-1 range).
func (pb *PageBuilder) SetStrokeColor(r, g, b float64) {
	fmt.Fprintf(&pb.buf, "%.3f %.3f %.3f RG\n", r, g, b)
}

// DrawText draws a text string at (x, y) using the current font and color.
func (pb *PageBuilder) DrawText(x, y float64, text string) {
	resName := pb.ensureFont(pb.font)
	fmt.Fprintf(&pb.buf, "BT /%s %.1f Tf %.2f %.2f Td (%s) Tj ET\n",
		resName, pb.fontSize, x, y, escapeStringPDF(text))
}

// DrawLine draws a line from (x1,y1) to (x2,y2) with the given width.
func (pb *PageBuilder) DrawLine(x1, y1, x2, y2, lineWidth float64) {
	fmt.Fprintf(&pb.buf, "%.2f w %.2f %.2f m %.2f %.2f l S\n",
		lineWidth, x1, y1, x2, y2)
}

// DrawRect draws a stroked rectangle.
func (pb *PageBuilder) DrawRect(x, y, w, h float64) {
	fmt.Fprintf(&pb.buf, "%.2f %.2f %.2f %.2f re S\n", x, y, w, h)
}

// FillRect draws a filled rectangle with the specified color.
func (pb *PageBuilder) FillRect(x, y, w, h, r, g, b float64) {
	fmt.Fprintf(&pb.buf, "q %.3f %.3f %.3f rg %.2f %.2f %.2f %.2f re f Q\n",
		r, g, b, x, y, w, h)
}

// TextWidth returns the width of text in the current font and size (in points).
func (pb *PageBuilder) TextWidth(text string) float64 {
	widths := stdFontWidths(pb.font)
	if widths == nil {
		return float64(len(text)) * pb.fontSize * 0.5
	}
	var total float64
	for _, r := range text {
		w, ok := widths[int(r)]
		if !ok {
			w = 500
		}
		total += w
	}
	return total / 1000.0 * pb.fontSize
}

func (pb *PageBuilder) ensureFont(baseFont string) string {
	if pb.fontMap == nil {
		pb.fontMap = make(map[string]string)
		pb.usedFonts = make(map[string]string)
	}
	if resName, ok := pb.fontMap[baseFont]; ok {
		return resName
	}
	pb.fontCounter++
	resName := fmt.Sprintf("F%d", pb.fontCounter)
	pb.fontMap[baseFont] = resName
	pb.usedFonts[resName] = baseFont
	return resName
}
