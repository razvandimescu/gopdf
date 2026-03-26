# gopdf

Pure Go PDF library — text extraction, merging, search, overlay, and redaction. Zero external dependencies, no CGo.

Built from scratch as an alternative to [MuPDF](https://mupdf.com/) bindings and [unipdf](https://github.com/unidoc/unipdf) (AGPL). Extracts text with accurate spatial positioning, merges PDFs with page selection, finds text with bounding rectangles, and supports text overlay and visual redaction.

## Install

```bash
go get github.com/razvandimescu/gopdf/pdf
```

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

    for i := 0; i < doc.NumPages(); i++ {
        lines, _ := doc.Page(i).TextLines()
        for _, line := range lines {
            fmt.Println(line.Text)
        }
    }
}
```

## Usage

### Extract all text

```go
doc, _ := pdf.OpenFile("document.pdf")
text, _ := doc.Text()
fmt.Println(text)
```

### Positioned text spans

Each span carries its X/Y coordinates, font name, and size — useful for column detection and table parsing.

```go
spans, _ := doc.Page(0).TextSpans()
for _, span := range spans {
    fmt.Printf("(%.1f, %.1f) [%s %.0fpt] %q\n",
        span.X, span.Y, span.Font, span.FontSize, span.Text)
}
```

### Search for text

Find all occurrences with bounding rectangles.

```go
results := doc.Search("Invoice Total")
for _, r := range results {
    fmt.Printf("Page %d at (%.0f, %.0f) size %.0fx%.0f\n",
        r.Page, r.Rect.X, r.Rect.Y, r.Rect.Width, r.Rect.Height)
}
```

### Merge PDFs

```go
combined, _ := pdf.MergeFiles("a.pdf", "b.pdf", "c.pdf")
os.WriteFile("merged.pdf", combined, 0644)
```

With page selection:

```go
m := pdf.NewMerger()
m.AddFile("big.pdf", 0, 2, 5) // pages 0, 2, 5 only
m.Add(otherPDFBytes)           // all pages
result, _ := m.Merge()
```

### Text overlay

Draw text onto PDF pages.

```go
ed := pdf.NewEditor(data)
ed.AddText(pdf.TextOverlay{
    Page: 0, X: 100, Y: 50,
    Text: "APPROVED", FontSize: 24,
    R: 0, G: 0.5, B: 0, // green
})
result, _ := ed.Apply()
```

### Redaction

Cover text with filled rectangles.

```go
ed := pdf.NewEditor(data)
ed.RedactText("Confidential", 0, 0, 0) // black box over all matches
result, _ := ed.Apply()
```

Or redact a specific region:

```go
ed.Redact(pdf.RedactRegion{
    Page: 0,
    Rect: pdf.Rect{X: 100, Y: 700, Width: 200, Height: 20},
    R: 1, G: 1, B: 1, // white
})
```

Combine redaction and overlay (e.g., replace text):

```go
ed := pdf.NewEditor(data)
ed.RedactText("OLD-REF", 1, 1, 1)       // white box
ed.AddText(pdf.TextOverlay{              // replacement text
    Page: 0, X: 100, Y: 750,
    Text: "NEW-REF", FontSize: 12,
})
result, _ := ed.Apply()
```

## API

### Document

| Method | Returns | Description |
|---|---|---|
| `pdf.OpenFile(path)` | `*Document, error` | Open PDF from file |
| `pdf.OpenBytes(data)` | `*Document, error` | Open PDF from bytes |
| `doc.NumPages()` | `int` | Page count |
| `doc.Page(n)` | `*Page` | Page by 0-based index |
| `doc.Text()` | `string, error` | All text, pages joined by newline |
| `doc.Search(query)` | `[]SearchResult` | Find text across all pages |

### Page

| Method | Returns | Description |
|---|---|---|
| `page.Text()` | `string, error` | Full page text |
| `page.TextLines()` | `[]TextLine, error` | Lines grouped by Y, sorted top-to-bottom |
| `page.TextSpans()` | `[]TextSpan, error` | Raw positioned spans |
| `page.Search(query)` | `[]SearchResult` | Find text on this page |
| `page.Rotation()` | `int` | Rotation in degrees (0/90/180/270) |
| `page.MediaBox()` | `[4]float64` | Page bounds [llx, lly, urx, ury] |

### Merge

| Method | Returns | Description |
|---|---|---|
| `pdf.MergeFiles(paths...)` | `[]byte, error` | Merge PDF files by path |
| `pdf.MergeBytes(pdfs...)` | `[]byte, error` | Merge in-memory PDFs |
| `pdf.NewMerger()` | `*Merger` | Create merger for page selection |
| `m.AddFile(path, pages...)` | `error` | Add file (0-indexed pages; empty = all) |
| `m.Add(data, pages...)` | `error` | Add bytes (0-indexed pages; empty = all) |
| `m.Merge()` | `[]byte, error` | Produce combined PDF |

### Editor

| Method | Returns | Description |
|---|---|---|
| `pdf.NewEditor(data)` | `*Editor` | Create editor from PDF bytes |
| `pdf.NewEditorFromFile(path)` | `*Editor, error` | Create editor from file |
| `ed.AddText(overlay)` | | Draw text (Helvetica, any size/color) |
| `ed.Redact(region)` | | Cover area with filled rectangle |
| `ed.RedactText(query, r, g, b)` | `error` | Search and redact all matches |
| `ed.Apply()` | `[]byte, error` | Produce modified PDF |

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

type SearchResult struct {
    Page     int
    Text     string
    Rect     Rect    // bounding rectangle
    FontSize float64
}

type Rect struct {
    X, Y, Width, Height float64
}
```

