# gopdf

Pure Go PDF text extraction library. Zero external dependencies, no CGo.

Built from scratch as an alternative to [MuPDF](https://mupdf.com/) bindings and [unipdf](https://github.com/unidoc/unipdf) (AGPL). Extracts text with accurate spatial positioning — useful for parsing tables, forms, and structured documents.

## Install

```bash
go get github.com/anthropics/gopdf/pdf
```

## Quick Start

```go
package main

import (
    "fmt"
    "log"

    "github.com/anthropics/gopdf/pdf"
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
doc, err := pdf.OpenFile("document.pdf")
if err != nil {
    log.Fatal(err)
}

text, _ := doc.Text()
fmt.Println(text)
```

### Positioned text spans

Each span carries its X/Y coordinates, font name, and size — useful for column detection and table parsing.

```go
page := doc.Page(0)
spans, _ := page.TextSpans()

for _, span := range spans {
    fmt.Printf("(%.1f, %.1f) [%s %.0fpt] %q\n",
        span.X, span.Y, span.Font, span.FontSize, span.Text)
}
```

### Open from bytes

```go
data, _ := os.ReadFile("document.pdf")
doc, err := pdf.OpenBytes(data)
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

### Page

| Method | Returns | Description |
|---|---|---|
| `page.Text()` | `string, error` | Full page text |
| `page.TextLines()` | `[]TextLine, error` | Lines grouped by Y, sorted top-to-bottom |
| `page.TextSpans()` | `[]TextSpan, error` | Raw positioned spans |
| `page.Rotation()` | `int` | Rotation in degrees (0/90/180/270) |
| `page.MediaBox()` | `[4]float64` | Page bounds [llx, lly, urx, ury] |

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
```

## Features

**PDF parsing** — lexer, object parser, xref tables and xref streams (PDF 1.5+), compressed object streams, linearized PDFs, filter chains (FlateDecode, LZW, ASCII85, ASCIIHex), PNG predictors.

**Text extraction** — all PDF text operators (BT/ET/Tf/Tm/Td/TD/T\*/TJ/Tj/'/"), proper affine matrix math for positioning, graphics state stack (q/Q), coordinate transforms (cm), Form XObject recursion (Do).

**Font decoding** — ToUnicode CMaps (bfchar + bfrange with array destinations), encoding differences, WinAnsi and MacRoman predefined encodings, Adobe Glyph List (4200 names), CIDFont/Type0 composite fonts with /W sparse width arrays, standard 14 font width tables, UTF-16 surrogate pair decoding.

**Page handling** — resource inheritance from page tree, rotation (90/180/270), MediaBox/CropBox, MarkedContent with ActualText (BMC/BDC/EMC).

## Comparison with Alternatives

| Library | License | Text Extraction | Positional Data | Pure Go |
|---|---|---|---|---|
| **gopdf** | MIT | Yes | Yes (per-span X/Y) | Yes |
| unipdf | AGPL/Commercial | Yes | Yes (TextMark) | Yes |
| ledongthuc/pdf | BSD-3 | Basic | Partial | Yes |
| pdfcpu | Apache-2.0 | Raw streams only | No | Yes |
| rsc.io/pdf | BSD-3 | No | No | Yes |

## Architecture

```
pdf/
  document.go   Public API (Document, Page)
  reader.go     PDF structure: xref, streams, object resolution, fonts, CMap
  text.go       Content stream → positioned text spans → line reconstruction
  lexer.go      PDF byte stream tokenizer
  parser.go     Token → object parser (dicts, arrays, refs)
  objects.go    Types: Dict, Array, Name, Ref, Stream; matrix math helpers
  glyphlist.go  Adobe Glyph List (generated, 4200 entries)
  stdfonts.go   Standard 14 font width tables
```

## Limitations

- No encryption/password support yet
- Text extraction only (no images)
- TIFF predictor not implemented
- DCTDecode/JPXDecode passed through (irrelevant for text)

## License

MIT
