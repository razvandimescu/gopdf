package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/razvandimescu/gopdf/pdf"
)

// reScriptBlock matches a full <script ...>...</script> element. Used to
// isolate script bodies from byte-global post-process rewrites so we cannot
// accidentally damage value-computing JavaScript (calculate, validate, event
// handlers) or comparison strings used in form logic.
var reScriptBlock = regexp.MustCompile(`(?s)<script\b[^>]*>.*?</script\s*>`)

// hideUnusedFormVariants hardcodes presence="hidden" on subform variants the
// entity's datasets indicate should not be shown. The source PDF carries an
// Adobe Reader Extensions (UR3) signature whose ByteRange is invalidated by
// any content edit; with the signature broken Reader silently disables the
// rights that let initialize-time scripts mutate `.presence`, so unused
// variants stay visible as empty pages. We replicate the visibility table
// from the form's own pune_parametri()/ascunde_formulare() scripts using the
// entity's tipAS (annual vs semi-annual), rbl (large/small/micro), and
// checkFF (annual-statements vs annual-reports) flags.
func hideUnusedFormVariants(template, datasets []byte) []byte {
	rbl := xfaDatasetField(datasets, "rbl")
	tipAS := xfaDatasetField(datasets, "tipAS")
	checkFF := xfaDatasetField(datasets, "checkFF")

	// Top-level subforms under form1 — hidden by name.
	topHide := map[string]bool{}
	// F30 child tables — hidden by name within the F30 subform span.
	f30Hide := map[string]bool{}
	// F30.Table7_MM_an child rows — hidden by name within that table's span.
	tab7MMAnRowHide := map[string]bool{}

	switch tipAS {
	case "1": // anual
		switch rbl {
		case "1": // mari
			topHide["F10S"] = true
			topHide["F20S"] = true
			f30Hide["Table71_an"] = true
			f30Hide["Table7_an"] = true
			f30Hide["Table5"] = true
			f30Hide["Table6"] = true
			f30Hide["Table8_an_micro"] = true
			f30Hide["Table7_sem"] = true
			f30Hide["Table8_sem_micro"] = true
			f30Hide["Table7_MM_sem"] = true
		case "2": // mici
			topHide["F10L"] = true
			topHide["F20S"] = true
			f30Hide["Table71_an"] = true
			f30Hide["Table7_an"] = true
			f30Hide["Table5"] = true
			f30Hide["Table6"] = true
			f30Hide["Table8_an_micro"] = true
			f30Hide["Table7_sem"] = true
			f30Hide["Table8_sem_micro"] = true
			f30Hide["Table7_MM_sem"] = true
		case "3": // micro
			topHide["F10L"] = true
			topHide["F20"] = true
			f30Hide["Table5_MM"] = true
			f30Hide["Table6_MM"] = true
			f30Hide["Table71_MM_an"] = true
			f30Hide["Table7_MM_an"] = true
			f30Hide["Table7_MM_sem"] = true
			f30Hide["Table7_sem"] = true
			f30Hide["Table8_sem_micro"] = true
		}
	case "2": // semestrial
		topHide["F40"] = true
		switch rbl {
		case "1", "2": // mari/mici
			topHide["F10L"] = true
			topHide["F20S"] = true
			f30Hide["Table5"] = true
			f30Hide["Table6"] = true
			f30Hide["Table71_an"] = true
			f30Hide["Table7_an"] = true
			f30Hide["Table8_an_micro"] = true
			f30Hide["Table71_MM_an"] = true
			f30Hide["Table7_MM_an"] = true
			f30Hide["Table7_sem"] = true
			f30Hide["Table8_sem_micro"] = true
		case "3": // micro
			topHide["F10L"] = true
			topHide["F20"] = true
			f30Hide["Table5_MM"] = true
			f30Hide["Table6_MM"] = true
			f30Hide["Table71_an"] = true
			f30Hide["Table7_an"] = true
			f30Hide["Table8_an_micro"] = true
			f30Hide["Table71_MM_an"] = true
			f30Hide["Table7_MM_an"] = true
			f30Hide["Table7_MM_sem"] = true
		}
	}
	// Pillar-Two minimum-tax rows R326/R327 only appear when the entity
	// files annual reports (checkFF==1); standard annual statements
	// (checkFF==0) keep them hidden.
	if checkFF != "1" {
		tab7MMAnRowHide["R326"] = true
		tab7MMAnRowHide["R327"] = true
	}

	for name := range topHide {
		template = setSubformPresence(template, name, "hidden")
	}
	for name := range f30Hide {
		template = setSubformPresenceInParent(template, "F30", name, "hidden")
	}
	for name := range tab7MMAnRowHide {
		template = setSubformPresenceInParent(template, "Table7_MM_an", name, "hidden")
	}
	return template
}

