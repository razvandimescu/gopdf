package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/razvandimescu/gopdf/pdf"
)

func TestSplitList(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{"Date", []string{"Date"}},
		{"Date, Amount ,Balance", []string{"Date", "Amount", "Balance"}},
		{"Date,,Amount", []string{"Date", "Amount"}},
		{",", nil},
	}
	for _, tt := range tests {
		if got := splitList(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("splitList(%q) = %#v, want %#v", tt.in, got, tt.want)
		}
	}
}

func TestTableOpts(t *testing.T) {
	// Row grouping is auto-tuned only when neither distance is pinned.
	if o := tableOpts("", "", "", "", 0, 0); !o.AutoTune {
		t.Error("AutoTune should be on when no gap is given")
	}
	if o := tableOpts("", "", "", "", 4, 0); o.AutoTune {
		t.Error("AutoTune should be off once -merge-gap is given")
	}
	if o := tableOpts("", "", "", "", 0, 12); o.AutoTune {
		t.Error("AutoTune should be off once -max-row-gap is given")
	}

	opts := tableOpts(" Date , Amount ", "page ,CONTINUED", "Amount,Balance", "Date", 0, 0)
	if want := []string{"Date", "Amount"}; !reflect.DeepEqual(opts.Headers, want) {
		t.Errorf("Headers = %#v, want %#v", opts.Headers, want)
	}
	if want := []string{"Amount", "Balance"}; !reflect.DeepEqual(opts.RequireAnyColumn, want) {
		t.Errorf("RequireAnyColumn = %#v, want %#v", opts.RequireAnyColumn, want)
	}
	if opts.AnchorColumn != "Date" {
		t.Errorf("AnchorColumn = %q, want Date", opts.AnchorColumn)
	}

	// The filter is case-insensitive in both directions and drops the row on
	// a match in any cell.
	if opts.RowFilter([]string{"01-04", "Page 2 of 9"}) {
		t.Error("row containing a filtered substring was kept")
	}
	if opts.RowFilter([]string{"01-04", "continued overleaf"}) {
		t.Error("filter should ignore case")
	}
	if !opts.RowFilter([]string{"01-04", "Ordinary transaction"}) {
		t.Error("row matching nothing was dropped")
	}
	if tableOpts("", "", "", "", 0, 0).RowFilter != nil {
		t.Error("RowFilter should be nil when -filter is empty")
	}
}

func testTable() *pdf.Table {
	return &pdf.Table{
		Columns: []pdf.Column{{Name: "Date"}, {Name: "Detail"}, {Name: "Amount"}},
		Rows: []pdf.Row{
			{Cells: []pdf.Cell{{Text: "01-04"}, {Text: "Coffee, black"}, {Text: "4.20"}}},
			{Cells: []pdf.Cell{{Text: "02-04"}, {Text: "Line one\nline two"}, {Text: "1.000,00"}}},
			{Cells: []pdf.Cell{{Text: "03-04"}}}, // short row: trailing cells missing
		},
	}
}

func TestWriteCSV(t *testing.T) {
	var buf bytes.Buffer
	if err := writeCSV(&buf, testTable()); err != nil {
		t.Fatalf("writeCSV: %v", err)
	}
	got := buf.String()

	for _, want := range []string{
		"Date,Detail,Amount\n",
		`"Coffee, black"`,        // a comma inside a cell must be quoted
		"\"Line one\nline two\"", // so must an embedded newline
		"03-04,,\n",              // a short row still fills every column
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CSV lacks %q:\n%s", want, got)
		}
	}
}

func TestWriteText(t *testing.T) {
	var buf bytes.Buffer
	writeText(&buf, testTable(), 12)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")

	if !strings.HasPrefix(lines[0], "Date        ") {
		t.Errorf("header not padded to the column width: %q", lines[0])
	}
	if strings.Trim(lines[1], "-") != "" {
		t.Errorf("second line should be the rule, got %q", lines[1])
	}
	if last := lines[len(lines)-1]; last != "(3 rows)" {
		t.Errorf("footer = %q, want (3 rows)", last)
	}
	// Figures are right-aligned, so a numeric cell ends at the column edge.
	if !strings.HasSuffix(lines[2], "        4.20") {
		t.Errorf("numeric cell not right-aligned: %q", lines[2])
	}
}

func TestIsNumeric(t *testing.T) {
	for _, s := range []string{"4.20", "1,000", "1.000,00", " 42 ", "0"} {
		if !isNumeric(s) {
			t.Errorf("isNumeric(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "  ", "Coffee", "4.20 EUR", "-", "."} {
		if isNumeric(s) {
			t.Errorf("isNumeric(%q) = true, want false", s)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly-10", 10, "exactly-10"},
		{"far too long to fit", 10, "far too..."},
		{"abc", 2, "abc"}, // no room for an ellipsis; leave it be
	}
	for _, tt := range tests {
		if got := truncate(tt.s, tt.n); got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}
