package pdf

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"strings"
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

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encoding test JPEG: %v", err)
	}
	return buf.Bytes()
}

func colorJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}
	return encodeJPEG(t, img)
}

func TestJPEGEmbeddable(t *testing.T) {
	gray := image.NewGray(image.Rect(0, 0, 20, 10))
	// A hand-built progressive frame: SOI, SOF2, SOS. Viewer support for
	// progressive behind DCTDecode is uneven, so it must not pass through.
	progressive := []byte{
		0xFF, 0xD8,
		0xFF, 0xC2, 0x00, 0x11, 0x08, 0x00, 0x10, 0x00, 0x20, 0x03,
		0x01, 0x11, 0x00, 0x02, 0x11, 0x01, 0x03, 0x11, 0x01,
		0xFF, 0xDA,
	}

	tests := []struct {
		name                string
		data                []byte
		wantOK              bool
		wantW, wantH, wantN int
	}{
		{"baseline color", colorJPEG(t, 64, 32), true, 64, 32, 3},
		{"baseline grayscale", encodeJPEG(t, gray), true, 20, 10, 1},
		{"progressive", progressive, false, 0, 0, 0},
		{"png", testPNG(t, false), false, 0, 0, 0},
		{"truncated", []byte{0xFF, 0xD8, 0xFF}, false, 0, 0, 0},
		{"empty", nil, false, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, h, n, ok := jpegEmbeddable(tt.data)
			if ok != tt.wantOK || w != tt.wantW || h != tt.wantH || n != tt.wantN {
				t.Errorf("jpegEmbeddable = (%d, %d, %d, %v), want (%d, %d, %d, %v)",
					w, h, n, ok, tt.wantW, tt.wantH, tt.wantN, tt.wantOK)
			}
		})
	}
}

// TestJPEGRidesThroughUnreencoded is the size guarantee: a photograph must
// not be inflated by decoding it to RGB and deflating the pixels.
func TestJPEGRidesThroughUnreencoded(t *testing.T) {
	src := colorJPEG(t, 400, 300)

	img, err := LoadImageBytes(src)
	if err != nil {
		t.Fatalf("loading JPEG: %v", err)
	}
	if img.rgb != nil {
		t.Error("JPEG was decoded to RGB instead of kept as-is")
	}
	if img.Width != 400 || img.Height != 300 {
		t.Errorf("size = %d×%d, want 400×300", img.Width, img.Height)
	}

	c := NewCreator()
	c.NewPage(400, 300).DrawImage(img, 0, 0, 400, 300)
	data, err := c.Build()
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	if len(data) > len(src)+4096 {
		t.Errorf("PDF is %d bytes for a %d-byte JPEG; it was re-encoded", len(data), len(src))
	}

	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("opening result: %v", err)
	}
	res, _ := doc.reader.ResolveDict(doc.pages[0]["Resources"])
	xobj, _ := doc.reader.ResolveDict(res["XObject"])
	stream, ok := doc.reader.Resolve(xobj["Im1"]).(*Stream)
	if !ok {
		t.Fatalf("image XObject does not resolve to a stream")
	}
	if stream.Dict["Filter"] != Name("DCTDecode") {
		t.Errorf("Filter = %v, want DCTDecode", stream.Dict["Filter"])
	}
	if stream.Dict["ColorSpace"] != Name("DeviceRGB") {
		t.Errorf("ColorSpace = %v, want DeviceRGB", stream.Dict["ColorSpace"])
	}
	if !bytes.Equal(stream.Raw, src) {
		t.Errorf("embedded bytes differ from the source JPEG (%d vs %d bytes)", len(stream.Raw), len(src))
	}
}

func TestGrayscaleJPEGKeepsItsColorSpace(t *testing.T) {
	gray := image.NewGray(image.Rect(0, 0, 32, 16))
	img, err := LoadImageBytes(encodeJPEG(t, gray))
	if err != nil {
		t.Fatalf("loading grayscale JPEG: %v", err)
	}
	c := NewCreator()
	c.NewPage(32, 16).DrawImage(img, 0, 0, 32, 16)
	data, err := c.Build()
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	doc, _ := OpenBytes(data)
	res, _ := doc.reader.ResolveDict(doc.pages[0]["Resources"])
	xobj, _ := doc.reader.ResolveDict(res["XObject"])
	stream, _ := doc.reader.Resolve(xobj["Im1"]).(*Stream)
	if stream.Dict["ColorSpace"] != Name("DeviceGray") {
		t.Errorf("ColorSpace = %v, want DeviceGray", stream.Dict["ColorSpace"])
	}
}

