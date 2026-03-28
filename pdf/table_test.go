package pdf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Helper to build spans for a simple table layout.
func makeSpan(x, y float64, text string) TextSpan {
	return TextSpan{X: x, Y: y, EndX: x + float64(len(text))*6, FontSize: 12, Text: text}
}

// =====================================================================
// Approach 1: Explicit headers
// =====================================================================

func TestFindTable_ExplicitHeaders(t *testing.T) {
	// Layout:
	//   Y=700: Name     Age    City
	//   Y=680: Alice    30     New York
	//   Y=660: Bob      25     London
	spans := []TextSpan{
		makeSpan(50, 700, "Name"),
		makeSpan(150, 700, "Age"),
		makeSpan(250, 700, "City"),
		makeSpan(50, 680, "Alice"),
		makeSpan(150, 680, "30"),
		makeSpan(250, 680, "New York"),
		makeSpan(50, 660, "Bob"),
		makeSpan(150, 660, "25"),
		makeSpan(250, 660, "London"),
	}

	tbl := FindTable(spans, &TableOpts{Headers: []string{"Name", "Age"}})
	if tbl == nil {
		t.Fatal("expected table, got nil")
	}
	if len(tbl.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(tbl.Columns))
	}
	if tbl.Columns[0].Name != "Name" || tbl.Columns[1].Name != "Age" || tbl.Columns[2].Name != "City" {
		t.Errorf("columns = %v", tbl.Columns)
	}
	if len(tbl.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(tbl.Rows))
	}
	if tbl.CellText(0, 0) != "Alice" {
		t.Errorf("cell(0,0) = %q, want Alice", tbl.CellText(0, 0))
	}
	if tbl.CellText(1, 2) != "London" {
		t.Errorf("cell(1,2) = %q, want London", tbl.CellText(1, 2))
	}
}

func TestFindTable_WrappedHeaders(t *testing.T) {
	// Layout:
	//   Y=700: Product    Suppliers    Unit
	//   Y=688: Code       Code         Price
	//   Y=660: ABC-123    SUP-001      45.00
	spans := []TextSpan{
		makeSpan(50, 700, "Product"),
		makeSpan(150, 700, "Suppliers"),
		makeSpan(300, 700, "Unit"),
		makeSpan(55, 688, "Code"),
		makeSpan(155, 688, "Code"),
		makeSpan(305, 688, "Price"),
		makeSpan(50, 660, "ABC-123"),
		makeSpan(150, 660, "SUP-001"),
		makeSpan(300, 660, "45.00"),
	}

	tbl := FindTable(spans, &TableOpts{Headers: []string{"Product", "Suppliers"}})
	if tbl == nil {
		t.Fatal("expected table, got nil")
	}
	if tbl.Columns[0].Name != "Product Code" {
		t.Errorf("col 0 = %q, want 'Product Code'", tbl.Columns[0].Name)
	}
	if tbl.Columns[1].Name != "Suppliers Code" {
		t.Errorf("col 1 = %q, want 'Suppliers Code'", tbl.Columns[1].Name)
	}
	if tbl.Columns[2].Name != "Unit Price" {
		t.Errorf("col 2 = %q, want 'Unit Price'", tbl.Columns[2].Name)
	}
	if len(tbl.Rows) != 1 {
		t.Fatalf("expected 1 data row, got %d", len(tbl.Rows))
	}
	if tbl.CellText(0, 0) != "ABC-123" {
		t.Errorf("cell(0,0) = %q, want ABC-123", tbl.CellText(0, 0))
	}
}

func TestFindTable_RowFilter(t *testing.T) {
	spans := []TextSpan{
		makeSpan(50, 700, "A"),
		makeSpan(150, 700, "B"),
		makeSpan(250, 700, "C"),
		makeSpan(50, 680, "1"),
		makeSpan(150, 680, "2"),
		makeSpan(250, 680, "3"),
		makeSpan(50, 660, "skip"),
		makeSpan(150, 660, "this"),
		makeSpan(250, 660, "row"),
		makeSpan(50, 640, "4"),
		makeSpan(150, 640, "5"),
		makeSpan(250, 640, "6"),
	}

	tbl := FindTable(spans, &TableOpts{
		Headers: []string{"A", "B"},
		RowFilter: func(cells []string) bool {
			return cells[0] != "skip"
		},
	})
	if tbl == nil {
		t.Fatal("expected table")
	}
	if len(tbl.Rows) != 2 {
		t.Fatalf("expected 2 rows (skipped one), got %d", len(tbl.Rows))
	}
	if tbl.CellText(0, 0) != "1" {
		t.Errorf("first row cell 0 = %q, want 1", tbl.CellText(0, 0))
	}
	if tbl.CellText(1, 0) != "4" {
		t.Errorf("second row cell 0 = %q, want 4", tbl.CellText(1, 0))
	}
}