// setSubformPresenceInParent locates the first top-level subform whose name
// is `parent`, then within its span finds and updates the first subform whose
// name is `child`.
func setSubformPresenceInParent(template []byte, parent, child, presence string) []byte {
	openRe := regexp.MustCompile(`<subform\s+[^>]*\bname="` + regexp.QuoteMeta(parent) + `"[^>]*>`)
	open := openRe.FindIndex(template)
	if open == nil {
		return template
	}
	end := findSubformEnd(template, open[1])
	if end < 0 {
		return template
	}
	patched := setSubformPresence(template[open[1]:end], child, presence)
	out := make([]byte, 0, len(template))
	out = append(out, template[:open[1]]...)
	out = append(out, patched...)
	out = append(out, template[end:]...)
	return out
}

// findSubformEnd returns the byte offset just past the </subform> tag that
// closes the subform whose body starts at `bodyStart`. Returns -1 if the
// closing tag isn't found.
func findSubformEnd(template []byte, bodyStart int) int {
	depth := 1
	i := bodyStart
	openTag := []byte("<subform ")
	closeTag := []byte("</subform")
	for i < len(template) {
		switch {
		case i+len(openTag) <= len(template) && bytes.Equal(template[i:i+len(openTag)], openTag):
			depth++
			i += len(openTag)
		case i+len(closeTag) <= len(template) && bytes.Equal(template[i:i+len(closeTag)], closeTag):
			depth--
			// advance past `</subform` then the closing `>`
			i += len(closeTag)
			for i < len(template) && template[i] != '>' {
				i++
			}
			if i < len(template) {
				i++
			}
			if depth == 0 {
				return i
			}
		default:
			i++
		}
	}
	return -1
}

// xfaDatasetField returns the text content of <Name>...</Name> in the XFA
// datasets stream. Returns "" if the field is missing or self-closing.
func xfaDatasetField(datasets []byte, name string) string {
	// Tags are written with a leading newline before the > (XFA quirk),
	// e.g. `<rbl\n>2</rbl\n>`.
	re := regexp.MustCompile(`<` + regexp.QuoteMeta(name) + `\s*>([^<]*)</` + regexp.QuoteMeta(name) + `\s*>`)
	m := re.FindSubmatch(datasets)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(string(m[1]))
}

// setSubformPresence locates the first top-level <subform name="X" ...> tag
// matching `name` and adds (or replaces) a presence="value" attribute on it.
// Only the opening tag is rewritten — the subform body is untouched.
func setSubformPresence(template []byte, name, presence string) []byte {
	re := regexp.MustCompile(`<subform\s+([^>]*\bname="` + regexp.QuoteMeta(name) + `"[^>]*)>`)
	return re.ReplaceAllFunc(template, func(m []byte) []byte {
		attrs := re.FindSubmatch(m)[1]
		if bytes.Contains(attrs, []byte("presence=\"")) {
			reP := regexp.MustCompile(`presence="[^"]*"`)
			attrs = reP.ReplaceAll(attrs, []byte(`presence="`+presence+`"`))
		} else {
			attrs = append(attrs, ' ')
			attrs = append(attrs, []byte(`presence="`+presence+`"`)...)
		}
		return []byte("<subform " + string(attrs) + ">")
	})
}