func TestRecognizedFormat(t *testing.T) {
	heic := append([]byte{0, 0, 0, 0x18}, "ftypheic"...)
	avif := append([]byte{0, 0, 0, 0x18}, "ftypavif"...)
	mp4 := append([]byte{0, 0, 0, 0x18}, "ftypisom"...)
	webp := append([]byte("RIFF\x00\x00\x00\x00"), "WEBP"...)

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"heic", heic, "HEIC"},
		{"avif", avif, "AVIF"},
		{"other ISO media", mp4, ""},
		{"webp", webp, "WebP"},
		{"tiff little-endian", []byte("II*\x00rest"), "TIFF"},
		{"tiff big-endian", []byte("MM\x00*rest"), "TIFF"},
		{"gif", []byte("GIF89a...."), "GIF"},
		{"bmp", []byte("BM......"), "BMP"},
		{"png", testPNG(t, false), ""},
		{"short", []byte("ab"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := recognizedFormat(tt.data); got != tt.want {
				t.Errorf("recognizedFormat = %q, want %q", got, tt.want)
			}
		})
	}

	// The name reaches the caller as a typed error it can act on, rather
	// than as "unknown format".
	_, err := LoadImageBytes(heic)
	var unsupported *UnsupportedFormatError
	if !errors.As(err, &unsupported) {
		t.Fatalf("LoadImageBytes error = %v, want an *UnsupportedFormatError", err)
	}
	if unsupported.Format != "HEIC" {
		t.Errorf("Format = %q, want HEIC", unsupported.Format)
	}
	if !strings.Contains(err.Error(), "HEIC is not supported") {
		t.Errorf("message = %q, want it to name HEIC", err)
	}

	// A format it cannot name still fails, just without the typed error.
	if _, err := LoadImageBytes([]byte("no image here at all")); err == nil {
		t.Error("garbage decoded without error")
	} else if errors.As(err, &unsupported) {
		t.Errorf("unrecognized bytes reported as format %q", unsupported.Format)
	}
}

// jpegWithOrientation wraps a JPEG in an APP1 EXIF segment declaring the
// given orientation, the way a phone camera records how it was held.
func jpegWithOrientation(t *testing.T, src []byte, orientation int) []byte {
	t.Helper()
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8} // big-endian, IFD0 at offset 8
	tiff = append(tiff, 0, 1)                   // one entry
	tiff = append(tiff, 0x01, 0x12)             // tag: Orientation
	tiff = append(tiff, 0, 3)                   // type: SHORT
	tiff = append(tiff, 0, 0, 0, 1)             // count: 1
	tiff = append(tiff, byte(orientation>>8), byte(orientation), 0, 0)
	tiff = append(tiff, 0, 0, 0, 0) // no next IFD

	payload := append([]byte("Exif\x00\x00"), tiff...)
	segment := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	segment = append(segment, payload...)

	out := append([]byte{}, src[:2]...) // SOI
	out = append(out, segment...)
	return append(out, src[2:]...)
}

func TestEXIFOrientation(t *testing.T) {
	src := colorJPEG(t, 40, 20)

	for want := 1; want <= 8; want++ {
		img, err := LoadImageBytes(jpegWithOrientation(t, src, want))
		if err != nil {
			t.Fatalf("orientation %d: %v", want, err)
		}
		if img.Orientation != want {
			t.Errorf("Orientation = %d, want %d", img.Orientation, want)
		}

		// A quarter turn swaps the dimensions the image displays at, while
		// the stored frame stays as it is.
		wantW, wantH := 40, 20
		if want >= 5 {
			wantW, wantH = 20, 40
		}
		if w, h := img.DisplaySize(); w != wantW || h != wantH {
			t.Errorf("orientation %d: DisplaySize = %d×%d, want %d×%d", want, w, h, wantW, wantH)
		}
		if img.Width != 40 || img.Height != 20 {
			t.Errorf("orientation %d: stored size changed to %d×%d", want, img.Width, img.Height)
		}
	}

	plain, err := LoadImageBytes(src)
	if err != nil {
		t.Fatalf("loading JPEG without EXIF: %v", err)
	}
	if plain.Orientation != 1 {
		t.Errorf("Orientation without EXIF = %d, want 1", plain.Orientation)
	}
	if _, err := LoadImageBytes(jpegWithOrientation(t, src, 99)); err != nil {
		t.Fatalf("out-of-range orientation should be ignored, not fail: %v", err)
	}
}