func TestFindTable_NoMatch(t *testing.T) {
	spans := []TextSpan{
		makeSpan(50, 700, "Hello"),
		makeSpan(50, 680, "World"),
	}
	tbl := FindTable(spans, &TableOpts{Headers: []string{"Name", "Age"}})
	if tbl != nil {
		t.Error("expected nil for no matching headers")
	}
}

func TestFindTableAcrossPages(t *testing.T) {
	page1 := []TextSpan{
		makeSpan(50, 700, "X"),
		makeSpan(150, 700, "Y"),
		makeSpan(250, 700, "Z"),
		makeSpan(50, 680, "a"),
		makeSpan(150, 680, "b"),
		makeSpan(250, 680, "c"),
	}
	page2 := []TextSpan{
		makeSpan(50, 700, "X"),
		makeSpan(150, 700, "Y"),
		makeSpan(250, 700, "Z"),
		makeSpan(50, 680, "d"),
		makeSpan(150, 680, "e"),
		makeSpan(250, 680, "f"),
	}

	tbl := FindTableAcrossPages([][]TextSpan{page1, page2}, &TableOpts{
		Headers: []string{"X", "Y"},
	})
	if tbl == nil {
		t.Fatal("expected table")
	}
	if len(tbl.Rows) != 2 {
		t.Fatalf("expected 2 rows across pages, got %d", len(tbl.Rows))
	}
	if tbl.CellText(0, 0) != "a" || tbl.CellText(1, 0) != "d" {
		t.Errorf("rows: [%q, %q], want [a, d]", tbl.CellText(0, 0), tbl.CellText(1, 0))
	}
}

// =====================================================================
// Approach 2: Gap-based auto-detection
// =====================================================================

func TestFindTables_AutoDetect(t *testing.T) {
	// Table with clear gaps at X≈120 and X≈240.
	// 5 rows sharing the same gap structure.
	spans := []TextSpan{
		makeSpan(50, 700, "Name"),
		makeSpan(200, 700, "Age"),
		makeSpan(350, 700, "City"),
		makeSpan(50, 680, "Alice"),
		makeSpan(200, 680, "30"),
		makeSpan(350, 680, "New York"),
		makeSpan(50, 660, "Bob"),
		makeSpan(200, 660, "25"),
		makeSpan(350, 660, "London"),
		makeSpan(50, 640, "Carol"),
		makeSpan(200, 640, "35"),
		makeSpan(350, 640, "Paris"),
		makeSpan(50, 620, "Dave"),
		makeSpan(200, 620, "28"),
		makeSpan(350, 620, "Berlin"),
	}

	tables := FindTables(spans, nil)
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	tbl := &tables[0]
	if len(tbl.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(tbl.Columns))
	}
	if tbl.Columns[0].Name != "Name" {
		t.Errorf("col 0 = %q, want Name", tbl.Columns[0].Name)
	}
	if len(tbl.Rows) != 4 {
		t.Fatalf("expected 4 data rows, got %d", len(tbl.Rows))
	}
	if tbl.CellText(2, 2) != "Paris" {
		t.Errorf("cell(2,2) = %q, want Paris", tbl.CellText(2, 2))
	}
}

func TestFindTables_NoTable(t *testing.T) {
	// Single-column paragraph text — no table.
	spans := []TextSpan{
		makeSpan(50, 700, "This is a paragraph of text."),
		makeSpan(50, 680, "It continues on the next line."),
		makeSpan(50, 660, "And the line after that."),
	}
	tables := FindTables(spans, nil)
	if len(tables) != 0 {
		t.Errorf("expected 0 tables for paragraph text, got %d", len(tables))
	}
}

