package pdf

import (
	"fmt"
	"math"
	"os"
	"strings"
)

// Rect is a bounding rectangle on a page.
type Rect struct {
	X, Y          float64 // bottom-left corner
	Width, Height float64
}

// SearchResult is a text match with its location.
type SearchResult struct {
	Page     int    // 0-based page index
	Text     string // matched text
	Rect     Rect   // bounding rectangle
	FontSize float64
}

// Search finds all occurrences of query across all pages.
// Case-sensitive. Returns results in document order.
func (d *Document) Search(query string) []SearchResult {
	var results []SearchResult
	for i := range d.pages {
		page := d.Page(i)
		results = append(results, page.Search(query)...)
	}
	return results
}

// Search finds all occurrences of query on this page.
func (p *Page) Search(query string) []SearchResult {
	if query == "" {
		return nil
	}

	spans, _ := p.TextSpans()
	var results []SearchResult

	// Strategy 1: check individual spans for the query.
	for _, span := range spans {
		idx := 0
		for {
			pos := strings.Index(span.Text[idx:], query)
			if pos < 0 {
				break
			}
			// Estimate X position within span.
			charWidth := (span.EndX - span.X)
			if len(span.Text) > 0 {
				charWidth /= float64(len([]rune(span.Text)))
			}
			matchX := span.X + float64(pos)*charWidth
			matchEndX := matchX + float64(len([]rune(query)))*charWidth

			results = append(results, SearchResult{
				Page: p.num,
				Text: query,
				Rect: Rect{
					X:      matchX,
					Y:      span.Y - span.FontSize*0.2, // descender
					Width:  matchEndX - matchX,
					Height: span.FontSize * 1.2,
				},
				FontSize: span.FontSize,
			})
			idx += pos + len(query)
		}
	}

	// Strategy 2: check across adjacent spans on the same line.
	lines := BuildLines(spans)
	for _, line := range lines {
		idx := 0
		for {
			pos := strings.Index(line.Text[idx:], query)
			if pos < 0 {
				break
			}
			// Check this wasn't already found in a single span.
			absPos := idx + pos
			// Find which spans cover this range.
			r := rectForLineRange(line, absPos, absPos+len([]rune(query)))
			if r != nil {
				// Deduplicate: skip if we already have a result at this position.
				dup := false
				for _, existing := range results {
					if existing.Page == p.num && math.Abs(existing.Rect.X-r.X) < 1 && math.Abs(existing.Rect.Y-r.Y) < 1 {
						dup = true
						break
					}
				}
				if !dup {
					results = append(results, SearchResult{
						Page:     p.num,
						Text:     query,
						Rect:     *r,
						FontSize: line.Spans[0].FontSize,
					})
				}
			}
			idx += pos + len(query)
		}
	}

	return results
}

// rectForLineRange estimates the bounding rect for a character range in a TextLine.
func rectForLineRange(line TextLine, startChar, endChar int) *Rect {
	if len(line.Spans) == 0 {
		return nil
	}

	// Walk through spans to find the start/end X positions.
	charIdx := 0
	var startX, endX float64
	var fontSize float64
	foundStart := false

	for _, span := range line.Spans {
		spanLen := len([]rune(span.Text))
		spanStartChar := charIdx

		// Account for space between spans in the line text.
		// The line.Text has spaces inserted by BuildLines — we need to track that.
		charIdx += spanLen

		if !foundStart && startChar < charIdx {
			offset := startChar - spanStartChar
			cw := spanCharWidth(span)
			startX = span.X + float64(offset)*cw
			fontSize = span.FontSize
			foundStart = true
		}
		if foundStart && endChar <= charIdx {
			offset := endChar - spanStartChar
			cw := spanCharWidth(span)
			endX = span.X + float64(offset)*cw
			break
		}
		if foundStart {
			endX = span.EndX
		}
	}

	if !foundStart || endX <= startX {
		return nil
	}

	return &Rect{
		X:      startX,
		Y:      line.Y - fontSize*0.2,
		Width:  endX - startX,
		Height: fontSize * 1.2,
	}
}

func spanCharWidth(span TextSpan) float64 {
	runeCount := float64(len([]rune(span.Text)))
	if runeCount == 0 || span.EndX <= span.X {
		return span.FontSize * 0.5
	}
	return (span.EndX - span.X) / runeCount
}

// TextOverlay describes text to draw on a page.
type TextOverlay struct {
	Page     int     // 0-based page index
	X, Y     float64 // position (PDF coordinates: origin bottom-left)
	Text     string
	FontSize float64
	R, G, B  float64 // color (0-1 range), default black
}

// RedactRegion describes an area to cover with a filled rectangle.
type RedactRegion struct {
	Page    int
	Rect    Rect
	R, G, B float64 // fill color (0-1 range), default black
}

// Editor modifies a PDF by adding overlays and redactions.
type Editor struct {
	data       []byte
	overlays   []TextOverlay
	redactions []RedactRegion
}

// NewEditor creates an Editor from PDF bytes.
func NewEditor(data []byte) *Editor {
	return &Editor{data: data}
}

// NewEditorFromFile creates an Editor from a PDF file.
func NewEditorFromFile(path string) (*Editor, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Validate it's a valid PDF.
	if _, err := Open(data); err != nil {
		return nil, err
	}
	return &Editor{data: data}, nil
}

// AddText adds a text overlay to a page.
func (e *Editor) AddText(overlay TextOverlay) {
	if overlay.FontSize == 0 {
		overlay.FontSize = 12
	}
	e.overlays = append(e.overlays, overlay)
}

