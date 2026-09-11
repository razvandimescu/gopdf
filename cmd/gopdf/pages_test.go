package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/razvandimescu/gopdf/pdf"
)

func TestParsePages(t *testing.T) {
	tests := []struct {
		spec string
		want []int // 0-based, as the library takes them
	}{
		{"1", []int{0}},
		{"1-2", []int{0, 1}},
		{"5-", []int{4, 5}},
		{"1,3,5-6", []int{0, 2, 4, 5}},
		{"6", []int{5}},
		{"1-6", []int{0, 1, 2, 3, 4, 5}},
		{"3,1", []int{2, 0}}, // order follows the range, so this reverses
		{" 1 , 3 ", []int{0, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			got, err := parsePages(tt.spec, 6)
			if err != nil {
				t.Fatalf("parsePages(%q): %v", tt.spec, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("parsePages(%q) = %v, want %v", tt.spec, got, tt.want)
			}
		})
	}
}

func TestParsePagesRejectsBadRanges(t *testing.T) {
	tests := []struct {
		name                string
		spec                string
		wantErrorContaining string
	}{
		{"gibberish", "abc", `bad range "abc"`},
		{"double dash", "1--2", `bad range "1--2"`},
		{"empty", "", `bad range ""`},
		{"empty item", "1,,2", `bad range "1,,2"`},
		// 0 would reach the library as -1, which counts from the end and
		// would silently hand back the last page.
		{"page zero", "0", "pages are numbered from 1"},
		{"zero in a range", "0-2", "pages are numbered from 1"},
		{"past the end", "7", "page 7 is out of range; the document has 6 pages"},
		{"range past the end", "5-9", "page 9 is out of range"},
		{"open range past the end", "9-", "page 9 is out of range"},
		{"backwards", "3-1", "3-1 counts backwards; write 1-3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parsePages(tt.spec, 6)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrorContaining) {
				t.Errorf("error = %v, want one containing %q", err, tt.wantErrorContaining)
			}
		})
	}
}

func TestPagesCommand(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.pdf")
	out := filepath.Join(dir, "out.pdf")
	if err := os.WriteFile(in, numberedPDF(t, 4), 0644); err != nil {
		t.Fatalf("writing input: %v", err)
	}

	if err := runPages([]string{"3,1", in, "-o", out}); err != nil {
		t.Fatalf("runPages: %v", err)
	}

	doc, err := pdf.OpenFile(out)
	if err != nil {
		t.Fatalf("opening result: %v", err)
	}
	if doc.NumPages() != 2 {
		t.Fatalf("pages: got %d, want 2", doc.NumPages())
	}
	for i, want := range []string{"page 3", "page 1"} {
		text, err := doc.Page(i).Text()
		if err != nil {
			t.Fatalf("reading page %d: %v", i, err)
		}
		if !strings.Contains(text, want) {
			t.Errorf("page %d holds %q, want %q", i, text, want)
		}
	}
}

// TestPagesCommandCatchesArgumentOrder: the range comes first, and a user who
// writes the file first should be told so rather than shown a range error.
func TestPagesCommandCatchesArgumentOrder(t *testing.T) {
	in := filepath.Join(t.TempDir(), "in.pdf")
	if err := os.WriteFile(in, numberedPDF(t, 2), 0644); err != nil {
		t.Fatalf("writing input: %v", err)
	}
	err := runPages([]string{in, "1-2"})
	if err == nil || !strings.Contains(err.Error(), "put the page range first") {
		t.Errorf("error = %v, want one naming the argument order", err)
	}
}

func numberedPDF(t *testing.T, pages int) []byte {
	t.Helper()
	c := pdf.NewCreator()
	for i := range pages {
		pb := c.NewPage(200, 200)
		pb.SetFont("Helvetica", 12)
		pb.DrawText(20, 100, "page "+string(rune('1'+i)))
	}
	data, err := c.Build()
	if err != nil {
		t.Fatalf("building test PDF: %v", err)
	}
	return data
}