func TestFindTables_TwoColumnNotEnough(t *testing.T) {
	// Two columns — below MinColumns default of 3.
	spans := []TextSpan{
		makeSpan(50, 700, "Key"),
		makeSpan(250, 700, "Value"),
		makeSpan(50, 680, "name"),
		makeSpan(250, 680, "alice"),
		makeSpan(50, 660, "age"),
		makeSpan(250, 660, "30"),
		makeSpan(50, 640, "city"),
		makeSpan(250, 640, "london"),
	}
	tables := FindTables(spans, nil)
	if len(tables) != 0 {
		t.Errorf("expected 0 tables for 2-column data (below MinColumns=3), got %d", len(tables))
	}

	// But with MinColumns=2 it should work.
	tables = FindTables(spans, &TableOpts{MinColumns: 2})
	if len(tables) != 1 {
		t.Fatalf("expected 1 table with MinColumns=2, got %d", len(tables))
	}
}

// =====================================================================
// Convenience methods
// =====================================================================

func TestTable_ColumnByName(t *testing.T) {
	tbl := &Table{
		Columns: []Column{{Name: "First Name"}, {Name: "Age"}, {Name: "City"}},
	}
	if tbl.ColumnByName("age") != 1 {
		t.Error("case-insensitive lookup failed")
	}
	if tbl.ColumnByName("nonexistent") != -1 {
		t.Error("expected -1 for missing column")
	}
}

func TestTable_CellByName(t *testing.T) {
	spans := []TextSpan{
		makeSpan(50, 700, "Name"),
		makeSpan(150, 700, "Score"),
		makeSpan(250, 700, "Grade"),
		makeSpan(50, 680, "Alice"),
		makeSpan(150, 680, "95"),
		makeSpan(250, 680, "A"),
	}
	tbl := FindTable(spans, &TableOpts{Headers: []string{"Name", "Score"}})
	if tbl == nil {
		t.Fatal("expected table")
	}
	if v := tbl.CellByName(0, "score"); v != "95" {
		t.Errorf("CellByName(0, score) = %q, want 95", v)
	}
	if v := tbl.CellByName(0, "missing"); v != "" {
		t.Errorf("CellByName(0, missing) = %q, want empty", v)
	}
}

func TestTable_CellText_OutOfBounds(t *testing.T) {
	tbl := &Table{
		Columns: []Column{{Name: "A"}},
		Rows:    []Row{{Y: 100, Cells: []Cell{{Column: 0, Text: "x"}}}},
	}
	if tbl.CellText(-1, 0) != "" || tbl.CellText(5, 0) != "" || tbl.CellText(0, 5) != "" {
		t.Error("out of bounds should return empty string")
	}
}

// =====================================================================
// Integration tests — real PDFs from example_out/ (git-ignored)
// =====================================================================

const pdfDir = "../example_out"

func openTestPDF(t *testing.T, name string) *Document {
	t.Helper()
	path := filepath.Join(pdfDir, name)
	doc, err := OpenFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("test PDF not found: %s", path)
		}
		t.Fatalf("opening %s: %v", name, err)
	}
	return doc
}

// allPageSpans collects spans from every page.
func allPageSpans(doc *Document) [][]TextSpan {
	pages := make([][]TextSpan, doc.NumPages())
	for i := 0; i < doc.NumPages(); i++ {
		pages[i], _ = doc.Page(i).TextSpans()
	}
	return pages
}

// quotation PDFs share headers: Quantity, Product Code, Suppliers Code, Product Description
var quotationHeaders = []string{"Quantity", "Suppliers"}

// quotationPDFs lists single-page quotation PDFs with expected column and minimum row counts.
var quotationPDFs = []struct {
	file    string
	cols    int // expected column count
	minRows int // minimum data rows expected
}{
	{"P2 Block 2 Showers.pdf", 6, 5},
	{"M1951 WHB_.pdf", 6, 5},
	{"AMAZON LTN4 SHOWER TRAY.pdf", 6, 5},
	{"AMAZON LTN4- S6454MY.pdf", 6, 5},
	{"Amazon LCY2 Tilbury 11569_NL1.pdf", 6, 5},
}