// Redact covers a region with a filled rectangle.
func (e *Editor) Redact(region RedactRegion) {
	e.redactions = append(e.redactions, region)
}

// RedactText searches for text and covers all occurrences.
func (e *Editor) RedactText(query string, r, g, b float64) error {
	doc, err := OpenBytes(e.data)
	if err != nil {
		return err
	}
	results := doc.Search(query)
	for _, res := range results {
		e.redactions = append(e.redactions, RedactRegion{
			Page: res.Page,
			Rect: res.Rect,
			R:    r, G: g, B: b,
		})
	}
	return nil
}

// Apply produces the modified PDF bytes.
func (e *Editor) Apply() ([]byte, error) {
	reader, err := Open(e.data)
	if err != nil {
		return nil, err
	}
	pages, err := reader.Pages()
	if err != nil {
		return nil, err
	}

	// Group overlays and redactions by page.
	pageOverlays := make(map[int][]TextOverlay)
	for _, o := range e.overlays {
		pageOverlays[o.Page] = append(pageOverlays[o.Page], o)
	}
	pageRedactions := make(map[int][]RedactRegion)
	for _, r := range e.redactions {
		pageRedactions[r.Page] = append(pageRedactions[r.Page], r)
	}

	w := NewWriter()
	pagesRef := w.AllocRef()
	catalogRef := w.AllocRef()

	ctx := &copyContext{
		reader:   reader,
		writer:   w,
		refCache: make(map[int]Ref),
	}

	var pageRefs []Ref

	for i, pageDict := range pages {
		overlays := pageOverlays[i]
		redactions := pageRedactions[i]

		if len(overlays) == 0 && len(redactions) == 0 {
			// No modifications — copy page as-is.
			copiedObj := ctx.copyObject(pageDict)
			copiedPage := copiedObj.(Dict)
			delete(copiedPage, "Parent")
			copiedPage["Parent"] = pagesRef

			pageRef := w.AllocRef()
			w.WriteObject(pageRef, copiedPage)
			pageRefs = append(pageRefs, pageRef)
			continue
		}

		// Page has modifications. Copy it, then append operators.
		copiedObj := ctx.copyObject(pageDict)
		copiedPage := copiedObj.(Dict)
		delete(copiedPage, "Parent")
		copiedPage["Parent"] = pagesRef

		// Get the existing content stream data.
		existingContent, _ := reader.PageContent(pageDict)

		// Build the extra operators to append.
		var extra strings.Builder

		// Redactions: draw filled rectangles.
		for _, red := range redactions {
			fmt.Fprintf(&extra, "q %.3f %.3f %.3f rg %.2f %.2f %.2f %.2f re f Q\n",
				red.R, red.G, red.B,
				red.Rect.X, red.Rect.Y, red.Rect.Width, red.Rect.Height)
		}

		// Overlays: draw text.
		if len(overlays) > 0 {
			// We need a font in Resources. Use Helvetica with a known name.
			fontName := Name("F_gopdf_overlay")
			ensureOverlayFont(copiedPage, fontName)

			for _, ov := range overlays {
				r, g, b := ov.R, ov.G, ov.B
				fmt.Fprintf(&extra, "q BT %.3f %.3f %.3f rg /%s %.1f Tf %.2f %.2f Td (%s) Tj ET Q\n",
					r, g, b, fontName, ov.FontSize, ov.X, ov.Y, escapeStringPDF(ov.Text))
			}
		}

		// Combine existing content + new operators into a single stream.
		var combined []byte
		if len(existingContent) > 0 {
			combined = append(combined, existingContent...)
			combined = append(combined, '\n')
		}
		combined = append(combined, []byte(extra.String())...)

		// Write the combined content as a new stream.
		contentRef := w.AllocRef()
		w.WriteStream(contentRef, Dict{}, combined)

		// Point the page to the new content stream.
		copiedPage["Contents"] = contentRef

		pageRef := w.AllocRef()
		w.WriteObject(pageRef, copiedPage)
		pageRefs = append(pageRefs, pageRef)
	}

	// Build Pages and Catalog.
	kids := make(Array, len(pageRefs))
	for i, ref := range pageRefs {
		kids[i] = ref
	}
	w.WriteObject(pagesRef, Dict{
		"Type":  Name("Pages"),
		"Kids":  kids,
		"Count": len(pageRefs),
	})
	w.WriteObject(catalogRef, Dict{
		"Type":  Name("Catalog"),
		"Pages": pagesRef,
	})

	return w.Finish(catalogRef)
}

// ensureOverlayFont adds a Helvetica font to the page's Resources if needed.
func ensureOverlayFont(page Dict, fontName Name) {
	// Get or create Resources dict.
	res, ok := page["Resources"].(Dict)
	if !ok {
		res = make(Dict)
		page["Resources"] = res
	}

	// Get or create Font dict within Resources.
	fontDict, ok := res["Font"].(Dict)
	if !ok {
		fontDict = make(Dict)
		res["Font"] = fontDict
	}

	// Add Helvetica if our font name isn't there.
	if _, exists := fontDict[fontName]; !exists {
		fontDict[fontName] = Dict{
			"Type":     Name("Font"),
			"Subtype":  Name("Type1"),
			"BaseFont": Name("Helvetica"),
		}
	}
}

// escapeStringPDF escapes a string for use in PDF literal string syntax (...).
func escapeStringPDF(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch c {
		case '(', ')':
			b.WriteByte('\\')
			b.WriteRune(c)
		case '\\':
			b.WriteString("\\\\")
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

