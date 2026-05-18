package pdf

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
)

// Image is a decoded raster image, ready to embed as a PDF XObject. Pixels
// are row-major, top-to-bottom — the orientation PDF Image XObjects expect
// when drawn with a positive CTM scale.
type Image struct {
	Width, Height int
	rgb           []byte // len = Width*Height*3
	alpha         []byte // len = Width*Height; nil if fully opaque
}

// LoadImage decodes a PNG or JPEG image from disk.
func LoadImage(path string) (*Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadImageBytes(data)
}

// LoadImageBytes decodes a PNG or JPEG image from memory.
func LoadImageBytes(data []byte) (*Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("image has zero dimensions")
	}

	rgb := make([]byte, 0, w*h*3)
	alpha := make([]byte, 0, w*h)
	hasAlpha := false

	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			// RGBA() returns 16-bit alpha-premultiplied values.
			r, g, bl, a := img.At(x, y).RGBA()
			if a > 0 && a < 0xFFFF {
				// Un-premultiply so the PDF gets straight color + alpha.
				r = (r * 0xFFFF) / a
				g = (g * 0xFFFF) / a
				bl = (bl * 0xFFFF) / a
			}
			rgb = append(rgb, byte(r>>8), byte(g>>8), byte(bl>>8))
			a8 := byte(a >> 8)
			alpha = append(alpha, a8)
			if a8 != 0xFF {
				hasAlpha = true
			}
		}
	}

	if !hasAlpha {
		alpha = nil
	}
	return &Image{Width: w, Height: h, rgb: rgb, alpha: alpha}, nil
}
