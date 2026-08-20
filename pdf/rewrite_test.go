package pdf

import (
	"bytes"
	"strings"
	"testing"
)

// Rewrite has no caller inside this module — gopdf-xfa consumes it — so nothing
// here exercised it before these tests. That is precisely why it needs them: a
// change made for an in-tree caller's benefit would otherwise break an external
// module silently, and only at its next dependency bump.

// contentRef returns the object number of a page's content stream.
func contentRef(t *testing.T, r *Reader, page int) int {
	t.Helper()
	pages, err := r.Pages()
	if err != nil {
		t.Fatalf("pages: %v", err)
	}
	ref, ok := pages[page].Ref("Contents")
	if !ok {
		t.Fatalf("page %d has no indirect /Contents", page)
	}
	return ref.Num
}

// substituteText rewrites a page's content stream with old replaced by new,
// editing the decoded bytes rather than authoring content-stream syntax.
func substituteText(t *testing.T, r *Reader, page int, old, new string) []byte {
	t.Helper()
	pages, err := r.Pages()
	if err != nil {
		t.Fatalf("pages: %v", err)
	}
	content, err := r.PageContent(pages[page])
	if err != nil {
		t.Fatalf("page content: %v", err)
	}
	if !bytes.Contains(content, []byte(old)) {
		t.Fatalf("page %d content does not contain %q", page, old)
	}
	edited := bytes.Replace(content, []byte(old), []byte(new), 1)

	out, err := r.Rewrite(map[int][]byte{contentRef(t, r, page): edited})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	return out
}

func pageText(t *testing.T, data []byte, page int) string {
	t.Helper()
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	text, err := doc.Page(page).Text()
	if err != nil {
		t.Fatalf("extract page %d: %v", page, err)
	}
	return text
}

func TestRewriteReplacesStreamContent(t *testing.T) {
	r, err := Open(testPDF(t, "original text"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	got := pageText(t, substituteText(t, r, 0, "original text", "replaced text"), 0)
	if !strings.Contains(got, "replaced text") {
		t.Errorf("page text = %q, want it to contain %q", got, "replaced text")
	}
	// The substitution has to remove the original, not merely append. This is
	// the whole point for a redaction-style caller.
	if strings.Contains(got, "original text") {
		t.Errorf("page text = %q, still contains the replaced string", got)
	}
}

// A substitution names one object; every other page must survive untouched.
func TestRewriteLeavesOtherPagesIntact(t *testing.T) {
	r, err := Open(testMultiPagePDF(t, "page one", "page two", "page three"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	out := substituteText(t, r, 1, "page two", "page TWO")

	doc, err := OpenBytes(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := doc.NumPages(); got != 3 {
		t.Fatalf("NumPages() = %d, want 3", got)
	}
	for i, want := range []string{"page one", "page TWO", "page three"} {
		if got := pageText(t, out, i); !strings.Contains(got, want) {
			t.Errorf("page %d = %q, want it to contain %q", i, got, want)
		}
	}
}

// /ID is a two-element array: [0] is set once at creation and never changes,
// [1] changes on every update (ISO 32000-1 §14.4). Acrobat compares the first
// half, so preserving it is what stops the output being flagged as a different
// document; regenerating the second is what honestly records that it changed.
func TestRewritePreservesCreationIDAndRenewsModificationID(t *testing.T) {
	r, err := Open(testPDF(t, "original text"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	want := r.OriginalID()
	if len(want) != 2 {
		t.Fatalf("source /ID has %d elements, want 2", len(want))
	}

	after, err := Open(substituteText(t, r, 0, "original text", "replaced text"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got := after.OriginalID()
	if len(got) != 2 {
		t.Fatalf("rewritten /ID has %d elements, want 2", len(got))
	}

	if got[0] != want[0] {
		t.Errorf("/ID[0] = %q, want the creation ID preserved (%q)", got[0], want[0])
	}
	if got[1] == want[1] {
		t.Error("/ID[1] is unchanged; a rewritten document should carry a new modification ID")
	}
}

// Documented contract: "Non-stream objects in the map are ignored." A caller
// that mis-identifies an object number gets an unchanged document rather than a
// corrupt one.
func TestRewriteIgnoresNonStreamObjects(t *testing.T) {
	data := testPDF(t, "original text")
	r, err := Open(data)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	rootRef, ok := r.Trailer().Ref("Root")
	if !ok {
		t.Fatal("no /Root")
	}

	out, err := r.Rewrite(map[int][]byte{rootRef.Num: []byte("not a stream")})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if got := pageText(t, out, 0); !strings.Contains(got, "original text") {
		t.Errorf("page text = %q, want the document unchanged", got)
	}
}

// Rewrite clones the whole document rather than the bounded subgraph merge
// walks, so the page tree has to come back intact — /Parent chains included,
// which merge deliberately prunes.
func TestRewritePreservesPageTree(t *testing.T) {
	r, err := Open(testMultiPagePDF(t, "page one", "page two"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	after, err := Open(substituteText(t, r, 0, "page one", "page ONE"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	pages, err := after.Pages()
	if err != nil {
		t.Fatalf("pages: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(pages))
	}
	for i, p := range pages {
		if _, ok := p.Ref("Parent"); !ok {
			t.Errorf("page %d lost its /Parent link", i)
		}
	}
}
