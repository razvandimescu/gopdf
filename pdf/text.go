package pdf

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// TextSpan is a piece of text with its position on the page.
type TextSpan struct {
	X, Y     float64
	EndX     float64 // X position after this span (for accurate gap detection)
	FontSize float64
	Font     string
	Text     string
}

// TextLine is a reconstructed line of text.
type TextLine struct {
	Y     float64
	Spans []TextSpan
	Text  string
}

// ExtractText extracts positioned text spans from a PDF content stream.
func ExtractText(content []byte, fonts map[Name]Dict, reader *Reader) []TextSpan {
	return ExtractTextWithResources(content, fonts, reader, nil)
}

// ExtractTextWithResources extracts text with access to full page resources
// (needed for Form XObject extraction via the Do operator).
func ExtractTextWithResources(content []byte, fonts map[Name]Dict, reader *Reader, resources Dict) []TextSpan {
	return extractTextWithResources(content, fonts, reader, resources, identity, 0, nil, nil)
}

// ExtractPageText extracts text from a page, handling rotation and resources automatically.
func ExtractPageText(page Dict, reader *Reader) []TextSpan {
	spans, _ := extractPage(page, reader, nil)
	return spans
}

// extractPage walks a page's content once, in displayed space. When paths is
// not nil it also collects the page's fills.
func extractPage(page Dict, reader *Reader, paths *pathCollector) ([]TextSpan, error) {
	content, err := reader.PageContent(page)
	if err != nil || content == nil {
		return nil, err
	}
	fonts := reader.PageFonts(page)
	resources := reader.PageResources(page)
	return extractTextWithResources(content, fonts, reader, resources, pageRotationMatrix(page), 0, nil, paths), nil
}

var identity = [6]float64{1, 0, 0, 1, 0, 0}

// pageRotationMatrix returns the map from unrotated user space — the space page
// content is drawn in — to the page's displayed space, which is what a viewer
// shows and what Page.Search reports matches in.
//
// The origin (x0, y0) is carried through so rotated positions stay about the
// page's true origin — the inverse of the map rotateOverlaySpace applies to
// overlays, so the two round-trip exactly.
func pageRotationMatrix(page Dict) [6]float64 {
	rotate, _ := page.Int("Rotate")
	rotate = ((rotate % 360) + 360) % 360
	if rotate == 0 {
		return identity
	}

	// MediaBox is [llx lly urx ury].
	var x0, y0, width, height float64
	if mb, ok := page.Array("MediaBox"); ok && len(mb) >= 4 {
		x0 = asFloat(mb[0])
		y0 = asFloat(mb[1])
		width = asFloat(mb[2]) - x0
		height = asFloat(mb[3]) - y0
	}

	switch rotate {
	case 90:
		return [6]float64{0, -1, 1, 0, x0 - y0, width + x0 + y0}
	case 180:
		return [6]float64{-1, 0, 0, -1, width + 2*x0, height + 2*y0}
	case 270:
		return [6]float64{0, 1, -1, 0, height + x0 + y0, y0 - x0}
	}
	return identity
}