## Features

**PDF parsing** — lexer, object parser, xref tables and xref streams (PDF 1.5+), compressed object streams, linearized PDFs, filter chains (FlateDecode, LZW, ASCII85, ASCIIHex), PNG predictors.

**Text extraction** — all PDF text operators (BT/ET/Tf/Tm/Td/TD/T\*/TJ/Tj/'/"), proper affine matrix math for positioning, graphics state stack (q/Q), coordinate transforms (cm), Form XObject recursion (Do).

**Font decoding** — ToUnicode CMaps (bfchar + bfrange with array destinations), encoding differences, WinAnsi and MacRoman predefined encodings, Adobe Glyph List (4200 names), CIDFont/Type0 composite fonts with /W sparse width arrays, standard 14 font width tables, UTF-16 surrogate pair decoding.

**Page handling** — resource inheritance from page tree, rotation (90/180/270), MediaBox/CropBox, MarkedContent with ActualText (BMC/BDC/EMC).

**PDF merging** — deep object graph copy with reference remapping, FlateDecode recompression, page tree construction, page selection. Produces valid PDF 1.7 output.

**Search and editing** — text search with bounding rectangles, text overlay using standard fonts, visual redaction with colored rectangles, combined overlay + redaction for text replacement.

## Comparison with Alternatives

| Library | License | Extract | Search | Merge | Edit | Pure Go |
|---|---|---|---|---|---|---|
| **gopdf** | MIT | Yes (positioned) | Yes | Yes | Overlay + redact | Yes |
| unipdf | AGPL/Commercial | Yes (TextMark) | Yes | Yes | Yes | Yes |
| pdfcpu | Apache-2.0 | Raw streams | No | Yes | Watermark | Yes |
| ledongthuc/pdf | BSD-3 | Basic | No | No | No | Yes |
| rsc.io/pdf | BSD-3 | No | No | No | No | Yes |

## Architecture

```
pdf/
  document.go   Public API (Document, Page)
  reader.go     PDF structure: xref, streams, object resolution, fonts, CMap
  text.go       Content stream → positioned text spans → line reconstruction
  writer.go     PDF object serializer, xref generation, FlateDecode compression
  merge.go      PDF merge: deep object copy with ref remapping, page tree construction
  edit.go       Text search, text overlay, visual redaction
  lexer.go      PDF byte stream tokenizer
  parser.go     Token → object parser (dicts, arrays, refs)
  objects.go    Types: Dict, Array, Name, Ref, Stream; matrix math helpers
  glyphlist.go  Adobe Glyph List (generated, 4200 entries)
  stdfonts.go   Standard 14 font width tables
```

## Limitations

- No encryption/password support yet
- No image extraction
- Merge drops interactive features (forms, bookmarks, JS)
- Redaction is visual only (rectangle drawn over text, not removed from stream)
- Text overlay uses Helvetica only
- TIFF predictor not implemented

## License

MIT
