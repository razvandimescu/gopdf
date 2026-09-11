package pdf

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// The private tier: an RFQ printed through Microsoft Print to PDF, the labels
// of its shapes, and a reference transcript of its outlined pages. The
// transcript was produced by a vision model, not by hand, and has errors of its
// own; every disagreement below was checked against the rendered pages.

// refText is a reference with its whitespace removed and look-alikes folded,
// remembering where the whitespace was.
type refText struct {
	text string
	brk  []bool // brk[i]: whitespace, or the end, precedes byte i
}

var lookAlikeFold = strings.NewReplacer("I", "l", "|", "l", "'", ",", "’", ",")

func foldLabel(s string) string {
	return lookAlikeFold.Replace(strings.Join(strings.Fields(s), ""))
}

func newRefText(raw string) refText {
	var b strings.Builder
	var brk []bool
	ws := false
	for _, r := range raw {
		if unicode.IsSpace(r) {
			ws = true
			continue
		}
		s := lookAlikeFold.Replace(string(r))
		for i := range len(s) {
			brk = append(brk, ws && i == 0)
		}
		b.WriteString(s)
		ws = false
	}
	return refText{b.String(), append(brk, true)}
}

// wordMatches returns where s occurs with whitespace on both sides.
func (t refText) wordMatches(s string) []int {
	var at []int
	for i := strings.Index(t.text, s); i >= 0; {
		if t.brk[i] && t.brk[i+len(s)] {
			at = append(at, i)
		}
		next := strings.Index(t.text[i+1:], s)
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return at
}

// runText is a run's labels, folded as the reference is.
func runText(r *run) string {
	var s strings.Builder
	for _, g := range r.glyphs {
		s.WriteString(foldLabel(g.label))
	}
	return s.String()
}

// runTruth is the word-break pattern of a run, when every place its text
// occurs in the reference agrees on one.
func (t refText) runTruth(r *run) ([]bool, bool) {
	var truth []bool
	found := false
	for _, at := range t.wordMatches(runText(r)) {
		var pat []bool
		pos := at
		for _, g := range r.glyphs[:len(r.glyphs)-1] {
			pos += len(foldLabel(g.label))
			pat = append(pat, t.brk[pos])
		}
		if found && !slices.Equal(pat, truth) {
			return nil, false
		}
		truth, found = pat, true
	}
	return truth, found
}

func openRFQ(t *testing.T) (*Document, map[int]string, refText) {
	t.Helper()
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(pdfDir, name))
		if err != nil {
			t.Skip("private corpus not present")
		}
		return b
	}
	var raw map[string]string
	if err := json.Unmarshal(read("outlined_rfq.labels.json"), &raw); err != nil {
		t.Fatal(err)
	}
	labels := map[int]string{}
	for k, v := range raw {
		id, err := strconv.Atoi(k)
		if err != nil {
			t.Fatal(err)
		}
		labels[id] = v
	}
	ref := newRefText(string(read("outlined_rfq.reference.txt")))
	return openTestPDF(t, "outlined_rfq.pdf"), labels, ref
}

func fixedLabeller(labels map[int]string) GlyphLabeler {
	return func(context.Context, []GlyphShape) (map[int]string, error) {
		return labels, nil
	}
}

func TestIntegration_RecoverOutlinedRFQ(t *testing.T) {
	doc, labels, ref := openRFQ(t)
	a, report, err := doc.recoverOutlines(context.Background(), fixedLabeller(labels))
	if err != nil {
		t.Fatal(err)
	}

	placed := 0
	for _, r := range a.runs {
		placed += len(r.glyphs)
	}
	if report.Glyphs != 3703 || report.Shapes != len(labels) || report.Rejected != 0 || len(report.Unlabeled) != 0 {
		t.Errorf("report: %d glyphs in %d shapes, %d rejected, %d unlabelled shapes; want 3703 in %d, none rejected or unlabelled",
			report.Glyphs, report.Shapes, report.Rejected, len(report.Unlabeled), len(labels))
	}
	if placed+len(report.Omitted) != report.Glyphs {
		t.Errorf("%d placed + %d omitted != %d glyphs", placed, len(report.Omitted), report.Glyphs)
	}
	if report.SpacingFallback {
		t.Error("spacing fell back to local gaps; E2 found a clear valley")
	}

	// E1: reference-matched-run coverage.
	matched := 0
	var unmatched []string
	for _, r := range a.runs {
		if s := runText(r); len(ref.wordMatches(s)) > 0 {
			matched += len(r.glyphs)
		} else {
			unmatched = append(unmatched, s)
		}
	}
	t.Logf("coverage %.1f%% of %d placed; fallback %d, omitted %d, guessed %d",
		100*float64(matched)/float64(placed), placed, len(report.FallbackPlaced), len(report.Omitted), len(report.Guessed))
	// Each unmatched run is explained: two cells 1.3 em apart merged into one
	// run (the unresolved cell boundary), a word the page breaks across lines,
	// and a reference spelling error.
	explained := []string{"Breastfeedi331/01", "ng/lnfant", "GlosswhitefinishwithSmartGuardanti-microbialHydrophylicglaze"}
	if len(report.Omitted) != 0 || !reflect.DeepEqual(unmatched, explained) {
		t.Errorf("%d omitted, unmatched runs %q; want none omitted, and only %q", len(report.Omitted), unmatched, explained)
	}

	scored, breaks, wholeDoc, heldOut := scoreBreaks(a, ref, doc.NumPages())
	if scored < 3300 || wholeDoc != 0 || heldOut != 0 {
		t.Errorf("word breaks: %d scored gaps (%d breaks), %d errors document-wide, %d held out; want at least 3300, no errors",
			scored, breaks, wholeDoc, heldOut)
	}
}

// scoreBreaks counts word-break errors on the gaps of runs the reference
// settles: with the document-wide fit that production uses, and with each page
// held out of the fit that decides it.
func scoreBreaks(a assembly, ref refText, pages int) (scored, breaks, wholeDoc, heldOut int) {
	type gap struct {
		page      int
		g         wordGap
		truth, at bool
	}
	var gaps []gap
	for _, r := range a.runs {
		truth, ok := ref.runTruth(r)
		if !ok {
			continue
		}
		for j, g := range r.gaps() {
			gaps = append(gaps, gap{r.glyphs[0].page, g, truth[j], r.breaks[j]})
		}
	}
	for _, g := range gaps {
		if g.truth {
			breaks++
		}
		if g.at != g.truth {
			wholeDoc++
		}
	}
	for hold := range pages {
		var train []wordGap
		for _, r := range a.runs {
			if r.glyphs[0].page != hold {
				train = append(train, r.gaps()...)
			}
		}
		sp := fitSpacing(train)
		for _, g := range gaps {
			if g.page == hold && (!sp.clear || sp.isBreak(g.g) != g.truth) {
				heldOut++
			}
		}
	}
	return len(gaps), breaks, wholeDoc, heldOut
}