// applyMatrix6 maps a point through an affine transform.
func applyMatrix6(m [6]float64, x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

// extractTextWithResources walks content with ctm as the transformation matrix
// it starts from, so every span, glyph and fill it records is in the space ctm
// maps to: a form's content is walked from the transform that places the form.
func extractTextWithResources(content []byte, fonts map[Name]Dict, reader *Reader, resources Dict, ctm [6]float64, depth int, rec *showRecorder, paths *pathCollector) []TextSpan {
	const maxDepth = 10
	if depth > maxDepth {
		return nil
	}
	lex := NewLexer(content)
	var spans []TextSpan

	// Graphics state (persists across text objects, saved/restored by q/Q).
	type graphicsState struct {
		ctm      [6]float64
		fontSize float64
		fontName string
		tc       float64
		tw       float64
		th       float64
		tl       float64
	}

	var (
		tm       [6]float64 // text matrix
		lm       [6]float64 // line matrix
		fontSize float64
		fontName string
		tl       float64       // leading
		tc       float64       // character spacing
		tw       float64       // word spacing
		th       float64 = 100 // horizontal scaling (percentage)
		gsStack  []graphicsState
	)

	// Marked content state for ActualText extraction.
	type markedEntry struct {
		actualText string
		hasActual  bool
		startX     float64
		startY     float64
		suppress   bool // suppress glyph output when ActualText active
	}
	var markedStack []markedEntry

	// Font-specific decoding.
	toUnicodeMaps := make(map[string]map[uint16]string)
	encodings := make(map[string]map[byte]string)
	fontWidths := make(map[string]map[int]float64)
	fontFirstChars := make(map[string]int)
	fontMissingWidths := make(map[string]float64)
	compositeFont := make(map[string]bool) // Type0 (CIDFont) → 2-byte codes

	for name, fd := range fonts {
		sname := string(name)
		if umap := reader.ToUnicodeMap(fd); umap != nil {
			toUnicodeMaps[sname] = umap
		}
		encodings[sname] = reader.FontEncoding(fd)

		subtype, _ := fd.Name("Subtype")

		if subtype == "Type0" {
			// Composite (CID) font — 2-byte character codes.
			compositeFont[sname] = true
			if descArr, ok := reader.ResolveArray(fd["DescendantFonts"]); ok && len(descArr) > 0 {
				cidFont, ok := reader.ResolveDict(descArr[0])
				if ok {
					// Default width.
					dw := 1000.0
					if v, ok := toFloat(reader.Resolve(cidFont["DW"])); ok {
						dw = v
					}
					fontMissingWidths[sname] = dw / 1000.0

					// Sparse width array /W.
					if wArr, ok := reader.ResolveArray(cidFont["W"]); ok {
						wm := parseCIDWidths(wArr)
						fontWidths[sname] = wm
					}

					// Font descriptor MissingWidth.
					if descRef, ok := cidFont["FontDescriptor"]; ok {
						if desc, ok := reader.ResolveDict(descRef); ok {
							if mw, ok := toFloat(reader.Resolve(desc["MissingWidth"])); ok {
								fontMissingWidths[sname] = mw / 1000.0
							}
						}
					}
				}
			}
			continue
		}

		// Simple font — extract widths from Widths array.
		if widths, ok := reader.ResolveArray(fd["Widths"]); ok {
			wm := make(map[int]float64)
			fc := asInt(reader.Resolve(fd["FirstChar"]))
			fontFirstChars[sname] = fc
			for i, w := range widths {
				wm[fc+i] = asFloat(reader.Resolve(w))
			}
			fontWidths[sname] = wm
		}
		if mw, ok := toFloat(reader.Resolve(fd["MissingWidth"])); ok {
			fontMissingWidths[sname] = mw
		}
		// Check font descriptor for MissingWidth.
		if descRef, ok := fd["FontDescriptor"]; ok {
			if desc, ok := reader.ResolveDict(descRef); ok {
				if mw, ok := toFloat(reader.Resolve(desc["MissingWidth"])); ok {
					fontMissingWidths[sname] = mw
				}
			}
		}

		// Standard 14 font fallback.
		if _, ok := fontWidths[sname]; !ok {
			if baseName, ok := fd.Name("BaseFont"); ok {
				if stdW := stdFontWidths(string(baseName)); stdW != nil {
					fontWidths[sname] = stdW
				}
			}
		}
	}

	// operand stack for content stream parsing.
	var stack []any

	// cidCharWidth returns width for a character code (CID or byte code).
	cidCharWidth := func(code int) float64 {
		if wm, ok := fontWidths[fontName]; ok {
			if w, ok := wm[code]; ok {
				if compositeFont[fontName] {
					return w // already divided by 1000 during parsing
				}
				return w / 1000.0
			}
		}
		if mw, ok := fontMissingWidths[fontName]; ok {
			if compositeFont[fontName] {
				return mw // already divided by 1000
			}
			return mw / 1000.0
		}
		return 0.6
	}

	isComposite := func() bool {
		return compositeFont[fontName]
	}

	// decodeByte reads one code of a simple font through its encoding, which
	// names only the codes that are not ASCII: a code it leaves out is ASCII
	// below 0x80, and above it is one the encoding does not define.
	decodeByte := func(b byte) string {
		if name, ok := encodings[fontName][b]; ok {
			return glyphToString(name)
		}
		if b < 0x80 {
			return string(rune(b))
		}
		return ""
	}

	decodeString := func(s string) string {
		raw := []byte(s)
		umap := toUnicodeMaps[fontName]
		// For composite fonts, always use 2-byte.
		// For simple fonts, detect based on map contents.
		isTwoByte := isComposite()
		if !isTwoByte && len(raw) >= 2 {
			_, isTwoByte = umap[uint16(raw[0])<<8|uint16(raw[1])]
		}

		var result strings.Builder
		if umap != nil && isTwoByte && len(raw)%2 == 0 {
			for i := 0; i+1 < len(raw); i += 2 {
				code := uint16(raw[i])<<8 | uint16(raw[i+1])
				if u, ok := umap[code]; ok {
					result.WriteString(u)
				} else {
					result.WriteRune(rune(code))
				}
			}
		} else {
			for _, b := range raw {
				if u, ok := umap[uint16(b)]; ok {
					result.WriteString(u)
				} else {
					result.WriteString(decodeByte(b))
				}
			}
		}
		return result.String()
	}

	// codeAdvance is the pen's travel over the character code at s[i], in text
	// space, including character spacing, word spacing and horizontal scaling.
	codeAdvance := func(s string, i, step int, hScale float64) float64 {
		code := int(s[i])
		if step == 2 {
			code = code<<8 | int(s[i+1])
		}
		adv := (cidCharWidth(code)*fontSize + tc) * hScale
		if step == 1 && s[i] == ' ' {
			adv += tw * hScale
		}
		return adv
	}

	// advanceTextMatrix walks the pen across s, one character code at a time,
	// and returns where each glyph sat while a recorder is attached. Redaction
	// needs exactly that — which codes drew inside a rectangle — and getting it
	// from the same walk that positions text keeps the two from disagreeing.
	//
	// Nobody is recording on the ordinary extraction path, and this runs for
	// every string of every page, so it pays for the glyph positions and the
	// byte slice they name only when something asked for them.
	advanceTextMatrix := func(s string) []glyph {
		hScale := th / 100.0
		step := 1
		if isComposite() && len(s)%2 == 0 {
			step = 2
		}

		if rec == nil {
			var total float64
			for i := 0; i+step <= len(s); i += step {
				total += codeAdvance(s, i, step, hScale)
			}
			tm[4] += total * tm[0]
			tm[5] += total * tm[1]
			return nil
		}

		raw := []byte(s)
		glyphs := make([]glyph, 0, len(raw)/step)
		x, y := applyMatrix6(ctm, tm[4], tm[5])
		for i := 0; i+step <= len(raw); i += step {
			adv := codeAdvance(s, i, step, hScale)
			tm[4] += adv * tm[0]
			tm[5] += adv * tm[1]
			nx, ny := applyMatrix6(ctm, tm[4], tm[5])
			glyphs = append(glyphs, glyph{
				code: raw[i : i+step],
				x0:   x, y0: y,
				x1: nx, y1: ny,
				adv: adv,
			})
			x, y = nx, ny
		}
		return glyphs
	}

	// kernTextMatrix applies a TJ displacement, given in thousandths of a unit
	// of text space. Like a glyph advance it is a text-space distance, so it
	// travels through the text matrix: under a Tm carrying a scale or rotation
	// the pen moves by the transformed amount, not the raw one.
	kernTextMatrix := func(v float64) {
		rec.kern(v)
		d := -v / 1000.0 * fontSize * (th / 100.0)
		tm[4] += d * tm[0]
		tm[5] += d * tm[1]
	}

	showString := func(s string) {
		// The pen advances over every code, whether or not the font gives it a
		// Unicode meaning: a string that decodes to nothing still occupies its
		// width, and still has glyphs redaction may need to remove.
		x, y := applyMatrix6(ctm, tm[4], tm[5])
		decoded := decodeString(s)
		rec.show(decoded, fontSize, advanceTextMatrix(s))
		if decoded == "" {
			return
		}
		// Suppress glyph output when ActualText is active — the EMC handler
		// will emit the ActualText string instead.
		for _, m := range markedStack {
			if m.suppress {
				return
			}
		}
		endX, _ := applyMatrix6(ctm, tm[4], tm[5])
		spans = append(spans, TextSpan{
			X:        x,
			Y:        y,
			EndX:     endX,
			FontSize: fontSize,
			Font:     fontName,
			Text:     decoded,
		})
	}

	// Byte offset of the first operand since the last operator: an operator
	// consumes everything from there, which is the range a recorded show
	// operation has to replace. Only operand tokens skip the bottom of the
	// loop, so the position left after one operator starts the next one.
	operandStart := 0

	for {
		tok, err := lex.NextToken()
		if err != nil || tok.Type == TEOF {
			break
		}

		// If it's an operand, push to stack.
		switch tok.Type {
		case TNumber:
			if tok.IsInt {
				stack = append(stack, tok.Int)
			} else {
				stack = append(stack, tok.Num)
			}
			continue
		case TString, THexString:
			stack = append(stack, tok.Str)
			continue
		case TName:
			stack = append(stack, Name(tok.Str))
			continue
		case TArrayStart:
			// Parse inline array.
			arr := parseInlineArray(lex)
			stack = append(stack, arr)
			continue
		case TDictStart:
			// Skip inline dicts (inline images etc).
			skipInlineDict(lex)
			continue
		}

		if tok.Type != TKeyword {
			continue
		}

		op := tok.Str

		switch op {
		case "BT":
			tm = identity
			lm = identity

		case "ET":
			// End text object.

		case "Tf":
			// Set font: /FontName size Tf
			if len(stack) >= 2 {
				fontSize = asFloat(stack[len(stack)-1])
				if n, ok := stack[len(stack)-2].(Name); ok {
					fontName = string(n)
				}
			}

		case "Tc":
			if len(stack) >= 1 {
				tc = asFloat(stack[len(stack)-1])
			}

		case "Tw":
			if len(stack) >= 1 {
				tw = asFloat(stack[len(stack)-1])
			}

		case "TL":
			if len(stack) >= 1 {
				tl = asFloat(stack[len(stack)-1])
			}

		case "Th", "Tz":
			if len(stack) >= 1 {
				th = asFloat(stack[len(stack)-1])
			}

		case "Td":
			// tx ty Td — move to next line (PDF spec 9.4.2).
			if len(stack) >= 2 {
				tx := asFloat(stack[len(stack)-2])
				ty := asFloat(stack[len(stack)-1])
				lm = matMul6(translateMatrix(tx, ty), lm)
				tm = lm
			}

		case "TD":
			// tx ty TD — same as: -ty TL; tx ty Td
			if len(stack) >= 2 {
				tx := asFloat(stack[len(stack)-2])
				ty := asFloat(stack[len(stack)-1])
				tl = -ty
				lm = matMul6(translateMatrix(tx, ty), lm)
				tm = lm
			}

		case "Tm":
			// a b c d e f Tm — set text matrix directly.
			if len(stack) >= 6 {
				n := len(stack)
				tm = [6]float64{
					asFloat(stack[n-6]), asFloat(stack[n-5]),
					asFloat(stack[n-4]), asFloat(stack[n-3]),
					asFloat(stack[n-2]), asFloat(stack[n-1]),
				}
				lm = tm
			}

		case "T*":
			// Move to start of next line — equivalent to 0 -tl Td.
			lm = matMul6(translateMatrix(0, -tl), lm)
			tm = lm

		case "Tj":
			if len(stack) >= 1 {
				if s, ok := stack[len(stack)-1].(string); ok {
					showString(s)
				}
			}

		case "'":
			// T* then Tj.
			lm = matMul6(translateMatrix(0, -tl), lm)
			tm = lm
			if len(stack) >= 1 {
				if s, ok := stack[len(stack)-1].(string); ok {
					showString(s)
				}
			}

		case "\"":
			// aw ac string " — set word/char spacing, T*, Tj.
			if len(stack) >= 3 {
				tw = asFloat(stack[len(stack)-3])
				tc = asFloat(stack[len(stack)-2])
				lm = matMul6(translateMatrix(0, -tl), lm)
				tm = lm
				if s, ok := stack[len(stack)-1].(string); ok {
					showString(s)
				}
			}

		case "TJ":
			// Array of strings and positioning adjustments.
			if len(stack) >= 1 {
				if arr, ok := stack[len(stack)-1].(Array); ok {
					for _, item := range arr {
						switch v := item.(type) {
						case string:
							showString(v)
						case int:
							kernTextMatrix(float64(v))
						case float64:
							kernTextMatrix(v)
						}
					}
				}
			}

		case "q":
			gsStack = append(gsStack, graphicsState{
				ctm: ctm, fontSize: fontSize, fontName: fontName,
				tc: tc, tw: tw, th: th, tl: tl,
			})

		case "Q":
			if len(gsStack) > 0 {
				gs := gsStack[len(gsStack)-1]
				gsStack = gsStack[:len(gsStack)-1]
				ctm = gs.ctm
				fontSize = gs.fontSize
				fontName = gs.fontName
				tc = gs.tc
				tw = gs.tw
				th = gs.th
				tl = gs.tl
			}

		case "cm":
			if len(stack) >= 6 {
				n := len(stack)
				m := [6]float64{
					asFloat(stack[n-6]), asFloat(stack[n-5]),
					asFloat(stack[n-4]), asFloat(stack[n-3]),
					asFloat(stack[n-2]), asFloat(stack[n-1]),
				}
				ctm = matMul6(m, ctm)
			}

		case "Do":
			if len(stack) >= 1 && resources != nil && reader != nil {
				if xobjName, ok := stack[len(stack)-1].(Name); ok {
					xobjDict, _ := reader.ResolveDict(resources["XObject"])
					if xobjDict != nil {
						xobjRef := xobjDict[xobjName]
						resolved := reader.Resolve(xobjRef)
						if stream, ok := resolved.(*Stream); ok {
							subtype, _ := stream.Dict.Name("Subtype")
							if subtype == "Form" {
								// Get Form's resources (fall back to page resources).
								formFonts := reader.fontsFromDict(stream.Dict)
								if len(formFonts) == 0 {
									formFonts = fonts
								}
								// Apply Form's Matrix if present.
								formCTM := ctm
								if mArr, ok := stream.Dict.Array("Matrix"); ok && len(mArr) == 6 {
									fm := [6]float64{
										asFloat(mArr[0]), asFloat(mArr[1]),
										asFloat(mArr[2]), asFloat(mArr[3]),
										asFloat(mArr[4]), asFloat(mArr[5]),
									}
									formCTM = matMul6(fm, ctm)
								}
								formResources, _ := reader.ResolveDict(stream.Dict["Resources"])
								outer := rec.enter(xobjRef, stream.Data)
								spans = append(spans, extractTextWithResources(stream.Data, formFonts, reader, formResources, formCTM, depth+1, rec, paths)...)
								rec.leave(outer)
							}
						}
					}
				}
			}

		case "BMC":
			// Begin marked content (no properties).
			markedStack = append(markedStack, markedEntry{})

		case "BDC":
			// Begin marked content with properties dict.
			entry := markedEntry{}
			if len(stack) >= 2 {
				if props, ok := stack[len(stack)-1].(Dict); ok {
					if at, ok := props.String("ActualText"); ok {
						entry.actualText = decodeActualText(at)
						entry.hasActual = true
						entry.suppress = true
						entry.startX = tm[4]
						entry.startY = tm[5]
					}
				}
			}
			markedStack = append(markedStack, entry)

		case "EMC":
			// End marked content.
			if len(markedStack) > 0 {
				top := markedStack[len(markedStack)-1]
				markedStack = markedStack[:len(markedStack)-1]
				if top.hasActual && top.actualText != "" {
					x, y := applyMatrix6(ctm, top.startX, top.startY)
					spans = append(spans, TextSpan{
						X:        x,
						Y:        y,
						EndX:     x, // approximate
						FontSize: fontSize,
						Font:     fontName,
						Text:     top.actualText,
					})
				}
			}

		case "BI":
			skipInlineImage(lex)

		// Paths matter only to a collector; without one these do nothing.
		case "m", "l", "c", "v", "y", "re":
			paths.construct(op, stack)
		case "h":
			paths.closeSubpath()
		case "f", "F", "B", "b":
			paths.fill(ctm, false)
		case "f*", "B*", "b*":
			paths.fill(ctm, true)
		case "n", "S", "s":
			paths.discard()
		}

		rec.finish(op, operandStart, lex.Pos(), fontSize, tc, tw, th)
		stack = stack[:0] // clear stack after each operator
		operandStart = lex.Pos()
	}

	return spans
}

func parseInlineArray(lex *Lexer) Array {
	var arr Array
	for {
		tok, err := lex.NextToken()
		if err != nil || tok.Type == TEOF || tok.Type == TArrayEnd {
			break
		}
		switch tok.Type {
		case TNumber:
			if tok.IsInt {
				arr = append(arr, tok.Int)
			} else {
				arr = append(arr, tok.Num)
			}
		case TString, THexString:
			arr = append(arr, tok.Str)
		case TName:
			arr = append(arr, Name(tok.Str))
		case TArrayStart:
			arr = append(arr, parseInlineArray(lex))
		}
	}
	return arr
}

// decodeActualText handles ActualText strings which may be UTF-16BE with BOM.
func decodeActualText(s string) string {
	raw := []byte(s)
	if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF {
		// UTF-16BE with BOM.
		var runes []rune
		for i := 2; i+1 < len(raw); i += 2 {
			u := rune(raw[i])<<8 | rune(raw[i+1])
			// Handle surrogate pairs.
			if u >= 0xD800 && u <= 0xDBFF && i+3 < len(raw) {
				lo := rune(raw[i+2])<<8 | rune(raw[i+3])
				if lo >= 0xDC00 && lo <= 0xDFFF {
					u = 0x10000 + (u-0xD800)*0x400 + (lo - 0xDC00)
					i += 2
				}
			}
			runes = append(runes, u)
		}
		return string(runes)
	}
	return s
}

func skipInlineDict(lex *Lexer) {
	depth := 1
	for depth > 0 {
		tok, err := lex.NextToken()
		if err != nil || tok.Type == TEOF {
			return
		}
		if tok.Type == TDictStart {
			depth++
		}
		if tok.Type == TDictEnd {
			depth--
		}
	}
}

// skipInlineImage moves lex from just after BI to just after the image's EI.
// Image data is binary and can hold EI by chance, so unfiltered data ends
// where its dictionary says it does; filtered data has no length to compute,
// and ends at the first EI the content stream carries on after.
func skipInlineImage(lex *Lexer) {
	dict := Dict{}
	p := Parser{lex: lex}
	for {
		tok, err := lex.NextToken()
		if err != nil || tok.Type == TEOF {
			return
		}
		if tok.Type == TKeyword && tok.Str == "ID" {
			break
		}
		if tok.Type == TName {
			if dict[Name(tok.Str)], err = p.ParseObject(); err != nil {
				return
			}
		}
	}
	// Skip single whitespace byte after ID.
	if !lex.AtEnd() {
		lex.read()
	}
	data := lex.data
	if n, ok := inlineImageLength(dict, len(data)-lex.pos); ok {
		end := lex.pos + n
		for end < len(data) && isWhitespace(data[end]) {
			end++
		}
		if atEI(data, end) {
			lex.pos = end + 2
			return
		}
	}
	for lex.pos < len(data)-2 {
		if isWhitespace(data[lex.pos]) && atEI(data, lex.pos+1) && resumesContent(data[lex.pos+3:]) {
			lex.pos += 3
			return
		}
		lex.pos++
	}
}

// inlineImageLength is the byte length of an unfiltered inline image's data,
// rows padded to whole bytes, when its dictionary determines one that fits in
// the avail bytes left. A colour space named in the page's resources does not.
func inlineImageLength(d Dict, avail int) (int, bool) {
	entry := func(abbrev, full Name) any {
		if v, ok := d[abbrev]; ok {
			return v
		}
		return d[full]
	}
	if entry("F", "Filter") != nil {
		return 0, false
	}
	w, _ := entry("W", "Width").(int)
	h, _ := entry("H", "Height").(int)
	bpc, _ := entry("BPC", "BitsPerComponent").(int)
	components := 1
	if mask, _ := entry("IM", "ImageMask").(bool); mask {
		bpc = 1
	} else {
		switch cs := entry("CS", "ColorSpace").(type) {
		case Name:
			components = map[Name]int{"G": 1, "DeviceGray": 1, "RGB": 3, "DeviceRGB": 3, "CMYK": 4, "DeviceCMYK": 4}[cs]
		case Array:
			if len(cs) == 0 || cs[0] != Name("I") && cs[0] != Name("Indexed") {
				components = 0
			}
		default:
			components = 0
		}
	}
	if w <= 0 || h <= 0 || bpc <= 0 || bpc > 16 || components == 0 || w > avail*8 {
		return 0, false
	}
	row := (w*components*bpc + 7) / 8
	if h > avail/row {
		return 0, false
	}
	return row * h, true
}

// atEI reports whether data holds the EI operator at i.
func atEI(data []byte, i int) bool {
	return i+2 <= len(data) && data[i] == 'E' && data[i+1] == 'I' &&
		(i+2 == len(data) || isWhitespace(data[i+2]) || isDelimiter(data[i+2]))
}

// resumesContent reports whether rest, the bytes after a candidate EI, read as
// the content stream carrying on: operands and known operators, lexed without
// error, for three operators, up to the next inline image, or to the end.
// Image data after a false EI soon lexes into an error, such as a literal
// string left open, or into a word that is no operator. The look ahead stops
// at window bytes, and a candidate still reading as content there is accepted:
// content can hold a comment or string longer than any window.
func resumesContent(rest []byte) bool {
	const window = 256
	lex := NewLexer(rest[:min(len(rest), window)])
	for ops := 0; ops < 3; {
		tok, err := lex.NextToken()
		switch {
		case lex.AtEnd() && len(rest) > window:
			return true
		case err != nil:
			return false
		case tok.Type == TEOF:
			return true
		case tok.Type != TKeyword:
			continue
		case tok.Str == "BI":
			return true
		case !contentOperators[tok.Str]:
			return false
		}
		ops++
	}
	return true
}

// contentOperators are the content stream operators (PDF 32000-1, Annex A),
// less ID and EI, which cannot follow the end of an inline image.
var contentOperators = func() map[string]bool {
	ops := make(map[string]bool)
	for _, op := range strings.Fields(`b B b* B* BDC BI BMC BT BX c cm CS cs d d0 d1
		Do DP EMC ET EX f F f* G g gs h i j J K k l m M MP n q Q re RG rg ri s S SC sc
		SCN scn sh T* Tc Td TD Tf Tj TJ TL Tm Tr Ts Tw Tz v w W W* y ' "`) {
		ops[op] = true
	}
	return ops
}()

// lineYTolerance is how far two baselines may sit apart and still be read as
// one line.
const lineYTolerance = 1.0

// spanGap is the whitespace that stands in for the horizontal distance between
// two pieces of text on one line. PDF draws words where it wants them and says
// nothing about the spaces between; the gap is all there is to go on.
//
// Both the reader's view of a page and redaction's view of it are assembled
// with this rule. They have to agree: removal locates text by searching what
// the page says, so a space one of them inserts and the other does not is a
// query that Page.Search answers and RemoveText silently does not.
func spanGap(gap, fontSize float64) string {
	spaceWidth := math.Max(fontSize*0.25, 2)
	if gap > spaceWidth {
		// Proportional, so tabulated columns keep their shape, capped so a
		// wide margin does not become a wall of spaces.
		const spaces = "          "
		return spaces[:min(int(gap/spaceWidth), len(spaces))]
	}
	if gap > 0.5 {
		return " "
	}
	return ""
}

// spanEnd is where a piece of text leaves the pen, estimated from its width
// when it did not record an end of its own.
func spanEnd(startX, endX, fontSize float64, text string) float64 {
	if endX > startX {
		return endX
	}
	return startX + float64(utf8.RuneCountInString(text))*fontSize*0.5
}

// BuildLines groups text spans into lines and reconstructs text.
func BuildLines(spans []TextSpan) []TextLine {
	if len(spans) == 0 {
		return nil
	}

	// Group spans by Y coordinate (with tolerance).
	// Keep tight to avoid merging overlapping text layers at similar Y positions.
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].Y > spans[j].Y // top to bottom
	})

	var lines []TextLine
	var currentLine *TextLine

	for _, span := range spans {
		if currentLine == nil || math.Abs(span.Y-currentLine.Y) > lineYTolerance {
			lines = append(lines, TextLine{Y: span.Y})
			currentLine = &lines[len(lines)-1]
		}
		currentLine.Spans = append(currentLine.Spans, span)
	}

	// Sort spans within each line by X and build text.
	for i := range lines {
		sortSpansByX(lines[i].Spans)

		var buf strings.Builder
		prevEnd := -1.0
		for _, span := range lines[i].Spans {
			if prevEnd >= 0 {
				buf.WriteString(spanGap(span.X-prevEnd, span.FontSize))
			}
			buf.WriteString(span.Text)
			prevEnd = spanEnd(span.X, span.EndX, span.FontSize, span.Text)
		}
		lines[i].Text = buf.String()
	}

	return lines
}

