# gopdf

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/razvandimescu/gopdf/pdf.svg)](https://pkg.go.dev/github.com/razvandimescu/gopdf/pdf)

Pure Go library for PDF text extraction, merging, search, and editing — no CGo, no external dependencies.

Extract text with accurate spatial positioning and font metadata. Search with bounding rectangles. Merge files with page selection. Overlay text and redact regions. All from a single, MIT-licensed package with zero dependencies outside the Go standard library.

## Why gopdf?

If you need to read, search, or edit PDFs in Go without CGo or AGPL licensing constraints, gopdf is the only option that combines positioned text extraction, search with bounding rectangles, merge, overlay, and redaction in a single zero-dependency MIT-licensed package.

| | gopdf | unipdf | pdfcpu | ledongthuc/pdf | MuPDF bindings |
|---|---|---|---|---|---|
| **License** | MIT | AGPL / Commercial | Apache-2.0 | BSD-3 | AGPL |
| **CGo required** | No | No | No | No | Yes |
| **Text extraction** | Positioned (X/Y) | Positioned | Raw streams | Basic | Positioned |
| **Text search** | With rects | Yes | No | No | Yes |
| **PDF merge** | Yes | Yes | Yes | No | No |
| **Text overlay** | Yes | Yes | Watermark | No | No |
| **Visual redaction** | Yes | Yes | No | No | No |
| **PDF creation** | No | Yes | Yes | No | Yes |
| **Encryption** | No | Yes | Yes | No | Yes |
| **Dependencies** | 0 | Many | 0 | 0 | System lib |

## Features

- Text extraction with X/Y coordinates, font name, and font size
- Line reconstruction with intelligent spacing
- Text search returning bounding rectangles
- PDF merge with page selection
- Text overlay (Helvetica, configurable size and color)
- Visual redaction (filled rectangles with configurable color)
- Pure Go — no CGo, no system dependencies

## Installation

```bash
go get github.com/razvandimescu/gopdf@latest
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
// Y=756: Quotation Ref: MG74703
// Y=732: Quote Name: King David Sixth Form
// Y=710: Company: Optimus Facilities
// ...
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

```go
ed := pdf.NewEditor(data)
ed.RedactText("Confidential", 0, 0, 0) // black box over all matches
result, err := ed.Apply()
```

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

## API Reference

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

### Merger

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
| **Structure** | Linearized PDFs, incremental updates, indirect Length references |

## Limitations

- No encryption/password support (planned)
- No image extraction
- No PDF creation from scratch (read/edit/merge only)
- Merge drops interactive features (forms, bookmarks, JS)
- Redaction is visual only (rectangle drawn over text, not removed from stream)
- Text overlay uses Helvetica only

## Architecture

```
pdf/
  document.go   Public API (Document, Page)
  reader.go     PDF structure: xref, streams, object resolution, fonts, CMap
  text.go       Content stream -> positioned text spans -> line reconstruction
  writer.go     PDF object serializer, xref generation, FlateDecode compression
  merge.go      PDF merge: deep object copy with ref remapping, page tree construction
  edit.go       Text search, text overlay, visual redaction
  lexer.go      PDF byte stream tokenizer
  parser.go     Token -> object parser (dicts, arrays, refs)
  objects.go    Types: Dict, Array, Name, Ref, Stream; matrix math helpers
  glyphlist.go  Adobe Glyph List (generated, 4200 entries)
  stdfonts.go   Standard 14 font width tables
```

## License

MIT — see [LICENSE](LICENSE).