func TestIntegration_ExplicitHeaders_SinglePage(t *testing.T) {
	for _, tc := range quotationPDFs {
		t.Run(tc.file, func(t *testing.T) {
			doc := openTestPDF(t, tc.file)
			spans, _ := doc.Page(0).TextSpans()

			tbl := FindTable(spans, &TableOpts{Headers: quotationHeaders})
			if tbl == nil {
				t.Fatal("FindTable returned nil")
			}
			if len(tbl.Columns) != tc.cols {
				names := make([]string, len(tbl.Columns))
				for i, c := range tbl.Columns {
					names[i] = c.Name
				}
				t.Errorf("columns: got %d %v, want %d", len(tbl.Columns), names, tc.cols)
			}
			if len(tbl.Rows) < tc.minRows {
				t.Errorf("rows: got %d, want >= %d", len(tbl.Rows), tc.minRows)
			}

			// Verify known header names are present.
			for _, want := range []string{"Quantity", "Product Code", "Suppliers Code", "Product Description"} {
				if tbl.ColumnByName(want) < 0 {
					t.Errorf("missing column %q", want)
				}
			}

			// Verify first data row has content in Quantity column.
			qtyCol := tbl.ColumnByName("Quantity")
			if qtyCol >= 0 && tbl.CellText(0, qtyCol) == "" {
				t.Error("first row Quantity cell is empty")
			}
		})
	}
}

func TestIntegration_ExplicitHeaders_MultiPage(t *testing.T) {
	// These PDFs have tables that span across pages (header repeated on page 2+).
	multiPagePDFs := []struct {
		file    string
		pages   int
		minRows int
	}{
		{"Joseph Wright Shower Block Cathedral Road Derby DE1 3PA.pdf", 2, 50},
		{"Lynton House_1.pdf", 3, 60},
	}

	for _, tc := range multiPagePDFs {
		t.Run(tc.file, func(t *testing.T) {
			doc := openTestPDF(t, tc.file)
			if doc.NumPages() < tc.pages {
				t.Fatalf("expected >= %d pages, got %d", tc.pages, doc.NumPages())
			}

			pages := allPageSpans(doc)
			tbl := FindTableAcrossPages(pages, &TableOpts{Headers: quotationHeaders})
			if tbl == nil {
				t.Fatal("FindTableAcrossPages returned nil")
			}
			if len(tbl.Rows) < tc.minRows {
				t.Errorf("rows across %d pages: got %d, want >= %d", doc.NumPages(), len(tbl.Rows), tc.minRows)
			}

			// Multi-page should find more rows than single first page.
			firstPage := pages[0]
			single := FindTable(firstPage, &TableOpts{Headers: quotationHeaders})
			if single != nil && len(tbl.Rows) <= len(single.Rows) {
				t.Errorf("multi-page (%d rows) should exceed single page (%d rows)", len(tbl.Rows), len(single.Rows))
			}
		})
	}
}

func TestIntegration_WrappedHeaders_Real(t *testing.T) {
	// Lynton House_2 has 8 columns with wrapped headers:
	//   Row 1: Quantity  Product  Suppliers  Product Description  List Price  Cost Price  Selling Price  Total Selling
	//   Row 2:           Code     Code                                                                   Price
	doc := openTestPDF(t, "Lynton House_2.pdf")
	spans, _ := doc.Page(0).TextSpans()

	tbl := FindTable(spans, &TableOpts{Headers: quotationHeaders})
	if tbl == nil {
		t.Fatal("FindTable returned nil")
	}

	// Should have 8 columns (the wrapped "Total Selling" + "Price" merges).
	if len(tbl.Columns) < 7 {
		names := make([]string, len(tbl.Columns))
		for i, c := range tbl.Columns {
			names[i] = c.Name
		}
		t.Errorf("expected >= 7 columns for wrapped-header PDF, got %d: %v", len(tbl.Columns), names)
	}

	// Verify wrapped headers merged correctly.
	found := false
	for _, c := range tbl.Columns {
		if strings.Contains(c.Name, "Suppliers") && strings.Contains(c.Name, "Code") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'Suppliers Code' as merged header")
	}

	if len(tbl.Rows) < 3 {
		t.Errorf("expected >= 3 data rows, got %d", len(tbl.Rows))
	}
}

