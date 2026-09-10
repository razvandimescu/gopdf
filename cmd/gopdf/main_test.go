package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/razvandimescu/gopdf/pdf"
)

func testPNGBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: 10, G: 200, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding test PNG: %v", err)
	}
	return buf.Bytes()
}

func TestIsPDF(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"header at byte 0", []byte("%PDF-1.7\n..."), true},
		{"header behind a transport envelope", append(bytes.Repeat([]byte("x"), 300), "%PDF-1.4"...), true},
		{"header past the 1024-byte window", append(bytes.Repeat([]byte("x"), 1100), "%PDF-1.4"...), false},
		{"png", []byte("\x89PNG\r\n\x1a\n"), false},
		{"empty", nil, false},
		{"shorter than the window", []byte("hi"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPDF(tt.data); got != tt.want {
				t.Errorf("isPDF = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseLayoutRejectsBadFlags(t *testing.T) {
	tests := []struct {
		name                string
		page                string
		dpi, margin         float64
		wantErrorContaining string
	}{
		{"unknown page size", "a3", 72, 18, `unknown -page "a3"`},
		{"negative dpi", "image", -1, 18, "-dpi must not be negative"},
		{"negative margin", "a4", 72, -1, "-margin must not be negative"},
		{"margin swallows the page", "a4", 72, 300, "leaves no room"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseLayout(tt.page, tt.dpi, tt.margin)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrorContaining) {
				t.Errorf("error = %v, want one containing %q", err, tt.wantErrorContaining)
			}
		})
	}
	if _, err := parseLayout("LETTER", 72, 18); err != nil {
		t.Errorf("page names should be case-insensitive: %v", err)
	}
}

func TestImageToPDFPageSizing(t *testing.T) {
	data := testPNGBytes(t, 800, 400) // 2:1

	tests := []struct {
		name                    string
		page                    string
		dpi, margin             float64
		wantPageW, wantPageH    float64
		wantImgW, wantImgH      float64
		wantOriginX, wantOrigin float64
	}{
		// -page image at 144 dpi halves the pixel count into points.
		{"image size", "image", 144, 0, 400, 200, 400, 200, 0, 0},
		{"image size with margin", "image", 144, 10, 420, 220, 400, 200, 10, 10},
		// A4 is 595 wide; a 2:1 image inside an 18pt margin is width-bound:
		// 595-36 = 559 wide, 279.5 tall, centered vertically.
		{"a4 fit", "a4", 72, 18, 595, 842, 559, 279.5, 18, 281.25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := parseLayout(tt.page, tt.dpi, tt.margin)
			if err != nil {
				t.Fatalf("parseLayout: %v", err)
			}
			out, err := l.imageToPDF(data)
			if err != nil {
				t.Fatalf("imageToPDF: %v", err)
			}
			doc, err := pdf.OpenBytes(out)
			if err != nil {
				t.Fatalf("opening result: %v", err)
			}
			if doc.NumPages() != 1 {
				t.Fatalf("pages: got %d, want 1", doc.NumPages())
			}
			box := doc.Page(0).MediaBox()
			if box != [4]float64{0, 0, tt.wantPageW, tt.wantPageH} {
				t.Errorf("MediaBox = %v, want [0 0 %v %v]", box, tt.wantPageW, tt.wantPageH)
			}

			// The content stream places the image: "w 0 0 h x y cm".
			r, err := pdf.Open(out)
			if err != nil {
				t.Fatalf("reading result: %v", err)
			}
			pages, err := r.Pages()
			if err != nil {
				t.Fatalf("reading pages: %v", err)
			}
			content, err := r.PageContent(pages[0])
			if err != nil {
				t.Fatalf("reading content: %v", err)
			}
			want := fmt.Sprintf("%.4f 0.0000 0.0000 %.4f %.4f %.4f cm",
				tt.wantImgW, tt.wantImgH, tt.wantOriginX, tt.wantOrigin)
			if !strings.Contains(string(content), want) {
				t.Errorf("content stream lacks %q:\n%s", want, content)
			}
		})
	}
}

