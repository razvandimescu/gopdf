package main

import (
	"bytes"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// label is one unique translatable string found in the XFA template.
type label struct {
	RO      string
	Context string // "text" or "xhtml" — which serialization form it was found in
}

// reText matches plain XFA caption/items text nodes: <text>CONTENT</text>.
// The XFA serializer often inserts \n before > inside tags; tolerate any whitespace.
var reText = regexp.MustCompile(`(?s)<text\s*>([^<]+)</text\s*>`)

// reXHTML matches xhtml-rich label text inside <exData contentType="text/html">:
// <p style="...">CONTENT<span ...> or <p ...>CONTENT</p>. Captures one line of
// label text. Multi-line xhtml labels with internal tags are not extracted.
// The `\s` after `<p` is essential — without it the regex would also match
// other XFA tags that start with "p" (param, para, presence, etc.).
var reXHTML = regexp.MustCompile(`(?s)<p\s[^>]*>([^<\n][^<]*?)(<span[^>]*>|</p\s*>)`)

// rePara matches an entire xhtml paragraph, including inner <span> formatting.
// Used to extract the FLATTENED text content of multi-span paragraph labels
// such as `<p>Entitatea<span>&#160;</span><span style="bold">are obligația…`.
// The `\s` after `<p` is critical — see comment on reXHTML above.
var rePara = regexp.MustCompile(`(?s)<p\s[^>]*>(.*?)</p\s*>`)

// reInnerTag strips any `<...>` element from a captured paragraph body.
var reInnerTag = regexp.MustCompile(`<[^>]*>`)

// reJSString matches double-quoted string literals (used for Romanian text
// inside XFA <script> validation/error-message JavaScript). We require at
// least one space and one Romanian-looking character to avoid matching
// identifiers, paths, and form-field names.
var reJSString = regexp.MustCompile(`"([^"\\\n]{6,400})"`)

// reWhitespace collapses runs of whitespace (including non-breaking spaces)
// to a single space, so flattened paragraph text reads naturally.
var reWhitespace = regexp.MustCompile(`[\s\xa0]+`)

// extractLabels walks the decoded XFA template XML and returns unique
// translatable labels in deterministic order.
func extractLabels(xml []byte) []label {
	seen := map[string]string{} // ro → context (prefer "text" form if duplicate)

	add := func(s, ctx string) {
		s = strings.TrimSpace(s)
		if s == "" || shouldSkip(s) {
			return
		}
		// TSV uses tab as column separator — skip strings with embedded
		// tabs (some JS error messages contain `\t` literals).
		if strings.ContainsAny(s, "\t\r\n") {
			return
		}
		// Decode XML entities so the TSV reads naturally; we re-encode on apply.
		s = decodeEntities(s)
		if _, ok := seen[s]; !ok {
			seen[s] = ctx
		}
	}

	for _, m := range reText.FindAllSubmatch(xml, -1) {
		add(string(m[1]), "text")
	}
	for _, m := range reXHTML.FindAllSubmatch(xml, -1) {
		add(string(m[1]), "xhtml")
	}
	// Form C: flattened paragraph text (catches labels split across <span>s).
	// Skip paragraphs that contain table-cell formatting widgets or JS code —
	// those mix multiple labels and widgets into one <p> and produce noise.
	for _, m := range rePara.FindAllSubmatch(xml, -1) {
		if bytes.Contains(m[1], []byte("num{")) ||
			bytes.Contains(m[1], []byte("this.rawValue")) ||
			bytes.Contains(m[1], []byte("if (")) ||
			bytes.Contains(m[1], []byte("Calcul")) {
			continue
		}
		flat := reInnerTag.ReplaceAll(m[1], nil)
		flat = []byte(decodeEntities(string(flat)))
		flat = reWhitespace.ReplaceAll(flat, []byte(" "))
		s := strings.TrimSpace(string(flat))
		// Skip extremely long flattened paragraphs (likely concatenated cells).
		// Legitimate footnote labels can exceed 400 chars; bump to 1500.
		if len(s) > 1500 {
			continue
		}
		add(s, "para")
	}
	// Form D: JavaScript string literals (catches validation/error messages
	// like "F10 - BILANT PRESCURTAT", long form descriptions, etc.).
	for _, m := range reJSString.FindAllSubmatch(xml, -1) {
		s := string(m[1])
		if !plausibleJSLabel(s) {
			continue
		}
		add(s, "js")
	}

	out := make([]label, 0, len(seen))
	for ro, ctx := range seen {
		out = append(out, label{RO: ro, Context: ctx})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].RO < out[j].RO
	})
	return out
}