// TestOrientationMatricesMapTheUnitSquare checks each matrix against the
// EXIF definition: where the stored image's top-left corner has to land.
func TestOrientationMatricesMapTheUnitSquare(t *testing.T) {
	// In PDF image space the stored image's top-left corner is (0, 1).
	tests := []struct {
		orientation  int
		name         string
		wantX, wantY float64
		alsoX, alsoY float64 // where the stored top-right corner (1, 1) lands
	}{
		{1, "as stored", 0, 1, 1, 1},
		{2, "mirrored left to right", 1, 1, 0, 1},
		{3, "half turn", 1, 0, 0, 0},
		{4, "mirrored top to bottom", 0, 0, 1, 0},
		{5, "transposed", 1, 0, 1, 1},
		{6, "quarter turn clockwise", 1, 1, 1, 0},
		{7, "transverse", 0, 1, 0, 0},
		{8, "quarter turn counter-clockwise", 0, 0, 0, 1},
	}
	apply := func(m [6]float64, x, y float64) (float64, float64) {
		return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := (&Image{Orientation: tt.orientation}).orientationMatrix()
			if x, y := apply(m, 0, 1); x != tt.wantX || y != tt.wantY {
				t.Errorf("top-left → (%v, %v), want (%v, %v)", x, y, tt.wantX, tt.wantY)
			}
			if x, y := apply(m, 1, 1); x != tt.alsoX || y != tt.alsoY {
				t.Errorf("top-right → (%v, %v), want (%v, %v)", x, y, tt.alsoX, tt.alsoY)
			}
		})
	}
}

// TestSidewaysPhotoFillsItsBoxUpright is the end-to-end case: a landscape
// frame that declares "quarter turn clockwise" must produce a portrait page
// with a matrix that turns it, not a landscape page with the photo on edge.
func TestSidewaysPhotoFillsItsBoxUpright(t *testing.T) {
	img, err := LoadImageBytes(jpegWithOrientation(t, colorJPEG(t, 400, 200), 6))
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	dw, dh := img.DisplaySize()
	if dw != 200 || dh != 400 {
		t.Fatalf("DisplaySize = %d×%d, want 200×400", dw, dh)
	}

	c := NewCreator()
	c.NewPage(float64(dw), float64(dh)).DrawImage(img, 0, 0, float64(dw), float64(dh))
	data, err := c.Build()
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	r, _ := Open(data)
	pages, _ := r.Pages()
	content, err := r.PageContent(pages[0])
	if err != nil {
		t.Fatalf("reading content: %v", err)
	}
	// Orientation 6 turns the unit square, so the matrix is off-diagonal.
	if want := "0.0000 -400.0000 200.0000 0.0000 0.0000 400.0000 cm"; !strings.Contains(string(content), want) {
		t.Errorf("content lacks %q:\n%s", want, content)
	}
}

