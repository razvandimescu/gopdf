package pdf

import "os"

// Document represents an opened PDF file.
type Document struct {
	reader *Reader
	pages  []Dict
}

// OpenFile opens a PDF from a file path.
func OpenFile(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return OpenBytes(data)
}

// OpenBytes opens a PDF from raw bytes.
func OpenBytes(data []byte) (*Document, error) {
	r, err := Open(data)
	if err != nil {
		return nil, err
	}
	pages, err := r.Pages()
	if err != nil {
		return nil, err
	}
	return &Document{reader: r, pages: pages}, nil
}

// NumPages returns the number of pages in the document.
func (d *Document) NumPages() int {
	return len(d.pages)
}

// Page returns the page at index n (0-based).
func (d *Document) Page(n int) *Page {
	if n < 0 || n >= len(d.pages) {
		return nil
	}
	return &Page{dict: d.pages[n], reader: d.reader, num: n}
}

// Text extracts all text from all pages, joined by newlines.
func (d *Document) Text() (string, error) {
	var result string
	for i := range d.pages {
		p := d.Page(i)
		text, err := p.Text()
		if err != nil {
			return "", err
		}
		if i > 0 {
			result += "\n"
		}
		result += text
	}
	return result, nil
}

// Page represents a single PDF page.
type Page struct {
	dict   Dict
	reader *Reader
	num    int
}

// TextSpans returns the raw positioned text spans on this page.
func (p *Page) TextSpans() ([]TextSpan, error) {
	spans := ExtractPageText(p.dict, p.reader)
	return spans, nil
}

// TextLines returns text grouped into spatial lines (sorted top-to-bottom).
func (p *Page) TextLines() ([]TextLine, error) {
	spans, err := p.TextSpans()
	if err != nil {
		return nil, err
	}
	return BuildLines(spans), nil
}

// Text returns the full text of the page as a single string.
func (p *Page) Text() (string, error) {
	lines, err := p.TextLines()
	if err != nil {
		return "", err
	}
	var result string
	for i, line := range lines {
		if i > 0 {
			result += "\n"
		}
		result += line.Text
	}
	return result, nil
}

// Rotation returns the page rotation in degrees (0, 90, 180, 270).
func (p *Page) Rotation() int {
	r, _ := p.dict.Int("Rotate")
	return r
}

// MediaBox returns the page media box [llx, lly, urx, ury].
func (p *Page) MediaBox() [4]float64 {
	mb, ok := p.dict.Array("MediaBox")
	if !ok || len(mb) < 4 {
		return [4]float64{0, 0, 612, 792} // default US Letter
	}
	return [4]float64{asFloat(mb[0]), asFloat(mb[1]), asFloat(mb[2]), asFloat(mb[3])}
}