// glyphToString converts a PostScript glyph name to its Unicode string, by the
// Adobe Glyph List's naming rules: what follows a period names a variant
// (one.oldstyle), and underscores join the parts of a ligature (f_f_i). Each
// part is a name in the list, uni and groups of four hex digits, or u and four
// to six; a part that is none of these, such as g12 or .notdef, reads as
// nothing.
func glyphToString(name string) string {
	base, _, _ := strings.Cut(name, ".")
	var s strings.Builder
	for _, part := range strings.Split(base, "_") {
		s.WriteString(glyphPartToString(part))
	}
	return s.String()
}

func glyphPartToString(part string) string {
	if r, ok := glyphMap[part]; ok {
		return string(r)
	}
	if hex, ok := strings.CutPrefix(part, "uni"); ok && len(hex)%4 == 0 {
		runes := make([]rune, 0, len(hex)/4)
		for i := 0; i < len(hex); i += 4 {
			r, ok := hexScalar(hex[i : i+4])
			if !ok {
				return ""
			}
			runes = append(runes, r)
		}
		return string(runes)
	}
	if hex, ok := strings.CutPrefix(part, "u"); ok && len(hex) >= 4 && len(hex) <= 6 {
		if r, ok := hexScalar(hex); ok {
			return string(r)
		}
	}
	return ""
}

