package pdf

import (
	"maps"
	"strings"
)

// StdFontWidths returns character widths (in 1/1000 units) for standard 14 fonts.
// Returns nil if the font is not a standard font. The returned map is a copy;
// callers may mutate it freely.
func StdFontWidths(baseName string) map[int]float64 {
	w := stdFontWidths(baseName)
	if w == nil {
		return nil
	}
	return maps.Clone(w)
}

// stdFontWidths returns the shared internal width map for standard 14 fonts.
// The result must be treated as read-only — it is reused across calls.
func stdFontWidths(baseName string) map[int]float64 {
	// Strip subset prefix (e.g., "ABCDEF+Helvetica" → "Helvetica").
	if idx := strings.Index(baseName, "+"); idx >= 0 {
		baseName = baseName[idx+1:]
	}

	switch baseName {
	case "Courier", "Courier-Bold", "Courier-Oblique", "Courier-BoldOblique",
		"CourierNew", "CourierNewPSMT", "CourierNewPS-BoldMT",
		"CourierNewPS-ItalicMT", "CourierNewPS-BoldItalicMT":
		return courierWidths()
	case "Helvetica", "Helvetica-Oblique",
		"ArialMT", "Arial", "Arial-ItalicMT":
		return helveticaWidths
	case "Helvetica-Bold", "Helvetica-BoldOblique",
		"Arial-BoldMT", "Arial-Bold", "Arial-BoldItalicMT":
		return helveticaBoldWidths
	// Times-Italic and Times-BoldItalic are mapped to the upright Times
	// metrics as a deliberate metric-compatible fallback. Italic Times is
	// a redesigned face with its own AFM (several glyph widths differ from
	// the upright by ~5–20 units), but reusing the upright metrics is much
	// closer than the 0.6 default and avoids shipping a fourth width table.
	case "Times-Roman", "Times-Italic",
		"TimesNewRomanPSMT", "TimesNewRoman", "TimesNewRomanPS-ItalicMT":
		return timesRomanWidths
	case "Times-Bold", "Times-BoldItalic",
		"TimesNewRomanPS-BoldMT", "TimesNewRoman-Bold", "TimesNewRomanPS-BoldItalicMT":
		return timesBoldWidths
	default:
		return nil
	}
}

// HelveticaTextWidth returns the width of a string rendered in Helvetica
// at the given font size (in PDF points).
func HelveticaTextWidth(text string, fontSize float64) float64 {
	var total float64
	for _, r := range text {
		w, ok := helveticaWidths[int(r)]
		if !ok {
			w = 556 // average width fallback
		}
		total += w
	}
	return total / 1000.0 * fontSize
}

func courierWidths() map[int]float64 {
	m := make(map[int]float64, 256)
	for i := 0; i < 256; i++ {
		m[i] = 600
	}
	return m
}

// Helvetica widths for characters 32-255 (from AFM data).
var helveticaWidths = map[int]float64{
	32: 278, 33: 278, 34: 355, 35: 556, 36: 556, 37: 889, 38: 667, 39: 191,
	40: 333, 41: 333, 42: 389, 43: 584, 44: 278, 45: 333, 46: 278, 47: 278,
	48: 556, 49: 556, 50: 556, 51: 556, 52: 556, 53: 556, 54: 556, 55: 556,
	56: 556, 57: 556, 58: 278, 59: 278, 60: 584, 61: 584, 62: 584, 63: 556,
	64: 1015, 65: 667, 66: 667, 67: 722, 68: 722, 69: 667, 70: 611, 71: 778,
	72: 722, 73: 278, 74: 500, 75: 667, 76: 556, 77: 833, 78: 722, 79: 778,
	80: 667, 81: 778, 82: 722, 83: 667, 84: 611, 85: 722, 86: 667, 87: 944,
	88: 667, 89: 667, 90: 611, 91: 278, 92: 278, 93: 278, 94: 469, 95: 556,
	96: 333, 97: 556, 98: 556, 99: 500, 100: 556, 101: 556, 102: 278, 103: 556,
	104: 556, 105: 222, 106: 222, 107: 500, 108: 222, 109: 833, 110: 556, 111: 556,
	112: 556, 113: 556, 114: 333, 115: 500, 116: 278, 117: 556, 118: 500, 119: 722,
	120: 500, 121: 500, 122: 500, 123: 334, 124: 260, 125: 334, 126: 584,
	160: 278, 161: 333, 162: 556, 163: 556, 164: 556, 165: 556, 166: 260,
	167: 556, 168: 333, 169: 737, 170: 370, 171: 556, 172: 584, 173: 333,
	174: 737, 175: 333, 176: 400, 177: 584, 178: 333, 179: 333, 180: 333,
	181: 556, 182: 537, 183: 278, 184: 333, 185: 333, 186: 365, 187: 556,
	188: 834, 189: 834, 190: 834, 191: 611, 192: 667, 193: 667, 194: 667,
	195: 667, 196: 667, 197: 667, 198: 1000, 199: 722, 200: 667, 201: 667,
	202: 667, 203: 667, 204: 278, 205: 278, 206: 278, 207: 278, 208: 722,
	209: 722, 210: 778, 211: 778, 212: 778, 213: 778, 214: 778, 215: 584,
	216: 778, 217: 722, 218: 722, 219: 722, 220: 722, 221: 667, 222: 667,
	223: 611, 224: 556, 225: 556, 226: 556, 227: 556, 228: 556, 229: 556,
	230: 889, 231: 500, 232: 556, 233: 556, 234: 556, 235: 556, 236: 278,
	237: 278, 238: 278, 239: 278, 240: 556, 241: 556, 242: 556, 243: 556,
	244: 556, 245: 556, 246: 556, 247: 584, 248: 611, 249: 556, 250: 556,
	251: 556, 252: 556, 253: 500, 254: 556, 255: 500,
}

