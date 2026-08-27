package pdf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
)

// Image is a raster image ready to embed as a PDF XObject. Decoded pixels are
// row-major, top-to-bottom — the orientation PDF Image XObjects expect when
// drawn with a positive CTM scale. A JPEG that PDF can decode itself is held
// in its original encoding instead, and rgb is nil.
type Image struct {
	Width, Height int
	Orientation   int     // EXIF orientation 1-8; 1 means "as stored"
	DPI           float64 // resolution the file declares; 0 when it declares none
	rgb           []byte  // len = Width*Height*3; nil when jpeg is set
	alpha         []byte  // len = Width*Height; nil if fully opaque
	jpeg          []byte  // original JPEG bytes, to embed with DCTDecode
	components    int     // jpeg color components: 1 grayscale, 3 YCbCr
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
//
// A JPEG that PDF viewers can decode themselves keeps its original encoding
// and is embedded with DCTDecode. Decoding a photograph to RGB and deflating
// it instead would multiply its size several-fold, since Flate has little to
// find in continuous-tone pixels.
func LoadImageBytes(data []byte) (*Image, error) {
	orientation, dpi := declaredMetadata(data)
	if w, h, components, ok := jpegEmbeddable(data); ok {
		return &Image{
			Width: w, Height: h, Orientation: orientation, DPI: dpi,
			jpeg: data, components: components,
		}, nil
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		if name := recognizedFormat(data); name != "" {
			return nil, &UnsupportedFormatError{Format: name}
		}
		return nil, fmt.Errorf("decode image: %w", err)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

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
	return &Image{
		Width: w, Height: h, Orientation: orientation, DPI: dpi,
		rgb: rgb, alpha: alpha,
	}, nil
}

// DisplaySize returns the dimensions the image occupies once its EXIF
// orientation is honoured: a quarter turn swaps width and height.
func (img *Image) DisplaySize() (width, height int) {
	if img.Orientation >= 5 && img.Orientation <= 8 {
		return img.Height, img.Width
	}
	return img.Width, img.Height
}

// orientationMatrices map the unit square onto itself so that an image stored
// with a given EXIF orientation appears the right way up. Index 0 is unused;
// 1-8 follow the EXIF tag's own numbering.
var orientationMatrices = [9][6]float64{
	1: {1, 0, 0, 1, 0, 0},   // as stored
	2: {-1, 0, 0, 1, 1, 0},  // mirrored left to right
	3: {-1, 0, 0, -1, 1, 1}, // half turn
	4: {1, 0, 0, -1, 0, 1},  // mirrored top to bottom
	5: {0, 1, 1, 0, 0, 0},   // transposed
	6: {0, -1, 1, 0, 0, 1},  // quarter turn clockwise
	7: {0, -1, -1, 0, 1, 1}, // transverse
	8: {0, 1, -1, 0, 1, 0},  // quarter turn counter-clockwise
}

func (img *Image) orientationMatrix() [6]float64 {
	if img.Orientation >= 1 && img.Orientation <= 8 {
		return orientationMatrices[img.Orientation]
	}
	return orientationMatrices[1]
}

// declaredMetadata reads what a file says about itself beyond its pixels:
// the orientation the camera recorded, and the resolution it claims. Both
// change where the image belongs on a page without touching a single pixel.
func declaredMetadata(data []byte) (orientation int, dpi float64) {
	if bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return 1, pngDPI(data)
	}
	return jpegMetadata(data)
}

// jpegMetadata walks a JPEG's marker segments. EXIF wins over JFIF where both
// declare a resolution: a camera's own APP1 is the more considered claim.
func jpegMetadata(data []byte) (orientation int, dpi float64) {
	orientation = 1
	var jfif float64
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			break
		}
		marker := data[i+1]
		if marker == 0xFF {
			i++
			continue
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD9) {
			i += 2
			continue
		}
		if marker == 0xDA { // scan data begins; no metadata past here
			break
		}
		length := int(data[i+2])<<8 | int(data[i+3])
		if length < 2 || i+2+length > len(data) {
			break
		}

		seg := data[i+4 : i+2+length]
		switch marker {
		case 0xE0:
			if d := jfifDPI(seg); d > 0 && jfif == 0 {
				jfif = d
			}
		case 0xE1:
			if o, d := exifMetadata(seg); o != 0 || d != 0 {
				if o != 0 && orientation == 1 {
					orientation = o
				}
				if d != 0 && dpi == 0 {
					dpi = d
				}
			}
		}
		i += 2 + length
	}
	if dpi == 0 {
		dpi = jfif
	}
	return orientation, dpi
}

