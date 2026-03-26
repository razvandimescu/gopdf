package pdf

import (
	"math"
	"sort"
	"strings"
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
		ctm      = [6]float64{1, 0, 0, 1, 0, 0} // current transformation matrix
		tm       [6]float64                       // text matrix
		lm       [6]float64                       // line matrix
		fontSize float64
		fontName string
		tl       float64     // leading
		tc       float64     // character spacing
		tw       float64     // word spacing
		th       float64 = 100 // horizontal scaling (percentage)
		gsStack  []graphicsState
	)

	// Font-specific decoding.
	toUnicodeMaps := make(map[string]map[uint16]string)
	encodingDiffs := make(map[string]map[byte]string)
	fontWidths := make(map[string]map[int]float64)
	fontFirstChars := make(map[string]int)
	fontMissingWidths := make(map[string]float64)
	compositeFont := make(map[string]bool) // Type0 (CIDFont) → 2-byte codes

	for name, fd := range fonts {
		sname := string(name)
		if umap := reader.ToUnicodeMap(fd); umap != nil {
			toUnicodeMaps[sname] = umap
		}
		if diffs := reader.FontEncoding(fd); diffs != nil {
			encodingDiffs[sname] = diffs
		}

		subtype, _ := fd.Name("Subtype")

		if subtype == "Type0" {
			// Composite (CID) font — 2-byte character codes.
			compositeFont[sname] = true
			if descArr, ok := fd.Array("DescendantFonts"); ok && len(descArr) > 0 {
				cidFont, ok := reader.ResolveDict(descArr[0])
				if ok {
					// Default width.
					dw := 1000.0
					if v, ok := cidFont.Float("DW"); ok {
						dw = v
					}
					fontMissingWidths[sname] = dw / 1000.0

					// Sparse width array /W.
					if wArr, ok := cidFont.Array("W"); ok {
						wm := parseCIDWidths(wArr)
						fontWidths[sname] = wm
					}

					// Font descriptor MissingWidth.
					if descRef, ok := cidFont["FontDescriptor"]; ok {
						if desc, ok := reader.ResolveDict(descRef); ok {
							if mw, ok := desc.Float("MissingWidth"); ok {
								fontMissingWidths[sname] = mw / 1000.0
							}
						}
					}
				}
			}
			continue
		}

		// Simple font — extract widths from Widths array.
		if widths, ok := fd.Array("Widths"); ok {
			wm := make(map[int]float64)
			fc, _ := fd.Int("FirstChar")
			fontFirstChars[sname] = fc
			for i, w := range widths {
				wm[fc+i] = asFloat(w)
			}
			fontWidths[sname] = wm
		}
		if mw, ok := fd.Float("MissingWidth"); ok {
			fontMissingWidths[sname] = mw
		}
		// Check font descriptor for MissingWidth.
		if descRef, ok := fd["FontDescriptor"]; ok {
			if desc, ok := reader.ResolveDict(descRef); ok {
				if mw, ok := desc.Float("MissingWidth"); ok {
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

	identity := [6]float64{1, 0, 0, 1, 0, 0}

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

	decodeString := func(s string) string {
		raw := []byte(s)
		isTwoByte := isComposite()

		// Try ToUnicode map first.
		if umap, ok := toUnicodeMaps[fontName]; ok && umap != nil {
			var result strings.Builder
			// For composite fonts, always use 2-byte.
			// For simple fonts, detect based on map contents.
			if !isTwoByte && len(raw) >= 2 {
				code := uint16(raw[0])<<8 | uint16(raw[1])
				if _, ok := umap[code]; ok {
					isTwoByte = true
				}
			}
			if isTwoByte && len(raw)%2 == 0 {
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
						result.WriteByte(b)
					}
				}
			}
			return result.String()
		}

		// Try encoding differences.
		if diffs, ok := encodingDiffs[fontName]; ok && diffs != nil {
			var result strings.Builder
			for _, b := range raw {
				if name, ok := diffs[b]; ok {
					result.WriteString(glyphToString(name))
				} else {
					result.WriteByte(b)
				}
			}
			return result.String()
		}

		// WinAnsiEncoding fallback (covers most modern PDFs).
		return winansiDecode(s)
	}

	advanceTextMatrix := func(s string) {
		raw := []byte(s)
		hScale := th / 100.0
		var totalWidth float64
		if isComposite() && len(raw)%2 == 0 {
			for i := 0; i+1 < len(raw); i += 2 {
				code := int(raw[i])<<8 | int(raw[i+1])
				w := cidCharWidth(code)
				totalWidth += (w*fontSize + tc) * hScale
			}
		} else {
			for _, b := range raw {
				w := cidCharWidth(int(b))
				totalWidth += (w*fontSize + tc) * hScale
				if b == ' ' {
					totalWidth += tw * hScale
				}
			}
		}
		tm[4] += totalWidth * tm[0]
		tm[5] += totalWidth * tm[1]
	}

	// transformPos applies CTM to a text-space position.
	transformPos := func(tx, ty float64) (float64, float64) {
		return ctm[0]*tx + ctm[2]*ty + ctm[4],
			ctm[1]*tx + ctm[3]*ty + ctm[5]
	}

	showString := func(s string) {
		decoded := decodeString(s)
		if decoded == "" {
			return
		}
		x, y := transformPos(tm[4], tm[5])
		advanceTextMatrix(s)
		endX, _ := transformPos(tm[4], tm[5])
		spans = append(spans, TextSpan{
			X:        x,
			Y:        y,
			EndX:     endX,
			FontSize: fontSize,
			Font:     fontName,
			Text:     decoded,
		})
	}

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
							// Displacement in thousandths of a unit of text space.
							tm[4] -= float64(v) / 1000.0 * fontSize * (th / 100.0)
						case float64:
							tm[4] -= v / 1000.0 * fontSize * (th / 100.0)
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
			// Form XObject reference — skip for now (Phase 4).

		case "BI":
			skipInlineImage(lex)
		}

		stack = stack[:0] // clear stack after each operator
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

func skipInlineImage(lex *Lexer) {
	// Parse the inline image dict until ID keyword.
	for {
		tok, err := lex.NextToken()
		if err != nil || tok.Type == TEOF {
			return
		}
		if tok.Type == TKeyword && tok.Str == "ID" {
			break
		}
	}
	// Skip single whitespace byte after ID.
	if !lex.AtEnd() {
		lex.read()
	}
	// Scan raw bytes for whitespace + "EI" + (whitespace or delimiter or EOF).
	for lex.pos < len(lex.data)-2 {
		if isWhitespace(lex.data[lex.pos]) &&
			lex.data[lex.pos+1] == 'E' && lex.data[lex.pos+2] == 'I' {
			if lex.pos+3 >= len(lex.data) || isWhitespace(lex.data[lex.pos+3]) || isDelimiter(lex.data[lex.pos+3]) {
				lex.pos += 3
				return
			}
		}
		lex.pos++
	}
}

// BuildLines groups text spans into lines and reconstructs text.
func BuildLines(spans []TextSpan) []TextLine {
	if len(spans) == 0 {
		return nil
	}

	// Group spans by Y coordinate (with tolerance).
	const yTolerance = 2.0
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].Y > spans[j].Y // top to bottom
	})

	var lines []TextLine
	var currentLine *TextLine

	for _, span := range spans {
		if currentLine == nil || math.Abs(span.Y-currentLine.Y) > yTolerance {
			lines = append(lines, TextLine{Y: span.Y})
			currentLine = &lines[len(lines)-1]
		}
		currentLine.Spans = append(currentLine.Spans, span)
	}

	// Sort spans within each line by X and build text.
	for i := range lines {
		sort.Slice(lines[i].Spans, func(a, b int) bool {
			return lines[i].Spans[a].X < lines[i].Spans[b].X
		})

		var buf strings.Builder
		prevEnd := -1.0
		for _, span := range lines[i].Spans {
			if prevEnd >= 0 {
				gap := span.X - prevEnd
				spaceWidth := span.FontSize * 0.25
				if spaceWidth < 2 {
					spaceWidth = 2
				}
				if gap > spaceWidth {
					// Insert proportional spaces.
					nSpaces := int(gap / spaceWidth)
					if nSpaces < 1 {
						nSpaces = 1
					}
					if nSpaces > 10 {
						nSpaces = 10 // cap at reasonable tab-like spacing
					}
					buf.WriteString(strings.Repeat(" ", nSpaces))
				} else if gap > 0.5 {
					buf.WriteByte(' ')
				}
			}
			buf.WriteString(span.Text)
			// Estimate where this span ends.
			if span.EndX > span.X {
				prevEnd = span.EndX
			} else {
				prevEnd = span.X + float64(len([]rune(span.Text)))*span.FontSize*0.5
			}
		}
		lines[i].Text = buf.String()
	}

	return lines
}