var helveticaBoldWidths = map[int]float64{
	32: 278, 33: 333, 34: 474, 35: 556, 36: 556, 37: 889, 38: 722, 39: 238,
	40: 333, 41: 333, 42: 389, 43: 584, 44: 278, 45: 333, 46: 278, 47: 278,
	48: 556, 49: 556, 50: 556, 51: 556, 52: 556, 53: 556, 54: 556, 55: 556,
	56: 556, 57: 556, 58: 333, 59: 333, 60: 584, 61: 584, 62: 584, 63: 611,
	64: 975, 65: 722, 66: 722, 67: 722, 68: 722, 69: 667, 70: 611, 71: 778,
	72: 722, 73: 278, 74: 556, 75: 722, 76: 611, 77: 833, 78: 722, 79: 778,
	80: 667, 81: 778, 82: 722, 83: 667, 84: 611, 85: 722, 86: 667, 87: 944,
	88: 667, 89: 667, 90: 611, 91: 333, 92: 278, 93: 333, 94: 584, 95: 556,
	96: 333, 97: 556, 98: 611, 99: 556, 100: 611, 101: 556, 102: 333, 103: 611,
	104: 611, 105: 278, 106: 278, 107: 556, 108: 278, 109: 889, 110: 611, 111: 611,
	112: 611, 113: 611, 114: 389, 115: 556, 116: 333, 117: 611, 118: 556, 119: 778,
	120: 556, 121: 556, 122: 500, 123: 389, 124: 280, 125: 389, 126: 584,
}

var timesRomanWidths = map[int]float64{
	32: 250, 33: 333, 34: 408, 35: 500, 36: 500, 37: 833, 38: 778, 39: 180,
	40: 333, 41: 333, 42: 500, 43: 564, 44: 250, 45: 333, 46: 250, 47: 278,
	48: 500, 49: 500, 50: 500, 51: 500, 52: 500, 53: 500, 54: 500, 55: 500,
	56: 500, 57: 500, 58: 278, 59: 278, 60: 564, 61: 564, 62: 564, 63: 444,
	64: 921, 65: 722, 66: 667, 67: 667, 68: 722, 69: 611, 70: 556, 71: 722,
	72: 722, 73: 333, 74: 389, 75: 722, 76: 611, 77: 889, 78: 722, 79: 722,
	80: 556, 81: 722, 82: 667, 83: 556, 84: 611, 85: 722, 86: 722, 87: 944,
	88: 722, 89: 722, 90: 611, 91: 333, 92: 278, 93: 333, 94: 469, 95: 500,
	96: 333, 97: 444, 98: 500, 99: 444, 100: 500, 101: 444, 102: 333, 103: 500,
	104: 500, 105: 278, 106: 278, 107: 500, 108: 278, 109: 778, 110: 500, 111: 500,
	112: 500, 113: 500, 114: 333, 115: 389, 116: 278, 117: 500, 118: 500, 119: 722,
	120: 500, 121: 500, 122: 444, 123: 480, 124: 200, 125: 480, 126: 541,
}

var timesBoldWidths = map[int]float64{
	32: 250, 33: 333, 34: 555, 35: 500, 36: 500, 37: 1000, 38: 833, 39: 278,
	40: 333, 41: 333, 42: 500, 43: 570, 44: 250, 45: 333, 46: 250, 47: 278,
	48: 500, 49: 500, 50: 500, 51: 500, 52: 500, 53: 500, 54: 500, 55: 500,
	56: 500, 57: 500, 58: 333, 59: 333, 60: 570, 61: 570, 62: 570, 63: 500,
	64: 930, 65: 722, 66: 667, 67: 722, 68: 722, 69: 667, 70: 611, 71: 778,
	72: 778, 73: 389, 74: 500, 75: 778, 76: 667, 77: 944, 78: 722, 79: 778,
	80: 611, 81: 778, 82: 722, 83: 556, 84: 667, 85: 722, 86: 722, 87: 1000,
	88: 722, 89: 722, 90: 667, 91: 333, 92: 278, 93: 333, 94: 581, 95: 500,
	96: 333, 97: 500, 98: 556, 99: 444, 100: 556, 101: 444, 102: 333, 103: 500,
	104: 556, 105: 278, 106: 333, 107: 556, 108: 278, 109: 833, 110: 556, 111: 500,
	112: 556, 113: 556, 114: 444, 115: 389, 116: 333, 117: 556, 118: 500, 119: 722,
	120: 500, 121: 500, 122: 444, 123: 394, 124: 220, 125: 394, 126: 520,
}
