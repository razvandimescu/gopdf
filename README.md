# gopdf

Pure Go PDF text extraction library. No CGo, no external dependencies.

## Features

- **PDF parsing**: lexer, parser, xref tables/streams, object streams, linearized PDFs
- **Text extraction**: content stream operators (BT/ET/Tf/Tm/Td/TJ/Tj/T*/'/"), spatial line reconstruction
- **Font support**: WinAnsi, MacRoman, ToUnicode CMaps, encoding differences, Adobe Glyph List (4200 entries), CIDFont/Type0 composite fonts, standard 14 font width tables
- **Graphics state**: q/Q save/restore, cm coordinate transforms, full CTM tracking
- **Form XObjects**: recursive text extraction from Form XObjects via Do operator
- **MarkedContent**: ActualText extraction (BMC/BDC/EMC) with UTF-16BE support
- **Page handling**: rotation (90/180/270), MediaBox/CropBox, resource inheritance from page tree
- **Stream filters**: FlateDecode, LZWDecode, ASCII85Decode, ASCIIHexDecode, PNG predictors, filter chains

## Install

```
go get gopdf/pdf
```

## Usage

### Simple: extract all text

```go
doc, err := pdf.OpenFile("document.pdf")
if err != nil {
    log.Fatal(err)
}

text, err := doc.Text()
if err != nil {
    log.Fatal(err)
}
fmt.Println(text)
```

### Per-page with lines

```go
doc, err := pdf.OpenFile("document.pdf")
if err != nil {
    log.Fatal(err)
}

for i := 0; i < doc.NumPages(); i++ {
    page := doc.Page(i)
    lines, err := page.TextLines()
    if err != nil {
        log.Fatal(err)
    }
    for _, line := range lines {
        fmt.Printf("Y=%.0f: %s\n", line.Y, line.Text)
    }
}
```

### Positioned text spans

```go
page := doc.Page(0)
spans, err := page.TextSpans()
if err != nil {
    log.Fatal(err)
}

for _, span := range spans {
    fmt.Printf("(%.1f, %.1f) [%s %.0fpt] %q\n",
        span.X, span.Y, span.Font, span.FontSize, span.Text)
}
```

### Low-level: from raw bytes

```go
data, _ := os.ReadFile("document.pdf")
reader, err := pdf.Open(data)
if err != nil {
    log.Fatal(err)
}

pages, _ := reader.Pages()
for _, page := range pages {
    content, _ := reader.PageContent(page)
    fonts := reader.PageFonts(page)
    resources := reader.PageResources(page)
    spans := pdf.ExtractTextWithResources(content, fonts, reader, resources)
    lines := pdf.BuildLines(spans)
    for _, line := range lines {
        fmt.Println(line.Text)
    }
}
```

## API

### High-level

| Function | Description |
|---|---|
| `pdf.OpenFile(path)` | Open a PDF file, returns `*Document` |
| `pdf.OpenBytes(data)` | Open a PDF from bytes, returns `*Document` |
| `doc.NumPages()` | Number of pages |
| `doc.Page(n)` | Get page by 0-based index |
| `doc.Text()` | Extract all text from all pages |
| `page.Text()` | Full page text as string |
| `page.TextLines()` | Spatial lines (Y-grouped, X-sorted) |
| `page.TextSpans()` | Raw positioned text spans |
| `page.Rotation()` | Page rotation in degrees |
| `page.MediaBox()` | Page dimensions `[llx, lly, urx, ury]` |

### Types

```go
type TextSpan struct {
    X, Y     float64  // position on page
    EndX     float64  // X after this span
    FontSize float64
    Font     string
    Text     string
}

type TextLine struct {
    Y     float64
    Spans []TextSpan
    Text  string      // reconstructed line text
}
```

## Architecture

```
pdf/
  document.go   - Public API (Document, Page)
  reader.go     - PDF structure (xref, streams, objects, fonts, CMap)
  text.go       - Content stream text extraction and line reconstruction
  lexer.go      - PDF tokenizer
  parser.go     - PDF object parser
  objects.go    - Type definitions (Dict, Array, Name, Ref, Stream)
  glyphlist.go  - Adobe Glyph List (4200 entries, generated)
  stdfonts.go   - Standard 14 font width tables
```

## Limitations

- No encryption/password support (planned)
- No image extraction (text-only)
- TIFF predictor (predictor 2) not implemented
- DCTDecode/JPXDecode filters passed through (not needed for text)

## License

MIT
