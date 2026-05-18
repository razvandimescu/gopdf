// watermark applies a single image as a diagonal watermark on every page of a PDF.
//
// Usage:
//
//	watermark -i input.pdf -img logo.png -o out.pdf [flags]
//
// Flags:
//
//	-angle    rotation in degrees, counter-clockwise (default 45)
//	-opacity  0..1, where 1 is fully opaque (default 0.15)
//	-scale    fraction of the page diagonal to occupy (default 0.85)
package main

import (
	"flag"
	"fmt"
	"math"
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
	flag.Parse()

	if *in == "" || *img == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}

	data, err := os.ReadFile(*in)
	if err != nil {
		die("read input pdf: %v", err)
	}
	doc, err := pdf.OpenBytes(data)
	if err != nil {
		die("open pdf: %v", err)
	}
	logo, err := pdf.LoadImage(*img)
	if err != nil {
		die("load image: %v", err)
	}

	editor := pdf.NewEditor(data)
	aspect := float64(logo.Height) / float64(logo.Width)

	for i := 0; i < doc.NumPages(); i++ {
		mb := doc.Page(i).MediaBox()
		pageW := mb[2] - mb[0]
		pageH := mb[3] - mb[1]
		cx := mb[0] + pageW/2
		cy := mb[1] + pageH/2

		// Size the image so the longest edge of the rotated bounding box
		// covers `scale` × min(page width, page height). This keeps the
		// watermark from spilling over the page at any angle.
		theta := *angle * math.Pi / 180
		w := math.Min(pageW, pageH) * (*scale) /
			(math.Abs(math.Cos(theta)) + aspect*math.Abs(math.Sin(theta)))
		h := w * aspect

		editor.AddImage(pdf.ImageOverlay{
			Page:     i,
			Image:    logo,
			CX:       cx,
			CY:       cy,
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