func TestImageToPDFRejectsNonImage(t *testing.T) {
	l, _ := parseLayout("a4", 72, 18)
	_, err := l.imageToPDF([]byte("this is not an image"))
	if err == nil || !strings.Contains(err.Error(), "not a PDF") {
		t.Errorf("error = %v, want one explaining the input is neither PDF nor image", err)
	}
}

func TestConversionHint(t *testing.T) {
	heic := append([]byte{0, 0, 0, 0x18}, "ftypheic"...)
	l, _ := parseLayout("a4", 72, 18)
	_, err := l.imageToPDF(heic)
	if err == nil {
		t.Fatal("HEIC decoded without error")
	}
	if got := err.Error(); got != "HEIC is not supported (PNG, JPEG and GIF only)" {
		t.Errorf("error = %q; an unsupported format should not also be called a non-PDF", got)
	}

	hint := conversionHint("/pics/IMG_6407.HEIC", err)
	if !strings.Contains(hint, "/pics/IMG_6407.jpg") {
		t.Errorf("hint = %q, want it to name the converted file", hint)
	}
	wantTool := "magick"
	if runtime.GOOS == "darwin" {
		wantTool = "sips -s format jpeg"
	}
	if !strings.Contains(hint, wantTool) {
		t.Errorf("hint = %q, want it to suggest %q on %s", hint, wantTool, runtime.GOOS)
	}

	// Every other failure is left to speak for itself.
	if got := conversionHint("x.pdf", errors.New("some other problem")); got != "" {
		t.Errorf("hint for an unrelated error = %q, want none", got)
	}
}

func TestResolutionPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		flag     float64 // -dpi
		declared float64 // what the file says
		want     float64
	}{
		{"an explicit -dpi wins", 150, 300, 150},
		{"otherwise the file decides", 0, 300, 300},
		{"and 72 when nothing does", 0, 0, 72},
		{"an explicit -dpi overrides a camera's meaningless 72", 300, 72, 300},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := parseLayout("image", tt.flag, 0)
			if err != nil {
				t.Fatalf("parseLayout: %v", err)
			}
			if got := l.resolution(&pdf.Image{DPI: tt.declared}); got != tt.want {
				t.Errorf("resolution = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPageFollowsDeclaredResolution is the point of reading the DPI at all:
// a scan that knows it is 300 dpi gets its true physical size, where the old
// flat 72 would have made a 600-pixel square into an 8-inch page.
func TestPageFollowsDeclaredResolution(t *testing.T) {
	l, err := parseLayout("image", 0, 0)
	if err != nil {
		t.Fatalf("parseLayout: %v", err)
	}
	out, err := l.imageToPDF(pngAt300DPI(t))
	if err != nil {
		t.Fatalf("imageToPDF: %v", err)
	}
	doc, err := pdf.OpenBytes(out)
	if err != nil {
		t.Fatalf("opening result: %v", err)
	}
	// 600 pixels at 300 dpi is two inches, which is 144 points.
	box := doc.Page(0).MediaBox()
	if math.Abs(box[2]-144) > 0.01 || math.Abs(box[3]-144) > 0.01 {
		t.Errorf("MediaBox = %v, want [0 0 144 144]", box)
	}
}

// pngAt300DPI builds a 600x600 PNG whose pHYs chunk declares 300 dpi.
func pngAt300DPI(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 600, 600))
	for y := range 600 {
		for x := range 600 {
			img.Set(x, y, color.RGBA{R: 90, G: 140, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding: %v", err)
	}
	src := buf.Bytes()

	body := make([]byte, 0, 9)
	perMetre := uint32(math.Round(300 / 0.0254))
	body = binary.BigEndian.AppendUint32(body, perMetre)
	body = binary.BigEndian.AppendUint32(body, perMetre)
	body = append(body, 1) // unit specifier: the metre

	chunk := make([]byte, 0, len(body)+12)
	chunk = binary.BigEndian.AppendUint32(chunk, uint32(len(body)))
	chunk = append(chunk, "pHYs"...)
	chunk = append(chunk, body...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(append([]byte("pHYs"), body...)))

	const afterIHDR = 8 + 25
	out := append([]byte{}, src[:afterIHDR]...)
	out = append(out, chunk...)
	return append(out, src[afterIHDR:]...)
}