// applyOutsideScripts runs fn on every byte region of xml that is NOT inside
// a <script>...</script> element, leaving script bodies byte-identical.
func applyOutsideScripts(xml []byte, fn func([]byte) []byte) []byte {
	matches := reScriptBlock.FindAllIndex(xml, -1)
	if len(matches) == 0 {
		return fn(xml)
	}
	var out bytes.Buffer
	out.Grow(len(xml))
	cursor := 0
	for _, m := range matches {
		out.Write(fn(xml[cursor:m[0]]))
		out.Write(xml[m[0]:m[1]])
		cursor = m[1]
	}
	out.Write(fn(xml[cursor:]))
	return out.Bytes()
}

// postProcessFixes performs targeted byte-level fixes for labels that
// straddle XFA span boundaries and aren't reachable by the regex-based label
// passes. Each entry rewrites a multi-element source fragment as a clean
// translated equivalent.
//
// All byte-global rewrites (the structural fix table and the Romanian
// abbreviation table) are applied OUTSIDE <script> bodies only — value-
// computing JavaScript, message strings, and comparison literals inside
// <calculate>/<validate>/<event> handlers are left byte-identical.
func postProcessFixes(xml []byte) []byte {
	type fix struct {
		find, replace []byte
	}
	fixes := []fix{
		{
			// "Bifati numai dacă este cazul :" with the trailing "ă" wrapped
			// in a styled <span> and a non-breaking-space <span>, plus a <p>
			// break before "este cazul :". Render as two clean paragraphs.
			find:    []byte("<p\n>Bifati numai dac<span style=\"text-decoration:none\"\n>ă</span\n><span style=\"xfa-spacerun:yes\"\n>\xc2\xa0</span\n></p\n><p\n>este cazul :</p"),
			replace: []byte("<p\n>Tick only</p\n><p\n>if applicable:</p"),
		},
		{
			// Validation error message JS string containing an embedded TAB
			// character: `"ATENTIE !\tConform prevederilor pct.1.11 alin 4 …"`.
			// TSV-based label substitution can't carry tab-bearing strings,
			// so we replace the Romanian portion byte-for-byte.
			find:    []byte("ATENTIE !\tConform prevederilor pct.1.11 alin 4 din Anexa nr. 1 la OMFP nr. 2206/ 2020, 'în vederea depunerii situațiilor financiare anuale aferente exercitiului financiar "),
			replace: []byte("ATTENTION!\tPer pt. 1.11 para 4 of Annex 1 to OMFP no. 2206/2020, 'for filing the annual financial statements for the financial year "),
		},
		{
			find:    []byte("ATENTIE !\tConform prevederilor pct.1.11 alin 4 din Anexa nr. 1 la OMFP nr. 2206/ 2020, arhiva va contine si prima paginã din situațiile financiare anuale aferente exercitiului financiar"),
			replace: []byte("ATTENTION!\tPer pt. 1.11 para 4 of Annex 1 to OMFP no. 2206/2020, the archive will also contain the first page of the annual financial statements for the financial year"),
		},
	}
	xml = applyOutsideScripts(xml, func(chunk []byte) []byte {
		for _, f := range fixes {
			chunk = bytes.ReplaceAll(chunk, f.find, f.replace)
		}
		return chunk
	})

	// Global normalisation of Romanian fiscal abbreviations that leak through
	// in parenthetical chart-of-accounts / row references. Pattern is "ct."
	// (cont = account), "rd." (rând = row), and "din ct." (from account).
	// Applied AFTER labels so it catches both freshly-translated and any
	// untranslated text that still contains these tokens.
	abbrevs := []fix{
		{find: []byte("(din ct."), replace: []byte("(from acc.")},
		{find: []byte(" din ct."), replace: []byte(" from acc.")},
		{find: []byte("(ct."), replace: []byte("(acc.")},
		{find: []byte(" ct."), replace: []byte(" acc.")},
		{find: []byte("(rd."), replace: []byte("(row ")},
		{find: []byte(" rd."), replace: []byte(" row ")},
		// At the start of an element body (right after `>` of opening tag),
		// e.g. `<span>rd. 191…</span>`. Also handles `>ct.` and `>din ct.`.
		{find: []byte(">rd."), replace: []byte(">row ")},
		{find: []byte(">ct."), replace: []byte(">acc.")},
		{find: []byte(">din ct."), replace: []byte(">from acc.")},
		// Inside formulas: "+ct.", ",ct.", "-ct." (without leading space).
		{find: []byte("+ct."), replace: []byte("+acc.")},
		{find: []byte(",ct."), replace: []byte(",acc.")},
		{find: []byte("-ct."), replace: []byte("-acc.")},
	}
	xml = applyOutsideScripts(xml, func(chunk []byte) []byte {
		for _, a := range abbrevs {
			chunk = bytes.ReplaceAll(chunk, a.find, a.replace)
		}
		return chunk
	})

	// `row N la M` (Romanian "la" = "to") — only translate when both sides
	// are digits so we don't accidentally rewrite English text containing
	// the word "la".
	reLa := regexp.MustCompile(`(row\s+[0-9a-z]+)\s+la\s+([0-9a-z]+)`)
	xml = applyOutsideScripts(xml, func(chunk []byte) []byte {
		return reLa.ReplaceAll(chunk, []byte("$1 to $2"))
	})
	return xml
}

