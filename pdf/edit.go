package pdf

import (
	"fmt"
	"math"
	"os"
	"strings"
)

// ImageOverlay places an image on a page. The image is anchored by its center
// at (CX, CY) in PDF user-space points (origin bottom-left), drawn at size
// (Width, Height), and rotated by Rotation degrees counter-clockwise around
// the anchor. Opacity is in [0, 1]; a value of 0 is treated as 1 (fully
// opaque), so the zero value renders as a normal image.
type ImageOverlay struct {
	Page          int
	Image         *Image
	CX, CY        float64
	Width, Height float64
	Rotation      float64
	Opacity       float64
}

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
	images     []ImageOverlay
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

// AddImage places an image on a page (e.g., a watermark or logo).
func (e *Editor) AddImage(overlay ImageOverlay) {
	if overlay.Image == nil {
		return
	}
	e.images = append(e.images, overlay)
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

	// Group overlays, redactions, and images by page.
	pageOverlays := make(map[int][]TextOverlay)
	for _, o := range e.overlays {
		pageOverlays[o.Page] = append(pageOverlays[o.Page], o)
	}
	pageRedactions := make(map[int][]RedactRegion)
	for _, r := range e.redactions {
		pageRedactions[r.Page] = append(pageRedactions[r.Page], r)
	}
	pageImages := make(map[int][]ImageOverlay)
	for _, im := range e.images {
		pageImages[im.Page] = append(pageImages[im.Page], im)
	}

	w := NewWriter()
	pagesRef := w.AllocRef()
	catalogRef := w.AllocRef()

	ctx := &copyContext{
		reader:   reader,
		writer:   w,
		refCache: make(map[int]Ref),
	}

	// Write each unique image XObject (plus its alpha SMask, if any) once and
	// assign it a stable resource name. Pages that use the same image then
	// share a single set of objects.
	imageNames := make(map[*Image]Name)
	imageRefs := make(map[*Image]Ref)
	for _, ov := range e.images {
		if _, done := imageRefs[ov.Image]; done {
			continue
		}
		ref, err := writeImageXObject(w, ov.Image)
		if err != nil {
			return nil, fmt.Errorf("writing image: %w", err)
		}
		imageRefs[ov.Image] = ref
		imageNames[ov.Image] = Name(fmt.Sprintf("Im_gopdf_wm_%d", len(imageNames)))
	}

	var pageRefs []Ref

	for i, pageDict := range pages {
		overlays := pageOverlays[i]
		redactions := pageRedactions[i]
		images := pageImages[i]

		if len(overlays) == 0 && len(redactions) == 0 && len(images) == 0 {
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

		// ensureOverlayFont / image-XObject registration need inline Dicts they
		// can modify, not Refs.
		if len(overlays) > 0 || len(images) > 0 {
			inlineResourceDicts(ctx, copiedPage, pageDict)
		}

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

		// Images: register XObject(s) in Resources, then draw.
		if len(images) > 0 {
			res := copiedPage["Resources"].(Dict)
			xobj, ok := res["XObject"].(Dict)
			if !ok {
				xobj = make(Dict)
				res["XObject"] = xobj
			}
			seenImage := make(map[*Image]bool)
			seenGS := make(map[Name]bool)

			for _, im := range images {
				if !seenImage[im.Image] {
					xobj[imageNames[im.Image]] = imageRefs[im.Image]
					seenImage[im.Image] = true
				}

				opacity := im.Opacity
				if opacity == 0 {
					opacity = 1
				}
				gsName := Name("")
				if opacity < 1 {
					gsName = Name(fmt.Sprintf("GS_gopdf_wm%03d", int(math.Round(opacity*100))))
					if !seenGS[gsName] {
						gs, ok := res["ExtGState"].(Dict)
						if !ok {
							gs = make(Dict)
							res["ExtGState"] = gs
						}
						gs[gsName] = Dict{
							"Type": Name("ExtGState"),
							"ca":   opacity,
							"CA":   opacity,
						}
						seenGS[gsName] = true
					}
				}

				theta := im.Rotation * math.Pi / 180
				cosT, sinT := math.Cos(theta), math.Sin(theta)
				W, H := im.Width, im.Height
				a := W * cosT
				b := W * sinT
				c := -H * sinT
				d := H * cosT
				eX := im.CX - W*cosT/2 + H*sinT/2
				fY := im.CY - W*sinT/2 - H*cosT/2

				extra.WriteString("q ")
				if gsName != "" {
					fmt.Fprintf(&extra, "/%s gs ", gsName)
				}
				fmt.Fprintf(&extra, "%.4f %.4f %.4f %.4f %.4f %.4f cm /%s Do Q\n",
					a, b, c, d, eX, fY, imageNames[im.Image])
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

	return w.FinishWithID(catalogRef, reader.OriginalID())
}

// inlineResourceDicts ensures Resources and its Font / XObject / ExtGState
// sub-dicts are inline Dicts in copiedPage (not Refs to already-written
// objects), so the overlay/image registration code can add entries to them.
// Sub-refs within these dicts (individual fonts, XObjects) stay as Refs and
// are reused from the copyContext cache.
func inlineResourceDicts(ctx *copyContext, copiedPage Dict, srcPage Dict) {
	srcRes, _ := ctx.reader.ResolveDict(srcPage["Resources"])

	res, ok := copiedPage["Resources"].(Dict)
	if !ok {
		if srcRes != nil {
			res = ctx.copyDict(srcRes)
		} else {
			res = make(Dict)
		}
		copiedPage["Resources"] = res
	}

	for _, sub := range []Name{"Font", "XObject", "ExtGState"} {
		if _, ok := res[sub].(Dict); ok {
			continue
		}
		if srcRes == nil {
			continue
		}
		if srcSub, ok := ctx.reader.ResolveDict(srcRes[sub]); ok {
			res[sub] = ctx.copyDict(srcSub)
		}
	}
}

// writeImageXObject writes an Image as a PDF XObject (plus a grayscale SMask
// sub-object if the image has any transparency) and returns the indirect
// reference to the main image stream.
func writeImageXObject(w *Writer, img *Image) (Ref, error) {
	mainDict := Dict{
		"Type":             Name("XObject"),
		"Subtype":          Name("Image"),
		"Width":            img.Width,
		"Height":           img.Height,
		"ColorSpace":       Name("DeviceRGB"),
		"BitsPerComponent": 8,
	}

	if img.alpha != nil {
		smaskRef := w.AllocRef()
		smaskDict := Dict{
			"Type":             Name("XObject"),
			"Subtype":          Name("Image"),
			"Width":            img.Width,
			"Height":           img.Height,
			"ColorSpace":       Name("DeviceGray"),
			"BitsPerComponent": 8,
		}
		if err := w.WriteStream(smaskRef, smaskDict, img.alpha); err != nil {
			return Ref{}, fmt.Errorf("writing alpha mask: %w", err)
		}
		mainDict["SMask"] = smaskRef
	}

	ref := w.AllocRef()
	if err := w.WriteStream(ref, mainDict, img.rgb); err != nil {
		return Ref{}, err
	}
	return ref, nil
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
