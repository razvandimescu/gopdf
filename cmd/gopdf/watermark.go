package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/razvandimescu/gopdf/pdf"
)

func runWatermark(args []string) error {
	fs := flag.NewFlagSet("watermark", flag.ExitOnError)
	image := fs.String("img", "", "watermark image path; PNG, JPEG or GIF (required)")
	out := fs.String("o", "", "output PDF path (default: stdout)")
	angle := fs.Float64("angle", 45, "rotation in degrees, counter-clockwise")
	opacity := fs.Float64("opacity", 0.15, "opacity in [0, 1]")
	scale := fs.Float64("scale", 0.85, "watermark size as a fraction of the page")
	skipFirst := fs.Bool("skip-first", false, "leave the first page un-watermarked")
	skipLast := fs.Bool("skip-last", false, "leave the last page un-watermarked")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: gopdf watermark [flags] -img logo.png input.pdf\n\n"+
			"Stamps the image diagonally across every page, centred and scaled to the\n"+
			"page it lands on.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	paths, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(paths) != 1 || *image == "" {
		fs.Usage()
		os.Exit(2)
	}
	if *opacity < 0 || *opacity > 1 {
		return fmt.Errorf("-opacity must be in [0, 1], got %v", *opacity)
	}

	data, err := os.ReadFile(paths[0])
	if err != nil {
		return err
	}
	logo, err := pdf.LoadImage(*image)
	if err != nil {
		return fmt.Errorf("%s: %w%s", *image, err, conversionHint(*image, err))
	}

	editor := pdf.NewEditor(data)
	doc, err := editor.Document()
	if err != nil {
		return err
	}

	stamped := 0
	for i := range doc.NumPages() {
		if (*skipFirst && i == 0) || (*skipLast && i == doc.NumPages()-1) {
			continue
		}
		box := doc.Page(i).MediaBox()
		pageW, pageH := box[2]-box[0], box[3]-box[1]
		// A 90°/270° page is displayed sideways; size and centre against what
		// the reader sees, which is the space the editor places overlays in.
		if rotation := doc.Page(i).Rotation(); rotation == 90 || rotation == 270 {
			pageW, pageH = pageH, pageW
		}
		w, h := logo.FitRotated(pageW, pageH, *angle, *scale)

		editor.AddImage(pdf.ImageOverlay{
			Page:  i,
			Image: logo,
			CX:    box[0] + pageW/2,
			CY:    box[1] + pageH/2,
			Width: w, Height: h,
			Rotation: *angle,
			Opacity:  *opacity,
		})
		stamped++
	}

	output, err := editor.Apply()
	if err != nil {
		return err
	}

	destination := *out
	if destination == "" {
		if _, err := os.Stdout.Write(output); err != nil {
			return err
		}
		destination = "stdout"
	} else if err := os.WriteFile(destination, output, 0644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%d of %d page%s stamped → %s (%s)\n",
		stamped, doc.NumPages(), plural(doc.NumPages()), destination, humanSize(len(output)))
	return nil
}