// runApply rewrites the XFA template with EN translations from the TSV and
// writes a new PDF.
func runApply(inPDF, mapTSV, outPDF string) error {
	mapping, err := loadTSV(mapTSV)
	if err != nil {
		return err
	}
	if len(mapping) == 0 {
		return fmt.Errorf("no translations in %s — fill the EN column first", mapTSV)
	}

	data, err := os.ReadFile(inPDF)
	if err != nil {
		return err
	}
	r, err := pdf.Open(data)
	if err != nil {
		return err
	}
	objNum, xml := r.FindXFATemplate()
	if objNum == 0 {
		return fmt.Errorf("no XFA template stream found in %s", inPDF)
	}
	_, datasets := r.FindXFAStream("datasets")

	translated, hits := applyTranslations(xml, mapping)
	translated = postProcessFixes(translated)
	if len(datasets) > 0 {
		translated = hideUnusedFormVariants(translated, datasets)
	}

	subs := map[int][]byte{objNum: translated}
	out, err := r.Rewrite(subs)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPDF, out, 0644); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "translated %d/%d labels (%d total hits in XFA), wrote %s (%d bytes)\n",
		len(hits), len(mapping), sumHits(hits), outPDF, len(out))
	// Report any mapping rows that didn't match — likely a transcription drift.
	missed := 0
	for ro := range mapping {
		if hits[ro] == 0 {
			missed++
		}
	}
	if missed > 0 {
		fmt.Fprintf(os.Stderr, "warning: %d mapping entries had zero matches in the XFA template\n", missed)
	}
	return nil
}

func sumHits(h map[string]int) int {
	n := 0
	for _, v := range h {
		n += v
	}
	return n
}

