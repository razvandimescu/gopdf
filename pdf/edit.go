package pdf

import (
	"fmt"
	"math"
	"os"
	"strings"
)

// ImageOverlay places an image on a page, anchored by its center at (CX, CY),
// drawn at size (Width, Height), rotated by Rotation degrees counter-clockwise
// around the anchor, with Opacity in [0, 1].
//
// (CX, CY) is in displayed space: the coordinate system the viewer shows after
// applying the page's /Rotate, with the MediaBox origin — the same space
// Page.Search reports matches in. On an unrotated, origin-0 page this is plain
// user space; the Editor maps it back to unrotated user space before drawing.
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
	Rect     Rect   // bounding rectangle in displayed space (see ImageOverlay)
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
	X, Y     float64 // position in displayed space (origin bottom-left; see ImageOverlay)
	Text     string
	FontSize float64
	R, G, B  float64 // color (0-1 range), default black
}

// RedactRegion describes an area to cover with a filled rectangle. Rect is in
// displayed space (see ImageOverlay), so a region from Search/RedactText lands
// on the matched text even on rotated pages.
type RedactRegion struct {
	Page    int
	Rect    Rect
	R, G, B float64 // fill color (0-1 range), default black
}

// Editor modifies a PDF by adding overlays and redactions.
type Editor struct {
	data       []byte
	doc        *Document
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

// Document returns the parsed PDF, parsing it on first call and caching the
// result. Callers (and Apply / RedactText internally) share a single parse.
func (e *Editor) Document() (*Document, error) {
	if e.doc != nil {
		return e.doc, nil
	}
	doc, err := OpenBytes(e.data)
	if err != nil {
		return nil, err
	}
	e.doc = doc
	return doc, nil
}

// RedactText searches for text and covers all occurrences.
func (e *Editor) RedactText(query string, r, g, b float64) error {
	doc, err := e.Document()
	if err != nil {
		return err
	}
	for _, res := range doc.Search(query) {
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
	doc, err := e.Document()
	if err != nil {
		return nil, err
	}
	reader := doc.reader
	pages := doc.pages

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

	imageEntries := make(map[*Image]imageEntry)
	for _, ov := range e.images {
		if _, done := imageEntries[ov.Image]; done {
			continue
		}
		ref, err := writeImageXObject(w, ov.Image)
		if err != nil {
			return nil, fmt.Errorf("writing image: %w", err)
		}
		imageEntries[ov.Image] = imageEntry{
			ref:  ref,
			name: Name(fmt.Sprintf("Im_gopdf_wm_%d", len(imageEntries))),
		}
	}

	var pageRefs []Ref

	for i, pageDict := range pages {
		overlays := pageOverlays[i]
		redactions := pageRedactions[i]
		images := pageImages[i]

		if len(overlays) == 0 && len(redactions) == 0 && len(images) == 0 {
			copiedObj := ctx.copyObject(pageDict)
			copiedPage := copiedObj.(Dict)
			delete(copiedPage, "Parent")
			copiedPage["Parent"] = pagesRef

			pageRef := w.AllocRef()
			w.WriteObject(pageRef, copiedPage)
			pageRefs = append(pageRefs, pageRef)
			continue
		}

		copiedObj := ctx.copyObject(pageDict)
		copiedPage := copiedObj.(Dict)
		delete(copiedPage, "Parent")
		copiedPage["Parent"] = pagesRef

		// ensureOverlayFont and the image-XObject / ExtGState registrations
		// below need inline Dicts they can modify, not Refs.
		if len(overlays) > 0 || len(images) > 0 {
			inlineResourceDicts(ctx, copiedPage, pageDict)
		}

		existingContent, _ := reader.PageContent(pageDict)

		var extra strings.Builder

		for _, red := range redactions {
			fmt.Fprintf(&extra, "q %.3f %.3f %.3f rg %.2f %.2f %.2f %.2f re f Q\n",
				red.R, red.G, red.B,
				red.Rect.X, red.Rect.Y, red.Rect.Width, red.Rect.Height)
		}

		if len(overlays) > 0 {
			fontName := Name("F_gopdf_overlay")
			ensureOverlayFont(copiedPage, fontName)

			for _, ov := range overlays {
				r, g, b := ov.R, ov.G, ov.B
				fmt.Fprintf(&extra, "q BT %.3f %.3f %.3f rg /%s %.1f Tf %.2f %.2f Td (%s) Tj ET Q\n",
					r, g, b, fontName, ov.FontSize, ov.X, ov.Y, escapeStringPDF(ov.Text))
			}
		}

		if len(images) > 0 {
			writeImageOps(&extra, copiedPage, images, imageEntries)
		}

		var combined []byte
		if len(existingContent) > 0 {
			prefix, suffix := isolateExistingContent(existingContent)
			combined = append(combined, prefix...)
			combined = append(combined, existingContent...)
			combined = append(combined, suffix...)
		}
		mb := doc.Page(i).MediaBox()
		injected := rotateOverlaySpace(doc.Page(i).Rotation(), mb[0], mb[1], mb[2]-mb[0], mb[3]-mb[1], extra.String())
		combined = append(combined, []byte(injected)...)

		contentRef := w.AllocRef()
		w.WriteStream(contentRef, Dict{}, combined)
		copiedPage["Contents"] = contentRef

		pageRef := w.AllocRef()
		w.WriteObject(pageRef, copiedPage)
		pageRefs = append(pageRefs, pageRef)
	}

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

// imageEntry pairs an image XObject's indirect ref with the resource name it
// is registered under on each page that draws it.
type imageEntry struct {
	ref  Ref
	name Name
}

// writeImageXObject writes an Image as a PDF XObject (plus a grayscale SMask
// sub-object if the image has any transparency) and returns the indirect
// reference to the main image stream.
func writeImageXObject(w *Writer, img *Image) (Ref, error) {
	imageStreamDict := func(colorSpace Name) Dict {
		return Dict{
			"Type":             Name("XObject"),
			"Subtype":          Name("Image"),
			"Width":            img.Width,
			"Height":           img.Height,
			"ColorSpace":       colorSpace,
			"BitsPerComponent": 8,
		}
	}

	mainDict := imageStreamDict("DeviceRGB")
	if img.alpha != nil {
		smaskRef := w.AllocRef()
		if err := w.WriteStream(smaskRef, imageStreamDict("DeviceGray"), img.alpha); err != nil {
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

// writeImageOps registers each overlay's image XObject and (if its opacity
// is below 1) its ExtGState on the page's Resources, then emits the
// content-stream operators to draw them.
func writeImageOps(buf *strings.Builder, page Dict, overlays []ImageOverlay, entries map[*Image]imageEntry) {
	res := page["Resources"].(Dict)
	xobj, ok := res["XObject"].(Dict)
	if !ok {
		xobj = make(Dict)
		res["XObject"] = xobj
	}

	for _, ov := range overlays {
		entry := entries[ov.Image]
		xobj[entry.name] = entry.ref

		gsName := Name("")
		if ov.Opacity < 1 {
			gsName = Name(fmt.Sprintf("GS_gopdf_wm%03d", int(math.Round(ov.Opacity*100))))
			gs, ok := res["ExtGState"].(Dict)
			if !ok {
				gs = make(Dict)
				res["ExtGState"] = gs
			}
			gs[gsName] = Dict{
				"Type": Name("ExtGState"),
				"ca":   ov.Opacity,
				"CA":   ov.Opacity,
			}
		}

		theta := ov.Rotation * math.Pi / 180
		cosT, sinT := math.Cos(theta), math.Sin(theta)
		W, H := ov.Width, ov.Height
		// cm = T(CX, CY) · R(θ) · T(-W/2, -H/2) · S(W, H)
		a := W * cosT
		b := W * sinT
		c := -H * sinT
		d := H * cosT
		eX := ov.CX - W*cosT/2 + H*sinT/2
		fY := ov.CY - W*sinT/2 - H*cosT/2

		buf.WriteString("q ")
		if gsName != "" {
			fmt.Fprintf(buf, "/%s gs ", gsName)
		}
		fmt.Fprintf(buf, "%.4f %.4f %.4f %.4f %.4f %.4f cm /%s Do Q\n",
			a, b, c, d, eX, fY, entry.name)
	}
}

// isolateExistingContent returns the operators to wrap a page's existing
// content with so that appended overlays draw in the default graphics state.
// It always opens at least one q/Q pair, so a CTM the original leaves applied
// (e.g. a top-level "1 0 0 -1 0 H cm" flip from top-left-origin generators)
// cannot leak into the overlay. It also pads for any unbalanced q/Q or
// marked-content (BDC/EMC) nesting in the original — some generators emit
// stack-underflowing streams that make Acrobat report "An error exists on
// this page" — so the rewritten stream is well-formed regardless.
func isolateExistingContent(content []byte) (prefix, suffix string) {
	qFinal, qMin, mcFinal, mcMin := scanContentNesting(content)

	leadQ := 1 - min(0, qMin) // ≥1 for isolation, plus cover for underflow
	trailQ := leadQ + qFinal  // depth after original; always ≥1
	leadMC := -min(0, mcMin)
	trailMC := leadMC + mcFinal

	var pre, suf strings.Builder
	for i := 0; i < leadQ; i++ {
		pre.WriteString("q\n")
	}
	for i := 0; i < leadMC; i++ {
		pre.WriteString("/Artifact BMC\n")
	}
	// q/Q and marked content are independent stacks, so close order is free.
	for i := 0; i < trailMC; i++ {
		suf.WriteString("EMC\n")
	}
	suf.WriteString("\n")
	for i := 0; i < trailQ; i++ {
		suf.WriteString("Q\n")
	}
	return pre.String(), suf.String()
}

// scanContentNesting tokenizes a content stream with the shared Lexer,
// tracking q/Q and BDC|BMC/EMC nesting and returning the final and minimum
// depth of each. Driving the lexer means (literal strings), <hex strings>,
// <<dicts>>, /names, % comments, and BI…EI inline image data are all skipped
// by the same code the rest of the library trusts, so operator-like bytes
// inside them are never miscounted. Malformed bytes that the lexer rejects
// are stepped over so the scan still reaches the end of a broken stream.
func scanContentNesting(s []byte) (qFinal, qMin, mcFinal, mcMin int) {
	lex := NewLexer(s)
	for {
		start := lex.Pos()
		tok, err := lex.NextToken()
		if err != nil {
			if lex.Pos() <= start {
				lex.SetPos(start + 1)
			}
			continue
		}
		if tok.Type == TEOF {
			return
		}
		if tok.Type != TKeyword {
			continue
		}
		switch tok.Str {
		case "q":
			qFinal++
		case "Q":
			qFinal--
			qMin = min(qMin, qFinal)
		case "BDC", "BMC":
			mcFinal++
		case "EMC":
			mcFinal--
			mcMin = min(mcMin, mcFinal)
		case "BI":
			skipInlineImage(lex)
		}
	}
}

// rotateOverlaySpace lets overlay coordinates be expressed in the page's
// displayed (post-/Rotate) space — the same space Page.Search returns. Page
// content is drawn in unrotated user space and the viewer applies /Rotate on
// top, so an overlay emitted naively inherits that rotation (e.g. a 45°
// watermark flips to 135° on a 90° page). Prepending the inverse-rotation
// matrix cancels the viewer's rotation, so the overlay renders identically to
// one on an unrotated page.
//
// The matrix is the displayed→user map for a MediaBox [x0, y0, x0+w, y0+h],
// derived by conjugating the origin-0 rotation with T(x0, y0): M = T(x0,y0) ·
// R⁻¹ · T(-x0,-y0). This keeps the MediaBox origin in displayed space (so it
// agrees with the rotated TextSpan positions Search produces) and reduces to
// the bare origin-0 matrix when x0 = y0 = 0. Rotation 0 maps displayed space
// onto user space identically, so it (and any non-orthogonal angle) passes
// through untouched. x0/y0 are the MediaBox origin; w/h its dimensions.
func rotateOverlaySpace(rotation int, x0, y0, w, h float64, content string) string {
	if content == "" {
		return content
	}
	var cm string
	switch ((rotation % 360) + 360) % 360 {
	case 90:
		cm = fmt.Sprintf("0 1 -1 0 %.4f %.4f", w+x0+y0, y0-x0)
	case 180:
		cm = fmt.Sprintf("-1 0 0 -1 %.4f %.4f", w+2*x0, h+2*y0)
	case 270:
		cm = fmt.Sprintf("0 -1 1 0 %.4f %.4f", x0-y0, h+x0+y0)
	default:
		return content
	}
	return "q " + cm + " cm\n" + content + "Q\n"
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
