// watermark applies an image as a diagonal watermark on every page of a PDF.
//
// Usage:
//
//	watermark -i input.pdf -img logo.png -o out.pdf [flags]
//
// Flags:
//
//	-angle       rotation in degrees, counter-clockwise (default 45)
//	-opacity     0..1, where 1 is fully opaque (default 0.15)
//	-scale       fraction of the page diagonal to occupy (default 0.85)
//	-skip-first  leave the first page (cover) un-watermarked
//	-skip-last   leave the last page un-watermarked
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/razvandimescu/gopdf/pdf"
)

func main() {
	in := flag.String("i", "", "input PDF path (required)")
	img := flag.String("img", "", "watermark image path; PNG or JPEG (required)")
	out := flag.String("o", "", "output PDF path (required)")
	angle := flag.Float64("angle", 45, "rotation in degrees, counter-clockwise")
	opacity := flag.Float64("opacity", 0.15, "opacity in [0, 1]")
	scale := flag.Float64("scale", 0.85, "watermark size as a fraction of the page diagonal")
	skipFirst := flag.Bool("skip-first", false, "leave the first page un-watermarked")
	skipLast := flag.Bool("skip-last", false, "leave the last page un-watermarked")
	flag.Parse()

	if *in == "" || *img == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}

	data, err := os.ReadFile(*in)
	if err != nil {
		die("read input pdf: %v", err)
	}
	logo, err := pdf.LoadImage(*img)
	if err != nil {
		die("load image: %v", err)
	}

	editor := pdf.NewEditor(data)
	doc, err := editor.Document()
	if err != nil {
		die("parse pdf: %v", err)
	}

	for i := 0; i < doc.NumPages(); i++ {
		if (*skipFirst && i == 0) || (*skipLast && i == doc.NumPages()-1) {
			continue
		}
		mb := doc.Page(i).MediaBox()
		pageW := mb[2] - mb[0]
		pageH := mb[3] - mb[1]
		// On 90°/270° pages the displayed page is sideways; size and center
		// against the visible dimensions. The editor places the overlay in
		// displayed space, so this stays consistent across all pages.
		if rot := doc.Page(i).Rotation(); rot == 90 || rot == 270 {
			pageW, pageH = pageH, pageW
		}
		w, h := logo.FitRotated(pageW, pageH, *angle, *scale)

		editor.AddImage(pdf.ImageOverlay{
			Page:     i,
			Image:    logo,
			CX:       mb[0] + pageW/2,
			CY:       mb[1] + pageH/2,
			Width:    w,
			Height:   h,
			Rotation: *angle,
			Opacity:  *opacity,
		})
	}

	output, err := editor.Apply()
	if err != nil {
		die("apply watermark: %v", err)
	}
	if err := os.WriteFile(*out, output, 0644); err != nil {
		die("write output: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Wrote %s (%d bytes, %d pages)\n", *out, len(output), doc.NumPages())
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
