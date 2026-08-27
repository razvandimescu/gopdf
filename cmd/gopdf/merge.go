package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/razvandimescu/gopdf/pdf"
)

// paperSizes are the -page values that fix the page, in points.
var paperSizes = map[string]struct {
	name          string
	width, height float64
}{
	"a4":     {"A4", 595, 842},
	"letter": {"Letter", 612, 792},
}

func runMerge(args []string) error {
	fs := flag.NewFlagSet("merge", flag.ExitOnError)
	out := fs.String("o", "", "output PDF path (default: stdout)")
	page := fs.String("page", "a4", "page size for image inputs: a4, letter, or image (page matches the image)")
	dpi := fs.Float64("dpi", 0, "image resolution in pixels per inch; 0 reads it from the file, falling back to 72")
	margin := fs.Float64("margin", 0, "whitespace around an image, in points (fitting already letterboxes)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: gopdf merge [flags] input1 input2 ...\n\n"+
			"Each input is a PDF or a PNG/JPEG image, detected by content, not by\n"+
			"extension. Images become one page each, in the order given.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	paths, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		fs.Usage()
		os.Exit(2)
	}
	layout, err := parseLayout(*page, *dpi, *margin)
	if err != nil {
		return err
	}

	docs := make([][]byte, len(paths))
	images := 0
	var natural naturalPage
	for i, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !isPDF(data) {
			converted, asked, err := layout.imageToPDF(data)
			if err != nil {
				return fmt.Errorf("%s: %w%s", path, err, conversionHint(path, err))
			}
			if images == 0 {
				natural = asked
			}
			images++
			data = converted
		}
		docs[i] = data
	}

	merged := docs[0] // a lone input needs no merge pass
	if len(docs) > 1 {
		if merged, err = pdf.MergeBytes(docs...); err != nil {
			return err
		}
	}

	destination := *out
	if destination == "" {
		if _, err := os.Stdout.Write(merged); err != nil {
			return err
		}
		destination = "stdout"
	} else if err := os.WriteFile(destination, merged, 0644); err != nil {
		return err
	}
	report(destination, len(paths), images, merged, layout, natural)
	return nil
}

// conversionHint spells out a command that turns a file gopdf cannot decode
// into one it can, so the error says what to do and not only what failed.
// It returns "" for any other failure.
func conversionHint(path string, err error) string {
	var unsupported *pdf.UnsupportedFormatError
	if !errors.As(err, &unsupported) {
		return ""
	}
	jpg := strings.TrimSuffix(path, filepath.Ext(path)) + ".jpg"
	command := fmt.Sprintf("magick %s %s", path, jpg) // ImageMagick, everywhere else
	if runtime.GOOS == "darwin" {
		command = fmt.Sprintf("sips -s format jpeg %s --out %s", path, jpg)
	}
	return "\n  convert it first:  " + command
}

// isPDF reports whether data carries a PDF header, using the same tolerance
// for prepended bytes that the reader applies.
func isPDF(data []byte) bool {
	return bytes.Contains(data[:min(1024, len(data))], []byte("%PDF-"))
}

// layout turns an image into a page.
type layout struct {
	paper         string  // "A4", "Letter", or "" when a page follows its image
	width, height float64 // the paper's size; unset when paper is ""
	dpi, margin   float64 // dpi 0 means "whatever the file declares"
}

// fitsPaper reports whether images are fitted to a fixed page rather than
// given one of their own.
func (l layout) fitsPaper() bool { return l.paper != "" }

func parseLayout(page string, dpi, margin float64) (layout, error) {
	if dpi < 0 {
		return layout{}, fmt.Errorf("-dpi must not be negative, got %v", dpi)
	}
	if margin < 0 {
		return layout{}, fmt.Errorf("-margin must not be negative, got %v", margin)
	}
	paper, fixed := paperSizes[strings.ToLower(page)]
	if !fixed {
		if strings.ToLower(page) != "image" {
			return layout{}, fmt.Errorf("unknown -page %q; want a4, letter, or image", page)
		}
		return layout{dpi: dpi, margin: margin}, nil
	}
	if paper.width <= 2*margin || paper.height <= 2*margin {
		return layout{}, fmt.Errorf("-margin %v leaves no room on a %v×%v page", margin, paper.width, paper.height)
	}
	return layout{
		paper: paper.name,
		width: paper.width, height: paper.height,
		dpi: dpi, margin: margin,
	}, nil
}

// resolution is the dots per inch to place an image at: an explicit -dpi
// wins, then whatever the file declares, and 72 when nothing does.
func (l layout) resolution(img *pdf.Image) float64 {
	if l.dpi > 0 {
		return l.dpi
	}
	if img.DPI > 0 {
		return img.DPI
	}
	return 72
}

// naturalPage is the page an image would occupy at its own resolution — what
// -page image produces, which the summary offers as the alternative.
type naturalPage struct {
	width, height, dpi float64
}

// imageToPDF renders one encoded image as a single-page PDF, and reports the
// page that image would have asked for had it been left to choose.
func (l layout) imageToPDF(data []byte) ([]byte, naturalPage, error) {
	img, err := pdf.LoadImageBytes(data)
	if err != nil {
		var unsupported *pdf.UnsupportedFormatError
		if errors.As(err, &unsupported) {
			return nil, naturalPage{}, unsupported // a named format is diagnosis enough
		}
		return nil, naturalPage{}, fmt.Errorf("not a PDF, and %w", err)
	}
	dw, dh := img.DisplaySize() // a photo held sideways occupies a taller page
	dpi := l.resolution(img)
	w := float64(dw) * 72 / dpi
	h := float64(dh) * 72 / dpi
	natural := naturalPage{width: w, height: h, dpi: dpi}

	pageW, pageH := w+2*l.margin, h+2*l.margin
	if l.fitsPaper() {
		pageW, pageH = l.width, l.height
		// Rotation 0: the largest w×h with the image's aspect ratio that fits
		// the page inside its margins.
		w, h = img.FitRotated(pageW-2*l.margin, pageH-2*l.margin, 0, 1)
	}

	c := pdf.NewCreator()
	c.NewPage(pageW, pageH).DrawImage(img, (pageW-w)/2, (pageH-h)/2, w, h)
	out, err := c.Build()
	return out, natural, err
}

// report describes what came out, on stderr so it stays clear of a PDF on
// stdout. A page size is invisible until someone prints the file, so when
// images were fitted to paper the summary also names the way out.
func report(destination string, inputs, images int, merged []byte, l layout, natural naturalPage) {
	pages := "unknown pages"
	if doc, err := pdf.OpenBytes(merged); err == nil {
		pages = fmt.Sprintf("%d page%s", doc.NumPages(), plural(doc.NumPages()))
	}
	fmt.Fprintf(os.Stderr, "%d input%s, %s → %s (%s)\n",
		inputs, plural(inputs), pages, destination, humanSize(len(merged)))

	if images == 0 || !l.fitsPaper() {
		return
	}
	fmt.Fprintf(os.Stderr,
		"  %d image%s fitted to %s %.0f×%.0fpt; -page image sizes each page to its image (%.0f×%.0fpt at %.0fdpi)\n",
		images, plural(images), l.paper, l.width, l.height,
		natural.width, natural.height, natural.dpi)
}