// hexScalar reads hex digits as a Unicode scalar value: never a surrogate.
func hexScalar(hex string) (rune, bool) {
	v, err := strconv.ParseUint(hex, 16, 32)
	return rune(v), err == nil && utf8.ValidRune(rune(v))
}

// glyphMap is defined in glyphlist.go (generated from Adobe Glyph List).

// parseCIDWidths parses a CIDFont /W array into a cid→width map.
// Format: [ cid_start [w1 w2 ...] ] or [ cid_start cid_end w ]
func parseCIDWidths(wArr Array) map[int]float64 {
	wm := make(map[int]float64)
	i := 0
	for i < len(wArr) {
		cid := asInt(wArr[i])
		i++
		if i >= len(wArr) {
			break
		}
		switch v := wArr[i].(type) {
		case Array:
			// cid_start [w1 w2 w3 ...]
			for j, w := range v {
				wm[cid+j] = asFloat(w) / 1000.0
			}
			i++
		default:
			// cid_start cid_end width
			if i+1 >= len(wArr) {
				break
			}
			cidEnd := asInt(wArr[i])
			i++
			width := asFloat(wArr[i]) / 1000.0
			i++
			for c := cid; c <= cidEnd; c++ {
				wm[c] = width
			}
		}
	}
	return wm
}