// applyTranslations performs in-place RO→EN substitution inside the XFA XML.
// Returns the modified bytes and a per-label hit count.
//
// Replacement forms (tried in this order for each label):
//
//	Form A: <text\s*>(\s*)RO(\s*)</text\s*>   plain text node
//	Form B: (<p[^>]*>)(\s*)RO(\s*)(<span|</p) xhtml leading-paragraph text
//	Form C: <p[^>]*>INNER</p\s*>              whole-paragraph flatten-and-replace
//	Form D: "RO"                              JavaScript string literal
//
// We sort keys by descending length so longer phrases match before any of
// their substring overlaps (e.g. "Cod 10" before "Cod"). Form C is matched
// against the FLATTENED inner content of each <p>, so labels split across
// <span>s are still translated as a single unit.
func applyTranslations(xml []byte, mapping map[string]string) ([]byte, map[string]int) {
	keys := make([]string, 0, len(mapping))
	for k := range mapping {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })

	hits := make(map[string]int, len(mapping))
	cur := xml

	// Pass 1: Form C FIRST — flattened paragraph replacement. Walks each
	// <p>...</p>, flattens to compare against the mapping keys, replaces
	// the entire body with <p>EN</p> when matched. Must run before A/B so
	// that A/B doesn't mutate paragraph contents into something Form C
	// can no longer match against the original RO key.
	cur = rePara.ReplaceAllFunc(cur, func(m []byte) []byte {
		groups := rePara.FindSubmatch(m)
		flat := reInnerTag.ReplaceAll(groups[1], nil)
		flat = []byte(decodeEntities(string(flat)))
		flat = reWhitespace.ReplaceAll(flat, []byte(" "))
		key := strings.TrimSpace(string(flat))
		if key == "" {
			return m
		}
		if en, ok := mapping[key]; ok {
			hits[key]++
			// Preserve the original <p ...> opening tag and </p> closing tag.
			openEnd := bytes.IndexByte(m, '>') + 1
			closeStart := bytes.LastIndex(m, []byte("</p"))
			return append(append(append([]byte{}, m[:openEnd]...), []byte(encodeEntities(en))...), m[closeStart:]...)
		}
		return m
	})

	// Pass 2: Form A + Form B (anchored). Whitespace-tolerant boundaries.
	// Runs after Form C so any leftover untranslated text nodes still get
	// caught by anchored replacement.
	for _, ro := range keys {
		en := mapping[ro]
		roEnc := encodeEntities(ro)
		enEnc := encodeEntities(en)
		patA := `<text(\s*)>(\s*)` + regexp.QuoteMeta(roEnc) + `(\s*)</text(\s*)>`
		patB := `(<p\s[^>]*>)(\s*)` + regexp.QuoteMeta(roEnc) + `(\s*)(<span|</p)`
		reA := regexp.MustCompile(patA)
		reB := regexp.MustCompile(patB)
		var nA, nB int
		cur = reA.ReplaceAllFunc(cur, func(m []byte) []byte {
			nA++
			groups := reA.FindSubmatch(m)
			return []byte(fmt.Sprintf("<text%s>%s%s%s</text%s>",
				groups[1], groups[2], enEnc, groups[3], groups[4]))
		})
		cur = reB.ReplaceAllFunc(cur, func(m []byte) []byte {
			nB++
			groups := reB.FindSubmatch(m)
			return []byte(fmt.Sprintf("%s%s%s%s%s",
				groups[1], groups[2], enEnc, groups[3], groups[4]))
		})
		hits[ro] += nA + nB
	}

	// Pass 3: Form D — JavaScript string literals. Substring-replace
	// `"RO"` with `"EN"` anywhere the exact quoted form appears. Safe
	// because Romanian phrases rarely appear inside unrelated JS code.
	for _, ro := range keys {
		if len(ro) < 6 {
			continue
		}
		en := mapping[ro]
		find := []byte(`"` + ro + `"`)
		replace := []byte(`"` + en + `"`)
		n := bytes.Count(cur, find)
		if n > 0 {
			cur = bytes.ReplaceAll(cur, find, replace)
			hits[ro] += n
		}
	}

	return cur, hits
}

// loadTSV reads a tab-separated mapping file (ro, en, [context]). Rows with
// an empty EN column are skipped (used to explicitly leave a label alone).
func loadTSV(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := make(map[string]string)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		ro := parts[0]
		en := parts[1]
		if ro == "" || en == "" {
			continue
		}
		m[ro] = en
	}
	return m, scanner.Err()
}