// jfifDPI reads the pixel density out of an APP0 JFIF segment. Units 0 means
// the numbers are an aspect ratio only, carrying no physical size.
func jfifDPI(seg []byte) float64 {
	if len(seg) < 12 || string(seg[:5]) != "JFIF\x00" {
		return 0
	}
	density := float64(int(seg[8])<<8 | int(seg[9]))
	switch seg[7] {
	case 1: // dots per inch
		return density
	case 2: // dots per centimetre
		return density * 2.54
	}
	return 0
}

// exifMetadata reads orientation (0x0112) and resolution (0x011A with the
// unit in 0x0128) out of an APP1 segment's first IFD. Either result is 0 when
// the segment is not EXIF or does not carry that tag.
func exifMetadata(seg []byte) (orientation int, dpi float64) {
	if len(seg) < 14 || string(seg[:6]) != "Exif\x00\x00" {
		return 0, 0
	}
	tiff := seg[6:]
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0, 0
	}

	ifd := int(order.Uint32(tiff[4:8]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 0, 0
	}

	var resolution float64
	unit := 2 // EXIF's default when the tag is absent is inches
	for e, count := 0, int(order.Uint16(tiff[ifd:ifd+2])); e < count; e++ {
		p := ifd + 2 + e*12
		if p+12 > len(tiff) {
			break
		}
		switch order.Uint16(tiff[p : p+2]) {
		case 0x0112: // Orientation
			if o := int(order.Uint16(tiff[p+8 : p+10])); o >= 1 && o <= 8 {
				orientation = o
			}
		case 0x011A: // XResolution, a rational at an offset
			resolution = rationalAt(tiff, order, int(order.Uint32(tiff[p+8:p+12])))
		case 0x0128: // ResolutionUnit: 2 inches, 3 centimetres
			unit = int(order.Uint16(tiff[p+8 : p+10]))
		}
	}

	switch unit {
	case 2:
		dpi = resolution
	case 3:
		dpi = resolution * 2.54
	}
	return orientation, dpi
}