// shouldSkip filters out strings that don't need translation: pure numbers,
// account-code parentheticals, county names, version stamps, etc.
func shouldSkip(s string) bool {
	if len(s) == 0 {
		return true
	}
	// Pure numeric.
	if reAllDigits.MatchString(s) {
		return true
	}
	// Single-character codes.
	if len([]rune(s)) <= 1 {
		return true
	}
	// "1.", "2)", "12.", etc. — pure ordinals.
	if reOrdinal.MatchString(s) {
		return true
	}
	// Row codes: "01a", "105b", "07a".
	if reRowCode.MatchString(s) {
		return true
	}
	// Footnote markers: "*)", "**)", "***)".
	if reFootnote.MatchString(s) {
		return true
	}
	// Sector NN (Bucharest districts).
	if strings.HasPrefix(s, "Sector ") && len(s) <= 9 {
		return true
	}
	// Pure account-code parentheticals (`(ct. 211 + 212 - 2811)`) — skip when
	// there is nothing translatable inside (just account numbers and
	// operators). Longer parenthetical labels with "din", "rd", commas, or
	// prose are kept so the user sees them rendered in English.
	if strings.HasPrefix(s, "(ct.") && strings.HasSuffix(strings.TrimSpace(s), ")") &&
		!strings.Contains(s, ",") && !strings.Contains(s, "din ") &&
		!strings.Contains(s, " la ") {
		return true
	}
	// "(NNN)" parenthesized row codes.
	if reParenNum.MatchString(s) {
		return true
	}
	// "1=2+3" style formulas.
	if isFormula(s) {
		return true
	}
	// Adobe version / template stamps.
	if strings.HasPrefix(s, "S1003_") || strings.HasPrefix(s, "S1002_") ||
		strings.HasPrefix(s, "8.0.") || strings.HasPrefix(s, "/ 25.") {
		return true
	}
	// County names — leave Romanian.
	if _, isCounty := countyNames[s]; isCounty {
		return true
	}
	return false
}

var (
	reAllDigits = regexp.MustCompile(`^[0-9]+$`)
	reOrdinal   = regexp.MustCompile(`^[0-9]+[.)]$`)
	reRowCode   = regexp.MustCompile(`^[0-9]{1,3}[a-z]$`)
	reFootnote  = regexp.MustCompile(`^\*+\)$`)
	reParenNum  = regexp.MustCompile(`^\([0-9]+\)$`)
)

// plausibleJSLabel keeps JavaScript string literals that look like user-
// visible Romanian sentences (typically validation error messages, form
// section names), rejecting identifiers, code fragments, NACE codes,
// XFA SOM expressions, and font/glyph metadata.
func plausibleJSLabel(s string) bool {
	if len(s) < 10 || len(s) > 500 {
		return false
	}
	// XFA SOM (Scripting Object Model) expressions and field paths.
	if strings.Contains(s, "form1.") || strings.Contains(s, ".rawValue") ||
		strings.Contains(s, "\\\"") || strings.Contains(s, "//") ||
		strings.Contains(s, "renderCache") || strings.Contains(s, "UTF-16") {
		return false
	}
	// JavaScript code fragments: contain operators or var-name patterns.
	if strings.ContainsAny(s, "{}=") || strings.Contains(s, "==") ||
		strings.Contains(s, "&&") || strings.Contains(s, "||") {
		return false
	}
	// Concatenation fragments (start/end with + or comma-only).
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "+") || strings.HasSuffix(t, "+") ||
		strings.HasPrefix(t, ",") || strings.HasPrefix(t, ".") {
		return false
	}
	// XFA field-name references (FOO_BAR[0], FOO[12]).
	if strings.HasSuffix(t, "]") && strings.Contains(t, "[") {
		return false
	}
	// XFA SOM colon paths (`:declaratie:v15`).
	if strings.HasPrefix(t, ":") {
		return false
	}
	// CSV-like data fixtures with many commas.
	if strings.Count(t, ",") > 4 && !strings.Contains(t, " ") {
		return false
	}
	if strings.Count(t, ",") > 8 {
		return false
	}
	// Uppercase-only field-name tokens (AP_LOCALITATE, ADMINISTRATOR,).
	if reUpperFieldName.MatchString(t) {
		return false
	}
	// NACE codes: "NNNN--Description" — keep Romanian (official classification).
	if reNACE.MatchString(s) {
		return false
	}
	// Require a fair density of letters (catches `+ v1 +` style fragments).
	letters := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters < 8 {
		return false
	}
	// Must contain a real word (4+ consecutive letters).
	if !reHasWord.MatchString(s) {
		return false
	}
	return true
}

var (
	reNACE           = regexp.MustCompile(`^\d{3,4}--`)
	reHasWord        = regexp.MustCompile(`\pL{4,}`)
	reUpperFieldName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*,?$`)
)

func isFormula(s string) bool {
	for _, r := range s {
		if !(unicode.IsDigit(r) || r == '+' || r == '-' || r == '=' || r == ' ') {
			return false
		}
	}
	return true
}

var countyNames = map[string]bool{
	"Alba": true, "Arad": true, "Arges": true, "Bacau": true, "Bihor": true,
	"Bistrita-Nasaud": true, "Botosani": true, "Brasov": true, "Braila": true,
	"Bucuresti": true, "Buzau": true, "Caras-Severin": true, "Calarasi": true,
	"Cluj": true, "Constanta": true, "Covasna": true, "Dambovita": true,
	"Dolj": true, "Galati": true, "Giurgiu": true, "Gorj": true, "Harghita": true,
	"Hunedoara": true, "Ialomita": true, "Iasi": true, "Ilfov": true,
	"Maramures": true, "Mehedinti": true, "Mures": true, "Neamt": true,
	"Olt": true, "Prahova": true, "Satu Mare": true, "Salaj": true, "Sibiu": true,
	"Suceava": true, "Teleorman": true, "Timis": true, "Tulcea": true,
	"Valcea": true, "Vaslui": true, "Vrancea": true,
}

func decodeEntities(s string) string {
	r := strings.NewReplacer(
		"&lt;", "<",
		"&gt;", ">",
		"&amp;", "&",
		"&apos;", "'",
		"&quot;", "\"",
	)
	return r.Replace(s)
}

func encodeEntities(s string) string {
	// Don't encode quote/apos — they're not significant in text content.
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return r.Replace(s)
}