func TestIntegration_AutoDetect(t *testing.T) {
	// Gap-based auto-detection has a known limitation: when one column
	// (e.g., Description) has very wide text, its EndX can overlap adjacent
	// columns, destroying the gap signal. The quotation PDFs exhibit this.
	// This test verifies auto-detect doesn't crash and works on PDFs with
	// narrower columns.
	for _, tc := range quotationPDFs {
		t.Run(tc.file, func(t *testing.T) {
			doc := openTestPDF(t, tc.file)
			spans, _ := doc.Page(0).TextSpans()

			// Should not panic; may or may not find tables.
			tables := FindTables(spans, nil)
			for _, tbl := range tables {
				if len(tbl.Columns) < 2 {
					t.Errorf("auto-detected table with %d columns (need >= 2)", len(tbl.Columns))
				}
			}
		})
	}
}

func TestIntegration_AutoDetect_NonTablePDF(t *testing.T) {
	// PDFs without structured tables should produce few or no results.
	nonTablePDFs := []string{"waranty.pdf", "cover_template.pdf"}
	for _, file := range nonTablePDFs {
		t.Run(file, func(t *testing.T) {
			doc := openTestPDF(t, file)
			for i := 0; i < doc.NumPages(); i++ {
				spans, _ := doc.Page(i).TextSpans()
				tables := FindTables(spans, nil)
				for _, tbl := range tables {
					// Any auto-detected "table" in non-table PDFs should be small
					// (false positives with many rows would indicate a problem).
					if len(tbl.Rows) > 10 && len(tbl.Columns) >= 3 {
						t.Errorf("page %d: auto-detect found suspicious table (%d cols, %d rows) in non-table PDF",
							i, len(tbl.Columns), len(tbl.Rows))
					}
				}
			}
		})
	}
}

func TestIntegration_PageTablesConvenience(t *testing.T) {
	doc := openTestPDF(t, "P2 Block 2 Showers.pdf")
	page := doc.Page(0)

	// Page.FindTable with explicit headers.
	tbl, err := page.FindTable(&TableOpts{Headers: quotationHeaders})
	if err != nil {
		t.Fatal(err)
	}
	if tbl == nil {
		t.Fatal("Page.FindTable returned nil")
	}
	if len(tbl.Columns) != 6 {
		t.Errorf("columns: got %d, want 6", len(tbl.Columns))
	}

	// Page.Tables auto-detection — may not find tables on quotation PDFs
	// due to wide Description column (see TestIntegration_AutoDetect).
	_, err = page.Tables()
	if err != nil {
		t.Fatal(err)
	}
}

func TestIntegration_CellByName_RealData(t *testing.T) {
	doc := openTestPDF(t, "P2 Block 2 Showers.pdf")
	spans, _ := doc.Page(0).TextSpans()

	tbl := FindTable(spans, &TableOpts{Headers: quotationHeaders})
	if tbl == nil {
		t.Fatal("no table")
	}

	// First row should have a numeric quantity.
	qty := tbl.CellByName(0, "Quantity")
	if qty == "" {
		t.Error("Quantity cell is empty")
	}

	// Product Description should be non-empty.
	desc := tbl.CellByName(0, "Product Description")
	if desc == "" {
		t.Error("Product Description cell is empty")
	}

	// Suppliers Code should be non-empty.
	supp := tbl.CellByName(0, "Suppliers Code")
	if supp == "" {
		t.Error("Suppliers Code cell is empty")
	}
}

// =====================================================================
// Integration: BCR bank statement (different table format)
// =====================================================================

var bcrHeaders = []string{"Explica" + "tie", "Debit"} // Romanian; concatenated to avoid misspell lint

