package pdf

import (
	"math"
	"sort"
	"strings"
)

// TextSpan is a piece of text with its position on the page.
type TextSpan struct {
	X, Y     float64
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

	// Text state.
	var (
		tm      [6]float64 // text matrix
		lm      [6]float64 // line matrix
		fontSize float64
		fontName string
		tl      float64 // leading
		tc      float64 // character spacing
		tw      float64 // word spacing
		th      float64 = 100 // horizontal scaling (percentage)
	)

	// Font-specific decoding.
	toUnicodeMaps := make(map[string]map[uint16]string)
	encodingDiffs := make(map[string]map[byte]string)
	fontWidths := make(map[string]map[int]float64)
	fontFirstChars := make(map[string]int)
	fontMissingWidths := make(map[string]float64)

	for name, fd := range fonts {
		sname := string(name)
		if umap := reader.ToUnicodeMap(fd); umap != nil {
			toUnicodeMaps[sname] = umap
		}
		if diffs := reader.FontEncoding(fd); diffs != nil {
			encodingDiffs[sname] = diffs
		}
		// Extract font widths for accurate positioning.
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
	}

	identity := [6]float64{1, 0, 0, 1, 0, 0}

	// operand stack for content stream parsing.
	var stack []any

	charWidth := func(code byte) float64 {
		if wm, ok := fontWidths[fontName]; ok {
			if w, ok := wm[int(code)]; ok {
				return w / 1000.0
			}
		}
		if mw, ok := fontMissingWidths[fontName]; ok {
			return mw / 1000.0
		}
		return 0.6 // reasonable default
	}

	decodeString := func(s string) string {
		// Try ToUnicode map first.
		if umap, ok := toUnicodeMaps[fontName]; ok && umap != nil {
			var result strings.Builder
			raw := []byte(s)
			// Detect if this is a 2-byte encoding.
			isTwoByte := false
			if len(raw) >= 2 {
				// Check if any 2-byte code matches.
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
			for _, b := range []byte(s) {
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
		for _, b := range raw {
			w := charWidth(b)
			totalWidth += (w*fontSize + tc) * hScale
			if b == ' ' {
				totalWidth += tw * hScale
			}
		}
		tm[4] += totalWidth
	}

	showString := func(s string) {
		decoded := decodeString(s)
		if decoded == "" {
			return
		}
		x := tm[4]
		y := tm[5]
		spans = append(spans, TextSpan{
			X:        x,
			Y:        y,
			FontSize: fontSize,
			Font:     fontName,
			Text:     decoded,
		})
		advanceTextMatrix(s)
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
			tc = 0
			tw = 0

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
			// tx ty Td — move to next line.
			if len(stack) >= 2 {
				tx := asFloat(stack[len(stack)-2])
				ty := asFloat(stack[len(stack)-1])
				lm[4] += tx * lm[0]
				lm[5] += ty * lm[3]
				if tx != 0 {
					lm[4] = lm[4] + tx - tx*lm[0] + tx
					lm[4] = roundTo(lm[4], 6)
				}
				// Simpler approach: just add offsets.
				lm[4] = tm[4] + tx
				lm[5] = tm[5] + ty
				tm = lm
			}

		case "TD":
			// tx ty TD — same as -ty TL; tx ty Td
			if len(stack) >= 2 {
				tx := asFloat(stack[len(stack)-2])
				ty := asFloat(stack[len(stack)-1])
				tl = -ty
				lm[4] = tm[4] + tx
				lm[5] = tm[5] + ty
				tm = lm
			}

		case "Tm":
			// a b c d e f Tm — set text matrix.
			if len(stack) >= 6 {
				n := len(stack)
				tm = [6]float64{
					asFloat(stack[n-6]), asFloat(stack[n-5]),
					asFloat(stack[n-4]), asFloat(stack[n-3]),
					asFloat(stack[n-2]), asFloat(stack[n-1]),
				}
				lm = tm
				// Update effective font size from matrix.
				// fontSize already set by Tf.
			}

		case "T*":
			// Move to start of next line (using TL).
			lm[4] = lm[4]
			lm[5] = lm[5] - tl
			tm = lm

		case "Tj":
			// Show string.
			if len(stack) >= 1 {
				if s, ok := stack[len(stack)-1].(string); ok {
					showString(s)
				}
			}

		case "'":
			// T* then Tj.
			lm[5] = lm[5] - tl
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
				lm[5] = lm[5] - tl
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

		case "cm":
			// Ignore CTM changes for text extraction (we'd need full
			// graphics state tracking for accuracy, but Tm resets text matrix).

		case "Do":
			// Form XObject reference — skip for now.

		case "BI":
			// Begin inline image — skip until EI.
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
	// Skip until "EI" keyword preceded by whitespace.
	for {
		tok, err := lex.NextToken()
		if err != nil || tok.Type == TEOF {
			return
		}
		if tok.Type == TKeyword && tok.Str == "EI" {
			return
		}
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
			prevEnd = span.X + float64(len(span.Text))*span.FontSize*0.5
		}
		lines[i].Text = buf.String()
	}

	return lines
}

func roundTo(v float64, decimals int) float64 {
	p := math.Pow(10, float64(decimals))
	return math.Round(v*p) / p
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

// Common PostScript glyph name → Unicode mapping.
var glyphMap = map[string]rune{
	"space": ' ', "exclam": '!', "quotedbl": '"', "numbersign": '#',
	"dollar": '$', "percent": '%', "ampersand": '&', "quotesingle": '\'',
	"parenleft": '(', "parenright": ')', "asterisk": '*', "plus": '+',
	"comma": ',', "hyphen": '-', "period": '.', "slash": '/',
	"zero": '0', "one": '1', "two": '2', "three": '3', "four": '4',
	"five": '5', "six": '6', "seven": '7', "eight": '8', "nine": '9',
	"colon": ':', "semicolon": ';', "less": '<', "equal": '=',
	"greater": '>', "question": '?', "at": '@',
	"bracketleft": '[', "backslash": '\\', "bracketright": ']',
	"asciicircum": '^', "underscore": '_', "grave": '`',
	"braceleft": '{', "bar": '|', "braceright": '}', "asciitilde": '~',
	"bullet": '\u2022', "endash": '\u2013', "emdash": '\u2014',
	"ellipsis": '\u2026', "quotedblleft": '\u201C', "quotedblright": '\u201D',
	"quoteleft": '\u2018', "quoteright": '\u2019',
	"fi": '\uFB01', "fl": '\uFB02',
	"sterling": '\u00A3', "Euro": '\u20AC',
}

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