// jpegWithResolution wraps a JPEG in an APP1 EXIF segment declaring
// XResolution and ResolutionUnit (2 inches, 3 centimetres).
func jpegWithResolution(t *testing.T, src []byte, resolution, unit int) []byte {
	t.Helper()
	const rationalAt = 38 // past the header, the two entries, and the next-IFD link

	be := func(v, n int) []byte {
		b := make([]byte, n)
		for i := n - 1; i >= 0; i-- {
			b[i] = byte(v)
			v >>= 8
		}
		return b
	}
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8}
	tiff = append(tiff, be(2, 2)...) // two entries

	tiff = append(tiff, be(0x011A, 2)...)     // XResolution
	tiff = append(tiff, be(5, 2)...)          // RATIONAL
	tiff = append(tiff, be(1, 4)...)          // count
	tiff = append(tiff, be(rationalAt, 4)...) // where the pair lives

	tiff = append(tiff, be(0x0128, 2)...) // ResolutionUnit
	tiff = append(tiff, be(3, 2)...)      // SHORT
	tiff = append(tiff, be(1, 4)...)      // count
	tiff = append(tiff, be(unit, 2)...)
	tiff = append(tiff, 0, 0) // SHORT padded to four bytes

	tiff = append(tiff, be(0, 4)...) // no next IFD
	tiff = append(tiff, be(resolution, 4)...)
	tiff = append(tiff, be(1, 4)...) // denominator

	payload := append([]byte("Exif\x00\x00"), tiff...)
	segment := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	segment = append(segment, payload...)

	out := append([]byte{}, src[:2]...)
	out = append(out, segment...)
	return append(out, src[2:]...)
}

// jpegWithJFIFDensity prepends an APP0 segment. Unit 0 means the numbers are
// an aspect ratio and carry no physical size; 1 is per inch, 2 per centimetre.
func jpegWithJFIFDensity(t *testing.T, src []byte, density, unit int) []byte {
	t.Helper()
	seg := []byte{0xFF, 0xE0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 2, byte(unit),
		byte(density >> 8), byte(density), byte(density >> 8), byte(density), 0, 0}
	out := append([]byte{}, src[:2]...)
	out = append(out, seg...)
	return append(out, src[2:]...)
}

func pngWithDensity(t *testing.T, pixelsPerMetre uint32, unit byte) []byte {
	t.Helper()
	src := testPNG(t, false)
	chunk := func(kind string, body []byte) []byte {
		out := make([]byte, 0, len(body)+12)
		out = binary.BigEndian.AppendUint32(out, uint32(len(body)))
		out = append(out, kind...)
		out = append(out, body...)
		return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(kind), body...)))
	}
	body := make([]byte, 0, 9)
	body = binary.BigEndian.AppendUint32(body, pixelsPerMetre)
	body = binary.BigEndian.AppendUint32(body, pixelsPerMetre)
	body = append(body, unit)

	// The signature and IHDR come first; pHYs must precede IDAT.
	const afterIHDR = 8 + 25
	out := append([]byte{}, src[:afterIHDR]...)
	out = append(out, chunk("pHYs", body)...)
	return append(out, src[afterIHDR:]...)
}

func TestDeclaredDPI(t *testing.T) {
	jpg := colorJPEG(t, 40, 20)

	tests := []struct {
		name string
		data []byte
		want float64
	}{
		{"EXIF, inches", jpegWithResolution(t, jpg, 300, 2), 300},
		{"EXIF, centimetres", jpegWithResolution(t, jpg, 100, 3), 254},
		{"JFIF, per inch", jpegWithJFIFDensity(t, jpg, 150, 1), 150},
		{"JFIF, per centimetre", jpegWithJFIFDensity(t, jpg, 100, 2), 254},
		{"JFIF, aspect ratio only", jpegWithJFIFDensity(t, jpg, 1, 0), 0},
		{"JPEG declaring nothing", jpg, 0},
		{"PNG pHYs, metres", pngWithDensity(t, 11811, 1), 299.9994},
		{"PNG pHYs, unknown unit", pngWithDensity(t, 11811, 0), 0},
		{"PNG declaring nothing", testPNG(t, false), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img, err := LoadImageBytes(tt.data)
			if err != nil {
				t.Fatalf("loading: %v", err)
			}
			if math.Abs(img.DPI-tt.want) > 0.001 {
				t.Errorf("DPI = %v, want %v", img.DPI, tt.want)
			}
		})
	}

	// EXIF is the more considered claim where a file makes both.
	both := jpegWithResolution(t, jpegWithJFIFDensity(t, jpg, 150, 1), 300, 2)
	img, err := LoadImageBytes(both)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if img.DPI != 300 {
		t.Errorf("DPI = %v with both JFIF 150 and EXIF 300, want 300", img.DPI)
	}
}