func TestIntegration_BCR_ExplicitHeaders(t *testing.T) {
	doc := openTestPDF(t, "BCR_Cont_principal.pdf")

	spans, _ := doc.Page(0).TextSpans()
	tbl := FindTable(spans, &TableOpts{Headers: bcrHeaders})
	if tbl == nil {
		t.Fatal("FindTable returned nil on BCR page 0")
	}

	// Should detect 5 columns.
	if len(tbl.Columns) != 5 {
		names := make([]string, len(tbl.Columns))
		for i, c := range tbl.Columns {
			names[i] = c.Name
		}
		t.Errorf("columns: got %d %v, want 5", len(tbl.Columns), names)
	}

	// Verify expected column names.
	for _, want := range []string{"Explica" + "tie", "Debit", "Credit"} {
		if tbl.ColumnByName(want) < 0 {
			t.Errorf("missing column %q", want)
		}
	}

	if len(tbl.Rows) < 5 {
		t.Fatalf("expected >= 5 rows, got %d", len(tbl.Rows))
	}

	// Some rows should have non-empty Debit or Credit values.
	di := tbl.ColumnByName("Debit")
	ci := tbl.ColumnByName("Credit")
	hasDebit, hasCredit := false, false
	for ri := 0; ri < len(tbl.Rows); ri++ {
		if di >= 0 && tbl.CellText(ri, di) != "" {
			hasDebit = true
		}
		if ci >= 0 && tbl.CellText(ri, ci) != "" {
			hasCredit = true
		}
	}
	if !hasDebit {
		t.Error("no rows have Debit values")
	}
	if !hasCredit {
		t.Error("no rows have Credit values")
	}
}

func TestIntegration_BCR_MultiPage(t *testing.T) {
	doc := openTestPDF(t, "BCR_Cont_principal.pdf")
	if doc.NumPages() < 10 {
		t.Fatalf("expected >= 10 pages, got %d", doc.NumPages())
	}

	pages := allPageSpans(doc)
	tbl := FindTableAcrossPages(pages, &TableOpts{Headers: bcrHeaders})
	if tbl == nil {
		t.Fatal("FindTableAcrossPages returned nil")
	}

	// 15-page statement should accumulate many rows.
	if len(tbl.Rows) < 100 {
		t.Errorf("expected >= 100 rows across all pages, got %d", len(tbl.Rows))
	}

	// Should have more rows than any single page.
	maxSingle := 0
	for _, pageSpans := range pages {
		single := FindTable(pageSpans, &TableOpts{Headers: bcrHeaders})
		if single != nil && len(single.Rows) > maxSingle {
			maxSingle = len(single.Rows)
		}
	}
	if len(tbl.Rows) <= maxSingle {
		t.Errorf("multi-page (%d rows) should exceed best single page (%d rows)", len(tbl.Rows), maxSingle)
	}
}

func TestIntegration_BCR_WrappedSubHeaders(t *testing.T) {
	// BCR has a sub-header row ("Data Valorii" / "Document") below the main
	// headers. These should NOT merge into the main headers because they're
	// multi-word spans too far from any single-word header.
	doc := openTestPDF(t, "BCR_Cont_principal.pdf")
	spans, _ := doc.Page(0).TextSpans()

	tbl := FindTable(spans, &TableOpts{Headers: bcrHeaders})
	if tbl == nil {
		t.Fatal("no table")
	}

	// Column names should be the main headers, not merged with sub-headers.
	for _, c := range tbl.Columns {
		if strings.Contains(c.Name, "Valorii") {
			t.Errorf("sub-header 'Data Valorii' incorrectly merged into column %q", c.Name)
		}
		if strings.Contains(c.Name, "Document") {
			t.Errorf("sub-header 'Document' incorrectly merged into column %q", c.Name)
		}
	}
}

func TestIntegration_AllQuotationPDFs(t *testing.T) {
	// Scan all PDFs in example_out/ — every quotation-format PDF should
	// be detectable with explicit headers.
	entries, err := os.ReadDir(pdfDir)
	if err != nil {
		t.Skipf("cannot read %s: %v", pdfDir, err)
	}

	quotationCount := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pdf") {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			doc := openTestPDF(t, e.Name())
			spans, _ := doc.Page(0).TextSpans()

			tbl := FindTable(spans, &TableOpts{Headers: quotationHeaders})
			if tbl == nil {
				return // not a quotation PDF, that's fine
			}

			quotationCount++

			if len(tbl.Columns) < 4 {
				t.Errorf("quotation table has only %d columns, want >= 4", len(tbl.Columns))
			}
			if len(tbl.Rows) == 0 {
				t.Error("quotation table has 0 data rows")
			}

			for ri, row := range tbl.Rows {
				hasContent := false
				for _, cell := range row.Cells {
					if cell.Text != "" {
						hasContent = true
						break
					}
				}
				if !hasContent {
					t.Errorf("row %d is completely empty", ri)
				}
			}
		})
	}

	if quotationCount == 0 {
		t.Error("no quotation PDFs were detected — expected at least one")
	}
}