// rationalAt reads the numerator/denominator pair an EXIF RATIONAL points to.
func rationalAt(tiff []byte, order binary.ByteOrder, offset int) float64 {
	if offset < 0 || offset+8 > len(tiff) {
		return 0
	}
	numerator := order.Uint32(tiff[offset : offset+4])
	denominator := order.Uint32(tiff[offset+4 : offset+8])
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

// pngDPI reads the pHYs chunk, whose density is in pixels per metre.
func pngDPI(data []byte) float64 {
	for i := 8; i+12 <= len(data); {
		length := int(binary.BigEndian.Uint32(data[i : i+4]))
		if length < 0 || i+12+length > len(data) {
			return 0
		}
		switch string(data[i+4 : i+8]) {
		case "pHYs":
			if length != 9 {
				return 0
			}
			body := data[i+8 : i+8+9]
			if body[8] != 1 { // unit specifier: 1 is the metre
				return 0
			}
			return float64(binary.BigEndian.Uint32(body[0:4])) * 0.0254
		case "IDAT": // pixels begin; pHYs must precede them
			return 0
		}
		i += 12 + length
	}
	return 0
}

// FitRotated returns the width and height at which to draw the image, centered
// on a page of size (pageW, pageH), so that after rotating by `rotation`
// degrees its bounding box occupies `scale` (0..1) of the page in both
// dimensions. The image's aspect ratio is preserved. Pair it with an
// ImageOverlay whose Width/Height are the returned values and whose Rotation
// matches `rotation`.
func (img *Image) FitRotated(pageW, pageH, rotation, scale float64) (width, height float64) {
	dw, dh := img.DisplaySize()
	aspect := float64(dh) / float64(dw)
	theta := rotation * math.Pi / 180
	cosT, sinT := math.Abs(math.Cos(theta)), math.Abs(math.Sin(theta))
	// Rotated bounding box of a w×h rectangle spans w·|cos|+h·|sin| horizontally
	// and w·|sin|+h·|cos| vertically; bound both against the page.
	w := math.Min(pageW*scale/(cosT+aspect*sinT), pageH*scale/(sinT+aspect*cosT))
	return w, w * aspect
}

// UnsupportedFormatError reports an image format gopdf recognizes but cannot
// decode. Callers can match it with errors.As to say something useful about
// the file — suggest a conversion, say — rather than relay "unknown format".
type UnsupportedFormatError struct {
	Format string // "HEIC", "HEIF", "AVIF", "WebP", "TIFF", "GIF" or "BMP"
}

func (e *UnsupportedFormatError) Error() string {
	return fmt.Sprintf("%s is not supported (PNG and JPEG only)", e.Format)
}

// heifBrands are the ISO base media brands that mark a still image the
// standard library cannot decode; the pixels behind them are HEVC or AV1.
var heifBrands = map[string]string{
	"heic": "HEIC", "heix": "HEIC", "heim": "HEIC", "heis": "HEIC",
	"hevc": "HEIC", "hevx": "HEIC", "hevm": "HEIC", "hevs": "HEIC",
	"mif1": "HEIF", "msf1": "HEIF",
	"avif": "AVIF", "avis": "AVIF",
}

// recognizedFormat names an image format gopdf can identify but not decode,
// so callers can say which format they were handed instead of "unknown".
// It returns "" for anything it does not recognize.
func recognizedFormat(data []byte) string {
	switch {
	case len(data) >= 12 && string(data[4:8]) == "ftyp":
		return heifBrands[string(data[8:12])] // "" for other ISO media files
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "WebP"
	case bytes.HasPrefix(data, []byte("II*\x00")), bytes.HasPrefix(data, []byte("MM\x00*")):
		return "TIFF"
	case bytes.HasPrefix(data, []byte("BM")):
		return "BMP"
	case bytes.HasPrefix(data, []byte("GIF8")):
		return "GIF"
	}
	return ""
}

// jpegEmbeddable reports whether data is a JPEG every PDF viewer can decode
// behind DCTDecode: 8-bit, Huffman-coded, baseline or extended sequential,
// grayscale or YCbCr. Progressive, arithmetic-coded, 12-bit and CMYK JPEGs
// go through the decoder instead — viewer support for them is uneven, and
// Adobe-style CMYK additionally needs an inverted /Decode array.
func jpegEmbeddable(data []byte) (width, height, components int, ok bool) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 0, 0, 0, false
	}
	for i := 2; i+3 < len(data); {
		if data[i] != 0xFF {
			return 0, 0, 0, false // lost the marker structure
		}
		marker := data[i+1]
		// Fill bytes, and the standalone markers that carry no length.
		if marker == 0xFF {
			i++
			continue
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD9) {
			i += 2
			continue
		}

		length := int(data[i+2])<<8 | int(data[i+3])
		if length < 2 || i+2+length > len(data) {
			return 0, 0, 0, false
		}
		switch marker {
		case 0xC0, 0xC1: // SOF0 baseline, SOF1 extended sequential
			seg := data[i+4 : i+2+length]
			if len(seg) < 6 || seg[0] != 8 { // bits per component
				return 0, 0, 0, false
			}
			h := int(seg[1])<<8 | int(seg[2])
			w := int(seg[3])<<8 | int(seg[4])
			n := int(seg[5])
			if w == 0 || h == 0 || (n != 1 && n != 3) {
				return 0, 0, 0, false
			}
			return w, h, n, true
		case 0xDA: // SOS: scan data begins, so there is no frame header to find
			return 0, 0, 0, false
		}
		i += 2 + length
	}
	return 0, 0, 0, false
}
