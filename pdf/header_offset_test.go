package pdf

import (
	"bytes"
	"strings"
	"testing"
)

// factSheetEnvelope mimics the cache/transport record observed prepended to
// "FactSheetExtended" exports: a 6-byte per-file hash, a 2-byte magic, then
// uint8-length-prefixed content-type and filename, before the real %PDF bytes.
func factSheetEnvelope(payload []byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x72, 0xf6, 0xe8, 0xff, 0xe1, 0xd6}) // per-file hash
	b.Write([]byte{0xde, 0x08})                         // magic
	const ct, fn = "application/pdf", "FactSheetExtended.pdf"
	b.WriteByte(byte(len(ct)))
	b.WriteString(ct)
	b.WriteByte(byte(len(fn)))
	b.WriteString(fn)
	b.Write(payload)
	return b.Bytes()
}

// TestLeadingBytesBeforeHeader ensures a PDF prepended with non-%PDF bytes
// still parses: every stored byte offset is shifted, so the reader must treat
// the "%PDF-" marker as the origin (matching Adobe/pdf.js) rather than byte 0.
func TestLeadingBytesBeforeHeader(t *testing.T) {
	clean := testPDF(t, "Hello World", "Second line")
	if !bytes.HasPrefix(clean, []byte("%PDF-")) {
		t.Fatalf("test PDF does not start with %%PDF-")
	}

	wrapped := factSheetEnvelope(clean)
	if got := headerOffset(wrapped); got != 46 {
		t.Fatalf("headerOffset: got %d, want 46", got)
	}

	doc, err := OpenBytes(wrapped)
	if err != nil {
		t.Fatalf("opening wrapped PDF: %v", err)
	}
	if doc.NumPages() != 1 {
		t.Errorf("pages: got %d, want 1", doc.NumPages())
	}
	text, err := doc.Text()
	if err != nil {
		t.Fatalf("extracting text: %v", err)
	}
	for _, want := range []string{"Hello World", "Second line"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q, got: %s", want, text)
		}
	}
}

// TestHeaderOffsetCleanFile keeps a normal PDF at zero displacement so the
// recovery path can never perturb well-formed input.
func TestHeaderOffsetCleanFile(t *testing.T) {
	clean := testPDF(t, "No envelope here")
	if got := headerOffset(clean); got != 0 {
		t.Fatalf("headerOffset on clean file: got %d, want 0", got)
	}
}
