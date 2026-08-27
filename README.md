# gopdf

[![CI](https://github.com/razvandimescu/gopdf/actions/workflows/ci.yml/badge.svg)](https://github.com/razvandimescu/gopdf/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/razvandimescu/gopdf/pdf.svg)](https://pkg.go.dev/github.com/razvandimescu/gopdf/pdf)
[![Go Report Card](https://goreportcard.com/badge/github.com/razvandimescu/gopdf)](https://goreportcard.com/report/github.com/razvandimescu/gopdf)
[![codecov](https://codecov.io/gh/razvandimescu/gopdf/branch/main/graph/badge.svg)](https://codecov.io/gh/razvandimescu/gopdf)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Pure Go table extraction from PDFs — plus positioned text, search, creation, merge, and editing. No CGo, no AGPL, no dependencies outside the standard library.

<p align="center">
  <img src="assets/sample-invoice.png" width="380" alt="Sample Invoice" />
  <img src="assets/sample-report.png" width="380" alt="Sample Report" />
</p>
<p align="center"><em>Generated entirely from Go code — <code>go run ./cmd/sample/</code></em></p>

Getting structured rows and columns out of an invoice, bank statement, or report normally means shelling out to `pdftotext -layout` and regexing the columns back together, or leaving Go for Python's camelot/tabula. `gopdf` does it natively: tables are located by header anchors or auto-detected from column gaps, with multi-line cell merging and multi-page continuation.

```go
doc, _ := pdf.OpenFile("statement.pdf")
tables, _ := doc.Page(0).Tables()
fmt.Println(tables[0].CellByName(0, "Amount")) // "1,204.50"
```

## Try it

```bash
go install github.com/razvandimescu/gopdf/cmd/gopdf@latest
gopdf tables statement.pdf -format csv
```

```csv
Date,Description,Debit,Credit
01-04-2025,Card payment,"94,19","0,00"
02-04-2025,Transfer received,"0,00","1.000,00"
```

One static binary, no toolchain, nothing to install alongside it. The same
command handles multi-line cells and tables that continue across pages. See
[the CLI section](#command-line) for `gopdf merge` and `gopdf watermark`.

## Why gopdf?

`go.mod` has no `require` block — static binaries, `FROM scratch` containers,
and cross-compilation without a C toolchain. Of the libraries below it is the
only one that pairs that with table extraction, under a permissive licence.

[unipdf](https://github.com/unidoc/unipdf) extracts tables too, but is AGPL or commercial. [ledongthuc/pdf](https://github.com/ledongthuc/pdf) gives you positioned text and groups it into rows and columns, but has no header anchoring, multi-line cell merging, or multi-page continuation.

| | gopdf | unipdf | pdfcpu | ledongthuc/pdf | MuPDF bindings |
|---|---|---|---|---|---|
| **License** | MIT | AGPL / Commercial | Apache-2.0 | BSD-3 | AGPL |
| **CGo required** | No | No | No | No | Yes |
| **Text extraction** | Positioned (X/Y) | Positioned | Raw streams | Positioned (X/Y) | Positioned |
| **Table detection** | Yes | Yes | No | Row/column grouping | No |
| **Text search** | With rects | Yes | No | No | Yes |
| **PDF merge** | Yes (size-constrained) | Yes | Yes | No | No |
| **Text overlay** | Yes | Yes | Watermark | No | No |
| **Visual redaction** | Yes | Yes | No | No | No |
| **Text removal (true redaction)** | Yes | Yes | No | No | No |
| **PDF creation** | Yes | Yes | Yes | No | Yes |
| **Reads encrypted PDFs** | Yes | Yes | Yes | No | Yes |
| **Dependencies** | 0 | Many | 10 | 0 | System lib |

### When to use something else

- **[pdfcpu](https://github.com/pdfcpu/pdfcpu)** — for document operations as your primary need: encryption, form filling, optimization, page manipulation, or a batch CLI. It is more mature and more broadly tested than gopdf on all of them.
- **[unipdf](https://github.com/unidoc/unipdf)** — for breadth and commercial support, if AGPL or a paid licence works for you.
- **[go-pdf/fpdf](https://github.com/go-pdf/fpdf)** — for generating documents with embedded fonts and rich layout. gopdf's creator covers the standard 14 fonts only.
- **MuPDF bindings** — for rendering fidelity, if CGo and AGPL are acceptable.

## Features

- Table detection with column/row extraction (explicit headers or auto-detection)
- Multi-line cell merging for tables with wrapped content (bank statements, invoices)
- Multi-page table support with automatic header re-detection
- Text extraction with X/Y coordinates, font name, and font size
- Line reconstruction with gap-derived word spacing
- Text search returning bounding rectangles
- PDF merge with page selection and size constraints (fail/truncate/shrink with JPEG recompression)
- Text overlay (Helvetica, configurable size and color)
- Visual redaction (filled rectangles with configurable color)
- Text removal: glyphs deleted from the content stream, not covered over
- Reads encrypted PDFs (RC4 40/128-bit, AES-128, AES-256; user or owner password)
- Image overlay / watermark (PNG/JPEG/GIF, rotation, opacity, transparent SMask)
- PDF creation with text, rectangles, lines, images, and multiple fonts
- Images as pages: PDFs and PNG/JPEG/GIF mixed into one document, detected by content, with baseline JPEGs embedded unre-encoded and EXIF orientation honoured
- One CLI — `gopdf tables`, `gopdf merge`, `gopdf watermark`
- Pure Go — no CGo, no system dependencies

## Installation

The CLI, as a single binary:

```bash
go install github.com/razvandimescu/gopdf/cmd/gopdf@latest
```

The library:

```bash
go get github.com/razvandimescu/gopdf@latest
```

`go.mod` has no `require` block, so neither one pulls anything in.

## Quick Start

```go
package main

import (
    "fmt"
    "log"

    "github.com/razvandimescu/gopdf/pdf"
)

func main() {
    doc, err := pdf.OpenFile("document.pdf")
    if err != nil {
        log.Fatal(err)
    }
    text, err := doc.Text()
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(text)
}
```

## Examples

### Positioned text lines

```go
doc, err := pdf.OpenFile("document.pdf")
if err != nil {
    log.Fatal(err)
}
for i := 0; i < doc.NumPages(); i++ {
    lines, _ := doc.Page(i).TextLines()
    for _, line := range lines {
        fmt.Printf("Y=%.0f: %s\n", line.Y, line.Text)
    }
}
// Output:
// Y=756: Quotation Ref: QT10001
// Y=732: Quote Name: Northgate Academy
// Y=710: Company: Nova Facilities
// ...
```

### Table extraction

```go
// Auto-detect tables (no header hints needed)
tables, _ := doc.Page(0).Tables()
for _, tbl := range tables {
    for _, col := range tbl.Columns {
        fmt.Printf("%-20s", col.Name)
    }
    fmt.Println()
    for _, row := range tbl.Rows {
        for _, cell := range row.Cells {
            fmt.Printf("%-20s", cell.Text)
        }
        fmt.Println()
    }
}
```

With explicit header anchors (more precise):

```go
spans, _ := doc.Page(0).TextSpans()
tbl := pdf.FindTable(spans, &pdf.TableOpts{
    Headers: []string{"Quantity", "Description"},
})
fmt.Println(tbl.CellByName(0, "Quantity"))    // "3"
fmt.Println(tbl.CellByName(0, "Description")) // "Widget Assembly"
```

Tables with multi-line cells (e.g., bank statements):

```go
tbl := pdf.FindTable(spans, &pdf.TableOpts{
    Headers:   []string{"Date", "Description", "Debit", "Credit"},
    MergeGap:  16,  // merge rows within 16pt into one logical row
    MaxRowGap: 30,  // stop table when gap exceeds 30pt (footer boundary)
})
```

Multi-page tables:

```go
var pages [][]pdf.TextSpan
for i := 0; i < doc.NumPages(); i++ {
    spans, _ := doc.Page(i).TextSpans()
    pages = append(pages, spans)
}
tbl := pdf.FindTableAcrossPages(pages, &pdf.TableOpts{
    Headers: []string{"Date", "Amount"},
})
// All rows across all pages in a single table
fmt.Printf("%d rows\n", len(tbl.Rows))
```

### Search for text

```go
results := doc.Search("Invoice Total")
for _, r := range results {
    fmt.Printf("Page %d at (%.0f, %.0f) size %.0fx%.0f\n",
        r.Page, r.Rect.X, r.Rect.Y, r.Rect.Width, r.Rect.Height)
}
// Output:
// Page 0 at (206, 691) size 70x12
```

### Encrypted PDFs

Files encrypted with an empty user password — the common case for emailed
statements — open through the ordinary entry points with no extra work:

```go
doc, err := pdf.OpenFile("statement.pdf")
```

When a password is genuinely required, `OpenFile` reports `pdf.ErrEncrypted` so
you can prompt for one rather than guessing at a parse failure:

```go
doc, err := pdf.OpenFile("statement.pdf")
if errors.Is(err, pdf.ErrEncrypted) {
    doc, err = pdf.OpenFile("statement.pdf", pdf.WithPassword(os.Getenv("PDF_PASSWORD")))
}
if errors.Is(err, pdf.ErrWrongPassword) {
    log.Fatal("wrong password")
}
```

Either the user or the owner password is accepted. Everything reached through
the opened document — text extraction, tables, search — behaves exactly as it
does for an unencrypted file.

`Merger` and `Editor` take their own entry points and do not yet accept a
password, so they handle encrypted input only when its user password is empty.

### Merge PDFs

```go
combined, err := pdf.MergeFiles("a.pdf", "b.pdf", "c.pdf")
if err != nil {
    log.Fatal(err)
}
os.WriteFile("merged.pdf", combined, 0644)
```

With page selection:

```go
m := pdf.NewMerger()
m.AddFile("big.pdf", 0, 2, 5) // pages 0, 2, 5 only
m.Add(otherPDFBytes)           // all pages
result, err := m.Merge()
```

### Size-constrained merge

Control output size with `MergeWithOptions`. Three oversize behaviors are available:

```go
m := pdf.NewMerger()
m.AddFile("a.pdf")
m.AddFile("b.pdf")
m.AddFile("c.pdf")

res, err := m.MergeWithOptions(pdf.MergeOptions{
    MaxSize:          20 * 1024 * 1024, // 20 MB
    OversizeBehavior: pdf.OversizeShrink,
})
if err != nil {
    var ose *pdf.OversizeError
    if errors.As(err, &ose) {
        log.Fatalf("can't fit: %d bytes after optimization (limit %d)", ose.Size, ose.MaxSize)
    }
    log.Fatal(err)
}
os.WriteFile("merged.pdf", res.Data, 0644)
fmt.Printf("%d/%d pages, %d bytes\n", res.IncludedPages, res.TotalPages, len(res.Data))
```

| Behavior | What it does |
|---|---|
| `OversizeFail` | Merges normally. Returns `*OversizeError` if output exceeds limit. |
| `OversizeTruncate` | Dedup + metadata strip + JPEG recompress. Includes as many pages as fit. |
| `OversizeShrink` | Two-pass: lossless optimization first, then JPEG recompression at ratio-derived quality to hit the target. Returns `*OversizeError` if still over. |

### Create a PDF from scratch

```go
c := pdf.NewCreator()
page := c.NewPage(595, 842) // A4

page.SetFont("Helvetica-Bold", 24)
page.DrawText(72, 750, "Invoice #12345")

page.SetFont("Helvetica", 12)
page.DrawText(72, 720, "Date: 2026-03-26")
page.DrawText(72, 704, "Total: $500.00")

page.FillRect(72, 690, 200, 1, 0, 0, 0) // separator line

data, err := c.Build()
os.WriteFile("invoice.pdf", data, 0644)
```

### Images as pages

`DrawImage` places a decoded PNG/JPEG by its lower-left corner, scaled to the
width and height you give it. `FitRotated` returns the largest such pair that
keeps the image's aspect ratio inside a page:

```go
img, err := pdf.LoadImage("scan.png")

c := pdf.NewCreator()
pageW, pageH := 595.0, 842.0 // A4
w, h := img.FitRotated(pageW-36, pageH-36, 0, 1) // fit inside an 18pt margin

page := c.NewPage(pageW, pageH)
page.DrawImage(img, (pageW-w)/2, (pageH-h)/2, w, h)

data, err := c.Build()
```

An image drawn on several pages is written to the file once. Transparency
travels with it as a soft mask.

A JPEG's EXIF orientation is honoured. Phone cameras record how the handset
was held rather than rotating the pixels, so a photo taken sideways is stored
landscape and declares a quarter turn; `DrawImage` folds that turn into the
placement matrix. It costs nothing — the pixels are never touched — and
`DisplaySize` reports the dimensions the image will actually occupy, which is
what you want when sizing a page around it. `Image.DPI` carries the resolution
the file declares — EXIF or JFIF for JPEG, the `pHYs` chunk for PNG — and is 0
when it declares none.

A baseline JPEG is embedded in its original encoding, behind `DCTDecode` —
decoding a photograph to RGB and deflating the pixels would multiply its size
several-fold. Progressive, 12-bit, and CMYK JPEGs take the decode path
instead, since viewer support for them behind `DCTDecode` is uneven.

### Command line

One binary, `gopdf`, with a subcommand per capability. Installed with
`go install github.com/razvandimescu/gopdf/cmd/gopdf@latest`:

```bash
gopdf tables invoice.pdf -format csv        # extract a table
gopdf merge report.pdf scan.png -o out.pdf  # combine PDFs and images
gopdf watermark -img logo.png in.pdf -o out.pdf
```

#### gopdf tables

Detects a table and prints its rows as aligned text or CSV. Columns come from
their headers when `-headers` is given, and from column geometry otherwise.
When no table is found it says so on stderr and exits non-zero, rather than
printing whatever the geometry happened to line up.

```bash
gopdf tables statement.pdf -format csv -o rows.csv
gopdf tables invoice.pdf -headers "Quantity,Product Code,Description"
gopdf tables statement.pdf -anchor Date -filter "Page ,Continued"
gopdf tables statement.pdf -format csv -delimiter ';'   # comma-decimal locales
```

| Flag | Default | Meaning |
|---|---|---|
| `-format` | `text` | `text` or `csv` |
| `-delimiter` | `,` | field delimiter for `-format csv`; `\t` gives TSV |
| `-o` | stdout | output path |
| `-headers` | — | comma-separated header anchors |
| `-anchor` | — | column that signals a new row; rows where it is empty merge upwards |
| `-filter` | — | substrings whose rows are dropped |
| `-require` | — | columns of which at least one must be filled |
| `-merge-gap`, `-max-row-gap` | auto | row grouping distances; auto-tuned when unset |
| `-col-width` | `30` | maximum column width, for `-format text` |
| `-password` | — | password for an encrypted PDF |

Where the decimal separator is a comma, `-delimiter ';'` is the difference
between a file a spreadsheet opens correctly and one it does not: every
`1.000,00` in the default output has to be quoted to survive the comma.

#### gopdf merge

Merges PDFs and images into one PDF. Each input is classified by
its bytes, not its extension: a PDF header (tolerating prepended transport
bytes, as the reader does) means PDF, anything else is decoded as an image.
Images become one page each, in the order given.

```bash
gopdf merge -o out.pdf scan.png photo.jpg     # images only
gopdf merge -o out.pdf report.pdf scan.png    # append a scan
gopdf merge -page image -o out.pdf scan.png   # page follows the image
```

| Flag | Default | Meaning |
|---|---|---|
| `-o` | stdout | output PDF path |
| `-page` | `a4` | page size for image inputs: `a4`, `letter`, or `image` |
| `-dpi` | `0` | image resolution; `0` reads it from the file, falling back to 72 |
| `-margin` | `0` | whitespace around an image, in points |

Image pages default to a paper size rather than to the image, which is the
opposite of what a converter like `mutool convert` or ImageMagick does. The
reason is the verb: `merge` promises one document, and pages of a document
have to agree with each other — `merge report.pdf photo.jpg` with page-follows-
image would bind a 42×56 inch page next to an A4 one, because a phone camera
declares 72 dpi when it has no real size to report. `-page image` is right when
the resolution means something, as it does for a 300 dpi scan, and it now
honours what the file declares instead of assuming 72.

Fitting an image to a page already letterboxes it on one axis, so `-margin`
defaults to 0; pass `-margin 18` for printers that cannot reach the edge.

The summary goes to stderr, leaving stdout for the PDF, and names both the
page size chosen and the way out of it:

```
2 inputs, 2 pages → out.pdf (7.1 MiB)
  2 images fitted to A4 595×842pt; -page image sizes each page to its image
```

PNG, JPEG and GIF are the formats Go's standard library decodes, so they are
the formats gopdf reads. Hand it a HEIC, AVIF, WebP or TIFF and the error
names the format and the command that fixes it:

```
$ gopdf merge IMG_6407.HEIC -o out.pdf
gopdf merge: IMG_6407.HEIC: HEIC is not supported (PNG, JPEG and GIF only)
  convert it first:  sips -s format jpeg IMG_6407.HEIC --out IMG_6407.jpg
```

`sips` ships with macOS; elsewhere the hint names ImageMagick. Library callers
can match `*pdf.UnsupportedFormatError` with `errors.As` to react to the format
themselves.

#### gopdf watermark

Stamps an image diagonally across every page, centred and scaled to the page it
lands on — including pages rotated 90° or 270°, which are measured as the reader
sees them rather than as they are stored.

```bash
gopdf watermark -img logo.png in.pdf -o out.pdf
gopdf watermark -img draft.png in.pdf -o out.pdf -angle 30 -opacity 0.12 -skip-first
```

| Flag | Default | Meaning |
|---|---|---|
| `-img` | — | watermark image; PNG, JPEG or GIF (required) |
| `-o` | stdout | output PDF path |
| `-angle` | `45` | rotation in degrees, counter-clockwise |
| `-opacity` | `0.15` | opacity, in [0, 1] |
| `-scale` | `0.85` | size as a fraction of the page |
| `-skip-first` | off | leave the first page un-watermarked |
| `-skip-last` | off | leave the last page un-watermarked |

The image is written to the file once and shared by every page that references
it, so a hundred-page watermark costs one copy.

### Text overlay

```go
ed := pdf.NewEditor(data)
ed.AddText(pdf.TextOverlay{
    Page: 0, X: 100, Y: 50,
    Text: "APPROVED", FontSize: 24,
    R: 0, G: 0.5, B: 0, // green
})
result, err := ed.Apply()
```

### Redaction

Two separate operations, because they answer different needs. `RedactText`
draws a box over the text and leaves it in the file. `RemoveText` deletes the
glyphs from the content stream, so extraction, search and copy/paste have
nothing left to find:

```go
ed := pdf.NewEditor(data)
ed.RemoveText("Confidential")          // the glyphs are gone from the output
ed.RedactText("Confidential", 0, 0, 0) // and a black box marks where they were
result, err := ed.Apply()
```

Surviving text does not reflow: each removed run leaves behind a kerning number
worth exactly the advance it had. `RemoveRegion(page, rect)` does the same for
an area rather than a string.

What removal reaches, and what it leaves alone:

| Location | Removed |
|---|---|
| Page content stream glyphs | Yes |
| Text inside Form XObjects | Yes |
| Image XObjects | No |
| Annotation text and appearance streams | No |
| AcroForm / XFA field values | No |
| Document information dictionary | No |
| XMP metadata | No |
| Embedded files | No |

Text is deleted from the page content stream. That is a narrower claim than
"secure redaction", and deliberately so: a document can carry the same string
in half a dozen other places, and which of them matter is yours to judge.

The claim is checked against real documents, not only ones this library wrote:
the test suite redacts a corpus of PDFs from assorted producers and reads the
output back with [MuPDF](https://mupdf.com) and [Poppler](https://poppler.freedesktop.org),
which share no code with gopdf or with each other. Reading it back with gopdf
alone would only show that its writer and its reader agree.

Combine redaction and overlay to replace text:

```go
ed := pdf.NewEditor(data)
ed.RedactText("OLD-REF", 1, 1, 1)       // white box over old text
ed.AddText(pdf.TextOverlay{              // write new text
    Page: 0, X: 100, Y: 750,
    Text: "NEW-REF", FontSize: 12,
})
result, err := ed.Apply()
```

### Image watermark

```go
logo, _ := pdf.LoadImage("logo.png")     // PNG or JPEG; alpha becomes an SMask

ed := pdf.NewEditor(data)
for i := 0; i < doc.NumPages(); i++ {
    mb := doc.Page(i).MediaBox()
    pageW, pageH := mb[2]-mb[0], mb[3]-mb[1]
    // Size so the rotated watermark fills 85% of the page at any angle.
    w, h := logo.FitRotated(pageW, pageH, 45, 0.85)
    ed.AddImage(pdf.ImageOverlay{
        Page:     i,
        Image:    logo,
        CX:       mb[0] + pageW/2,        // page center
        CY:       mb[1] + pageH/2,
        Width:    w, Height: h,
        Rotation: 45,                     // diagonal
        Opacity:  0.15,
    })
}
result, err := ed.Apply()
```

The same image is written once and shared by every page that references it.
[`gopdf watermark`](#gopdf-watermark) wraps this for one-shot usage.

## API Reference

Full reference on [pkg.go.dev](https://pkg.go.dev/github.com/razvandimescu/gopdf/pdf) — every exported symbol is documented there.

### Types

```go
type TextSpan struct {
    X, Y     float64 // position on page
    EndX     float64 // X position after this span
    FontSize float64
    Font     string
    Text     string
}

type TextLine struct {
    Y     float64
    Spans []TextSpan
    Text  string // reconstructed line text with spacing
}

type Table struct {
    Columns []Column
    Rows    []Row
}

type Column struct {
    Name string
    X    float64
}

type Row struct {
    Y     float64
    Cells []Cell
}

type Cell struct {
    Column int // index into Table.Columns
    Text   string
    Spans  []TextSpan
}

type SearchResult struct {
    Page     int
    Text     string
    Rect     Rect
    FontSize float64
}

type Rect struct {
    X, Y, Width, Height float64
}
```

## Supported PDF Features

| Category | Details |
|---|---|
| **PDF versions** | 1.0–1.7, including xref streams (1.5+) and compressed object streams |
| **Text encodings** | ToUnicode CMaps (bfchar + bfrange), WinAnsi, MacRoman, encoding differences, Adobe Glyph List (4200 names) |
| **Font types** | Type1, TrueType, CIDFont/Type0 composite fonts, standard 14 fonts with built-in width tables |
| **Compression** | FlateDecode, LZWDecode, ASCII85Decode, ASCIIHexDecode, PNG predictors, filter chains |
| **Page features** | Resource inheritance from page tree, rotation (0/90/180/270), MediaBox/CropBox |
| **Content streams** | All text operators (BT/ET/Tf/Tm/Td/TD/T\*/TJ/Tj/'/"), graphics state stack (q/Q), CTM (cm) |
| **XObjects** | Recursive text extraction from Form XObjects via Do operator |
| **Marked content** | ActualText extraction (BMC/BDC/EMC) with UTF-16BE support |
| **Encryption** | Standard security handler: RC4 40/128-bit (R2/R3), AES-128 (R4), AES-256 (R5/R6), crypt filters, /EncryptMetadata |
| **Structure** | Linearized PDFs, incremental updates, indirect Length references |

## Limitations

- **Redaction covers two operations with different guarantees.** `RemoveText` / `RemoveRegion` delete the glyphs from the page content streams and the Form XObjects those pages draw — and nothing else in the file (see the table above). `RedactText` / `Redact` only draw a rectangle: the text stays in the content stream and remains recoverable by copy/paste or any PDF parser.
- **Reading encrypted PDFs is supported; writing them is not.** Output from merge, rewrite, and creation is always unencrypted, so an encrypted input is effectively decrypted by any operation that writes it back out. Public-key (certificate) security handlers are not supported.
- **Auto-detection judges a table by its column names.** Running text is
  sliced into "columns" wherever word gaps line up, so auto-detection rejects
  candidates whose headings read as fragments rather than words. That is what
  keeps prose from being reported as a table, and it means a real table whose
  columns are named with one or two characters needs `-headers` (or
  `TableOpts.Headers`) to be found.
- No image extraction
- **Images read as PNG, JPEG and GIF only** — what the standard library decodes. HEIC and AVIF need an HEVC or AV1 decoder, which it does not have; the Go decoders that do exist are either CGo wrappers around LGPL libraries or wrap AGPL-licensed code, so neither fits a CGo-free MIT library. WebP and TIFF would need `golang.org/x/image`, a dependency this library does not take. Unsupported formats are named in the error, with a conversion command.
- PDF creation supports standard 14 fonts only (no font embedding)
- Merge drops interactive features (forms, bookmarks, JS)
- Text overlay uses Helvetica only

## Architecture

```
pdf/
  document.go   Public API (Document, Page)
  reader.go     PDF structure: xref, streams, object resolution, fonts, CMap
  text.go       Content stream -> positioned text spans -> line reconstruction
  table.go      Table detection: header anchors, gap-based auto-detect, multi-page
  writer.go     PDF object serializer, xref generation, FlateDecode compression
  merge.go      PDF merge: size constraints (fail/truncate/shrink), stream dedup, JPEG recompression
  edit.go       Text search, text overlay, image overlay, visual redaction
  redact.go     Text removal: glyph-level content stream rewriting
  image.go      Image decoding (PNG/JPEG/GIF) → RGB + grayscale SMask streams
  creator.go    PDF creation from scratch (text, shapes, images, fonts)
  lexer.go      PDF byte stream tokenizer
  parser.go     Token -> object parser (dicts, arrays, refs)
  objects.go    Types: Dict, Array, Name, Ref, Stream; matrix math helpers
  glyphlist.go  Adobe Glyph List (generated, 4200 entries)
  stdfonts.go   Standard 14 font width tables

cmd/
  gopdf         CLI: tables, merge, watermark
  sample        generates the README's sample PDFs
  genglyphlist  regenerates glyphlist.go from the Adobe Glyph List
```

## License

MIT — see [LICENSE](LICENSE).
