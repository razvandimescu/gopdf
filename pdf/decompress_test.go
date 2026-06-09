package pdf

import (
	"bytes"
	"compress/zlib"
	"testing"
)

// Some PDF producers terminate a deflate stream with a sync-flush (00 00 FF FF)
// and omit the final block + Adler-32 checksum. decompress must return the
// bytes decoded before the truncation rather than discarding the whole stream
// (which previously failed every such document with "unexpected EOF").
func TestDecompressSyncFlushTruncation(t *testing.T) {
	want := bytes.Repeat([]byte("xref-entry"), 64)

	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(want); err != nil {
		t.Fatal(err)
	}
	if err := zw.Flush(); err != nil { // sync-flush; no Close => no final block/checksum
		t.Fatal(err)
	}

	got, err := decompress(buf.Bytes())
	if err != nil {
		t.Fatalf("decompress returned error on sync-flush stream: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("recovered %d bytes, want %d", len(got), len(want))
	}
}

// A stream truncated before any block decodes (here: a valid zlib header and
// nothing else) is real corruption, not a recoverable missing tail. decompress
// must surface the error rather than return an empty stream as if it were valid.
func TestDecompressTruncatedBeforeAnyBlock(t *testing.T) {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Flush(); err != nil {
		t.Fatal(err)
	}

	headerOnly := buf.Bytes()[:2] // zlib header, no deflate blocks
	got, err := decompress(headerOnly)
	if err == nil {
		t.Fatalf("expected error on header-only stream, got %d bytes", len(got))
	}
}