// glyphToString converts a PostScript glyph name to its Unicode string.
func glyphToString(name string) string {
	// Common glyph names.
	if r, ok := glyphMap[name]; ok {
		return string(r)
	}
	// If it looks like "uniXXXX", decode hex.
	if strings.HasPrefix(name, "uni") && len(name) == 7 {
		v, err := parseHexRune(name[3:])
		if err == nil {
			return string(v)
		}
	}
	if len(name) == 1 {
		return name
	}
	return name
}

func parseHexRune(s string) (rune, error) {
	var v rune
	for _, c := range s {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= c - '0'
		case c >= 'a' && c <= 'f':
			v |= c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v |= c - 'A' + 10
		default:
			return 0, nil
		}
	}
	return v, nil
}

// glyphMap is defined in glyphlist.go (generated from Adobe Glyph List).

// winansiDecode converts a WinAnsiEncoding string to UTF-8.
func winansiDecode(s string) string {
	var buf strings.Builder
	for _, b := range []byte(s) {
		if r, ok := winansiMap[b]; ok {
			buf.WriteRune(r)
		} else {
			buf.WriteByte(b)
		}
	}
	return buf.String()
}

// WinAnsiEncoding special mappings (0x80-0x9F differ from Latin-1).
var winansiMap = map[byte]rune{
	0x80: '\u20AC', 0x82: '\u201A', 0x83: '\u0192', 0x84: '\u201E',
	0x85: '\u2026', 0x86: '\u2020', 0x87: '\u2021', 0x88: '\u02C6',
	0x89: '\u2030', 0x8A: '\u0160', 0x8B: '\u2039', 0x8C: '\u0152',
	0x8E: '\u017D', 0x91: '\u2018', 0x92: '\u2019', 0x93: '\u201C',
	0x94: '\u201D', 0x95: '\u2022', 0x96: '\u2013', 0x97: '\u2014',
	0x98: '\u02DC', 0x99: '\u2122', 0x9A: '\u0161', 0x9B: '\u203A',
	0x9C: '\u0153', 0x9E: '\u017E', 0x9F: '\u0178',
}

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
