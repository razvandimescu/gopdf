package pdf

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// Integration tests — real PDFs from example_out/ (git-ignored).
//
// Everything in redact_test.go works on documents this library wrote itself,
// which makes them agreeable: one show operator per line, a standard font, a
// single content stream. Real producers emit none of that. They split a word
// across kerned fragments, embed subset fonts, address glyphs by two-byte
// codes and spread a page over several streams — and removal has to find the
// text through all of it.
//
// The files are not in the repository, so these skip when they are absent.

// realPDFs returns every PDF in the test corpus. The corpus is scanned rather
// than named: the files are customer documents and their names do not belong
// in the source.
func realPDFs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(pdfDir)
	if err != nil {
		t.Skipf("no test corpus at %s", pdfDir)
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".pdf") {
			paths = append(paths, filepath.Join(pdfDir, e.Name()))
		}
	}
	if len(paths) == 0 {
		t.Skipf("no PDFs in %s", pdfDir)
	}
	return paths
}

// words counts every alphanumeric token. Removal leaves a gap where the text
// was, and line reconstruction fills gaps with spaces, so the length of the
// page text says little; what each word does is the thing to hold onto.
func words(text string) map[string]int {
	counts := make(map[string]int)
	for _, w := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		counts[w]++
	}
	return counts
}

// longestWord picks something worth redacting: the longest alphanumeric run on
// the page, which in these documents is a name, a code or an account number.
func longestWord(text string) string {
	longest := ""
	for word := range words(text) {
		if len(word) > len(longest) {
			longest = word
		}
	}
	if len(longest) < 6 {
		return ""
	}
	return longest
}

func TestIntegration_RemoveTextFromRealPDFs(t *testing.T) {
	for _, path := range realPDFs(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := OpenBytes(data)
			if err != nil {
				t.Fatalf("opening: %v", err)
			}
			before, err := doc.Text()
			if err != nil {
				t.Fatalf("extracting: %v", err)
			}
			query := longestWord(before)
			if query == "" {
				t.Skip("nothing distinctive enough to redact")
			}

			editor := NewEditor(data)
			editor.RemoveText(query)
			out, err := editor.Apply()
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			result, err := OpenBytes(out)
			if err != nil {
				t.Fatalf("reopening the result: %v", err)
			}

			if result.NumPages() != doc.NumPages() {
				t.Errorf("page count %d became %d", doc.NumPages(), result.NumPages())
			}
			after, err := result.Text()
			if err != nil {
				t.Fatalf("extracting the result: %v", err)
			}
			if strings.Contains(after, query) {
				t.Error("the text survived removal")
			}
			if hits := result.Search(query); len(hits) != 0 {
				t.Errorf("search still finds %d occurrences", len(hits))
			}

			// Not one word but the redacted one may change.
			was, now := words(before), words(after)
			for word, count := range was {
				if word != query && now[word] != count {
					t.Errorf("%q appeared %d times and now appears %d", word, count, now[word])
					break
				}
			}

			// Nothing reflows: every piece of text in the output sits within
			// the run of text that was there before, on the same baseline.
			// Removal may split a span or take one away entirely, but no glyph
			// may land anywhere the original did not already cover.
			for page := 0; page < result.NumPages(); page++ {
				original, _ := doc.Page(page).TextSpans()
				for _, span := range mustSpans(t, result.Page(page)) {
					if !coveredBy(span, original) {
						t.Errorf("page %d: %q sits at x=%.2f y=%.2f, where no text was",
							page, span.Text, span.X, span.Y)
						break
					}
				}
			}
		})
	}
}

func mustSpans(t *testing.T, page *Page) []TextSpan {
	t.Helper()
	spans, err := page.TextSpans()
	if err != nil {
		t.Fatalf("spans: %v", err)
	}
	return spans
}

func coveredBy(span TextSpan, original []TextSpan) bool {
	const tolerance = 0.05
	for _, was := range original {
		if was.Y-tolerance <= span.Y && span.Y <= was.Y+tolerance &&
			was.X-tolerance <= span.X && span.X <= was.EndX+tolerance {
			return true
		}
	}
	return false
}

// TestIntegration_RedactedPDFsReadCleanToOtherParsers is the only check here
// that is not circular. Every other test reads the output back with the same
// code that wrote it, which proves the two agree and nothing more: a stream
// this library mangles in a way its own lexer forgives would still look
// redacted to it. MuPDF and Poppler share no code with this library or with
// each other, so text they cannot find is text that is genuinely gone.
func TestIntegration_RedactedPDFsReadCleanToOtherParsers(t *testing.T) {
	readers := []struct {
		name string
		args func(path string) []string
	}{
		{"mutool", func(p string) []string { return []string{"mutool", "draw", "-F", "txt", "-o", "-", p} }},
		{"pdftotext", func(p string) []string { return []string{"pdftotext", p, "-"} }},
	}
	var available []int
	for i, r := range readers {
		if _, err := exec.LookPath(r.name); err == nil {
			available = append(available, i)
		}
	}
	if len(available) == 0 {
		t.Skip("neither mutool nor pdftotext is installed")
	}

	dir := t.TempDir()
	for _, path := range realPDFs(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := OpenBytes(data)
			if err != nil {
				t.Fatalf("opening: %v", err)
			}
			text, err := doc.Text()
			if err != nil {
				t.Fatalf("extracting: %v", err)
			}
			query := longestWord(text)
			if query == "" {
				t.Skip("nothing distinctive enough to redact")
			}

			editor := NewEditor(data)
			editor.RemoveText(query)
			out, err := editor.Apply()
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			redacted := filepath.Join(dir, "redacted.pdf")
			if err := os.WriteFile(redacted, out, 0o600); err != nil {
				t.Fatal(err)
			}

			for _, i := range available {
				reader := readers[i]
				// The control comes first. Without it a reader that fails to
				// open the file at all, or that never saw the text to begin
				// with, would report a clean redaction.
				beforeHits := bytes.Count(runReader(t, reader.args(path)), []byte(query))
				if beforeHits == 0 {
					t.Logf("%s does not read %q in the original; nothing to prove here", reader.name, query)
					continue
				}
				if afterHits := bytes.Count(runReader(t, reader.args(redacted)), []byte(query)); afterHits != 0 {
					t.Errorf("%s still reads %d of %d occurrences after removal",
						reader.name, afterHits, beforeHits)
				}
			}
		})
	}
}

func runReader(t *testing.T, args []string) []byte {
	t.Helper()
	out, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		t.Fatalf("%s: %v", args[0], err)
	}
	return out
}
