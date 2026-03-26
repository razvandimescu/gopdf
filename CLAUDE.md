# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Test

```bash
go build ./...              # build everything
go build -o gopdf .         # build CLI binary
go test ./...               # run all tests
go test -v -run TestSupplierCodes ./...  # run a single test
go test -count=1 ./...      # skip test cache
go vet ./...                # static analysis
```

Tests require PDF files in `example_out/` (git-ignored). Tests skip gracefully if PDFs are missing.

To regenerate the Adobe Glyph List: download `glyphlist.txt` to `/tmp/`, then `go run cmd/genglyphlist/main.go`.

## Architecture

Two-layer design: generic PDF library (`pdf/`) and domain-specific quotation extractor (root package).

### PDF Library (`pdf/`)

Pipeline: **Lexer** (bytes→tokens) → **Parser** (tokens→objects) → **Reader** (xref/streams/object resolution) → **Text extraction** (content stream operators→positioned spans) → **Line reconstruction** (spatial grouping).

- `document.go` — Public API: `Document`/`Page` types wrapping the internals
- `reader.go` — PDF structure: xref tables/streams, object resolution with caching, stream decompression (FlateDecode/LZW/ASCII85/ASCIIHex), filter chains, PNG predictors, compressed object streams (ObjStm), font/CMap/encoding helpers, resource inheritance
- `text.go` — Content stream interpretation: all text operators (BT/ET/Tf/Tm/Td/TJ/Tj/T\*/'/"), graphics state stack (q/Q), CTM tracking (cm), Form XObject recursion (Do), MarkedContent/ActualText (BMC/BDC/EMC), CIDFont 2-byte handling, page rotation
- `writer.go` — PDF serializer: object writing, FlateDecode compression, xref table generation
- `merge.go` — PDF merge: deep object graph copy with Ref remapping, page tree construction, `MergeFiles`/`MergeBytes`/`Merger` API
- `glyphlist.go` — Generated: 4200-entry Adobe Glyph List (glyph name→rune)
- `stdfonts.go` — Width tables for standard 14 fonts (Courier, Helvetica, Times)

Key design decisions:
- Affine transforms use `[6]float64` arrays with `matMul6()` for composition
- Object resolution is cached in `Reader.cache` to avoid re-parsing
- Compressed objects (ObjStm) and xref streams (PDF 1.5+) are fully supported
- Resource inheritance propagates `Resources`/`MediaBox`/`CropBox`/`Rotate` down the page tree during `collectPages`
- Font encoding chain: ToUnicode CMap → Encoding Differences → WinAnsi/MacRoman fallback

### Quotation Extractor (root package)

`extract.go` parses structured data from quotation PDFs: company, quote name, ref, table headers, line items, supplier codes. Uses column detection by finding the "Suppliers Code" header span's X position, then classifies data spans by nearest column. Handles multi-page tables, multi-line wrapped headers, and continuation rows (codes split across lines like `PRIMABOX4/B-MF-C/P`).

`extract_test.go` validates against 10 real PDFs with 40 assertions covering supplier codes (exact set match + order), header fields, and table headers.

## Constraints

- Pure Go only — no CGo, no external C libraries
- `github.com/ledongthuc/pdf` is only in `cmd/compare` (benchmark utility), not in the core library
- PDF files are git-ignored; test PDFs live in `example_out/`
- Do not reference customer/client names in commit messages or public-facing text
- Encryption support is not yet implemented (Phase 5, deferred)
