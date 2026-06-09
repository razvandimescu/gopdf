package pdf

import (
	"math"
	"testing"
)

func TestImageFitRotated(t *testing.T) {
	const eps = 1e-9

	// boundingBox returns the rotated bounding-box extents of a width×height
	// rectangle rotated by `rotation` degrees.
	boundingBox := func(width, height, rotation float64) (bw, bh float64) {
		theta := rotation * math.Pi / 180
		c, s := math.Abs(math.Cos(theta)), math.Abs(math.Sin(theta))
		return width*c + height*s, width*s + height*c
	}

	tests := []struct {
		name            string
		imgW, imgH      int
		pageW, pageH    float64
		rotation, scale float64
	}{
		{"square image, square page, 45deg", 100, 100, 500, 500, 45, 0.85},
		{"wide logo, A4 portrait, 45deg", 1772, 591, 595, 842, 45, 0.85},
		{"tall logo, square page, 30deg", 100, 300, 500, 500, 30, 0.85},
		{"upright on landscape page", 400, 200, 842, 595, 0, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img := &Image{Width: tt.imgW, Height: tt.imgH}
			w, h := img.FitRotated(tt.pageW, tt.pageH, tt.rotation, tt.scale)

			// Aspect ratio is preserved.
			wantAspect := float64(tt.imgH) / float64(tt.imgW)
			if got := h / w; math.Abs(got-wantAspect) > eps {
				t.Fatalf("aspect = %v, want %v", got, wantAspect)
			}

			// The rotated bounding box fits within scale of the page in both
			// dimensions, and touches the limit in at least one (maximal fit).
			bw, bh := boundingBox(w, h, tt.rotation)
			limW, limH := tt.pageW*tt.scale, tt.pageH*tt.scale
			if bw > limW+1e-6 || bh > limH+1e-6 {
				t.Fatalf("bbox (%.3f, %.3f) exceeds limit (%.3f, %.3f)", bw, bh, limW, limH)
			}
			if math.Abs(bw-limW) > 1e-6 && math.Abs(bh-limH) > 1e-6 {
				t.Fatalf("bbox (%.3f, %.3f) touches neither limit (%.3f, %.3f); not maximal", bw, bh, limW, limH)
			}
		})
	}
}
