package main

import (
	"os"
	"sort"
	"strings"
	"testing"

	"gopdf/pdf"
)

type expectedQuote struct {
	File          string
	Company       string
	QuoteName     string
	QuotationRef  string
	SupplierCodes []string
	TableHeaders  []string // expected subset of headers
}

var testCases = []expectedQuote{
	{
		File:         "example_out/King David Sixth Form.pdf",
		Company:      "Optimus Facilities",
		QuoteName:    "King David Sixth Form",
		QuotationRef: "MG74703",
		SupplierCodes: []string{
			"S0439HY", "S1347AA", "S067001", "325999000",
			"BC186AA", "A10", "6951000000", "S4112MY",
		},
		TableHeaders: []string{"Quantity", "Product Code", "Suppliers Code", "Product Description", "Unit Price", "Total Price"},
	},
	{
		File:         "example_out/43 Whitfield St.pdf",
		Company:      "Sale Nugen LTD/ Sale Group",
		QuoteName:    "43 Whitfield St",
		QuotationRef: "RE75371",
		SupplierCodes: []string{
			"S6972MY", "S6960MY", "S225101", "201429", "A10R",
			"MBFU120W", "6951000000", "E050901", "E772601",
			"S332667", "S1082AA", "CD1200-P",
		},
		TableHeaders: []string{"Quantity", "Product Code", "Suppliers Code", "Product Description"},
	},
	{
		File:         "example_out/Lynton House_1.pdf",
		Company:      "Pip Building Services Ltd",
		QuoteName:    "Lynton House",
		QuotationRef: "SR73905/2",
		SupplierCodes: []string{
			"E050901", "E772601", "R031767", "R0115A6", "MAC-7A",
			"0703500008", "6951000000", "TSL.882BK", "204314",
			"204117", "S0438HY", "S4066V3", "R014267",
			"S247401", "S911067", "A7548AA", "40015CP",
			"S881001", "A10R", "S247301", "Dolphin Carriage",
		},
		TableHeaders: []string{"Quantity", "Product Code", "Suppliers Code", "Product Description"},
	},
	{
		File:         "example_out/Lynton House_2.pdf",
		Company:      "Pip Building Services Ltd",
		QuoteName:    "Lynton House",
		QuotationRef: "SR77665",
		SupplierCodes: []string{
			"TSL.990BK",
		},
		TableHeaders: []string{"Quantity", "Product Code", "Suppliers Code", "Product Description", "List Price", "Cost Price", "Selling Price", "Total Selling Price"},
	},
	{
		File:         "example_out/P2 Block 2 Showers.pdf",
		Company:      "MPE Engineering",
		QuoteName:    "P2 Block 2 Showers",
		QuotationRef: "RE74491",
		SupplierCodes: []string{
			"AB090-",
		},
	},
	{
		File:         "example_out/M1951 WHB_.pdf",
		Company:      "MPE Engineering",
		QuoteName:    "M1951 WHB",
		QuotationRef: "DK75237",
		SupplierCodes: []string{
			"B8263AA",
		},
	},
	{
		File:         "example_out/AMAZON LTN4- S6454MY.pdf",
		Company:      "MPE Engineering",
		QuoteName:    "AMAZON LTN4- S6454MY",
		QuotationRef: "DK74700",
		SupplierCodes: []string{
			"S6454MY",
		},
	},
	{
		File:         "example_out/AMAZON LTN4 SHOWER TRAY.pdf",
		Company:      "MPE Engineering",
		QuoteName:    "AMAZON LTN4 SHOWER TRAY",
		QuotationRef: "DK75234",
		SupplierCodes: []string{
			"F100100",
		},
	},
	{
		File:         "example_out/Amazon LCY2 Tilbury 11569_NL1.pdf",
		Company:      "MPE Engineering",
		QuoteName:    "Amazon LCY2 Tilbury 11569/NL1",
		QuotationRef: "DK73996",
		SupplierCodes: []string{
			"DRK4",
		},
	},
	{
		File:         "example_out/Joseph Wright Shower Block Cathedral Road Derby DE1 3PA.pdf",
		Company:      "Adkin Mechanical Services Limited",
		QuoteName:    "Joseph Wright Shower Block, Cathedral Road, Derby, DE1 3PA",
		QuotationRef: "RG73850/2",
		SupplierCodes: []string{
			"S0684LI", "PRIMABOX4/B-MF-C/P", "GP86710", "250603/WH",
			"SB-085/DB", "S645436", "S645236", "S636036",
			"SD1200X1200", "SD1200x900", "08GT52-R", "GP85200",
			"ST10010CP", "20010CP", "ERI-MFSRK-DB-C/P",
		},
	},
}

func extractFromFile(t *testing.T, path string) QuoteData {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	reader, err := pdf.Open(data)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	pages, err := reader.Pages()
	if err != nil {
		t.Fatalf("getting pages from %s: %v", path, err)
	}

	var allSpans [][]pdf.TextSpan
	for _, page := range pages {
		content, err := reader.PageContent(page)
		if err != nil {
			continue
		}
		fonts := reader.PageFonts(page)
		resources := reader.PageResources(page)
		spans := pdf.ExtractTextWithResources(content, fonts, reader, resources)
		allSpans = append(allSpans, spans)
	}

	return ExtractQuote(allSpans)
}

func TestSupplierCodes(t *testing.T) {
	for _, tc := range testCases {
		t.Run(tc.File, func(t *testing.T) {
			if _, err := os.Stat(tc.File); os.IsNotExist(err) {
				t.Skipf("test PDF not found: %s", tc.File)
			}

			q := extractFromFile(t, tc.File)

			// Check supplier codes: exact set match.
			got := make(map[string]bool)
			for _, code := range q.SupplierCodes {
				got[code] = true
			}
			want := make(map[string]bool)
			for _, code := range tc.SupplierCodes {
				want[code] = true
			}

			for code := range want {
				if !got[code] {
					t.Errorf("MISSING supplier code: %s", code)
				}
			}
			for code := range got {
				if !want[code] {
					t.Errorf("EXTRA supplier code: %s", code)
				}
			}

			if len(q.SupplierCodes) != len(tc.SupplierCodes) {
				t.Errorf("supplier code count: got %d, want %d", len(q.SupplierCodes), len(tc.SupplierCodes))
			}
		})
	}
}

func TestHeaderFields(t *testing.T) {
	for _, tc := range testCases {
		t.Run(tc.File, func(t *testing.T) {
			if _, err := os.Stat(tc.File); os.IsNotExist(err) {
				t.Skipf("test PDF not found: %s", tc.File)
			}

			q := extractFromFile(t, tc.File)

			if q.Company != tc.Company {
				t.Errorf("Company: got %q, want %q", q.Company, tc.Company)
			}
			if q.QuoteName != tc.QuoteName {
				t.Errorf("QuoteName: got %q, want %q", q.QuoteName, tc.QuoteName)
			}
			if q.QuotationRef != tc.QuotationRef {
				t.Errorf("QuotationRef: got %q, want %q", q.QuotationRef, tc.QuotationRef)
			}
		})
	}
}

func TestTableHeaders(t *testing.T) {
	for _, tc := range testCases {
		if len(tc.TableHeaders) == 0 {
			continue
		}
		t.Run(tc.File, func(t *testing.T) {
			if _, err := os.Stat(tc.File); os.IsNotExist(err) {
				t.Skipf("test PDF not found: %s", tc.File)
			}

			q := extractFromFile(t, tc.File)

			gotHeaders := q.TableHeaders.Columns
			for _, want := range tc.TableHeaders {
				found := false
				for _, got := range gotHeaders {
					if strings.EqualFold(got, want) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("missing table header %q in %v", want, gotHeaders)
				}
			}
		})
	}
}

func TestSupplierCodeOrder(t *testing.T) {
	// Verify supplier codes appear in document order (not sorted).
	for _, tc := range testCases {
		if len(tc.SupplierCodes) < 3 {
			continue
		}
		t.Run(tc.File, func(t *testing.T) {
			if _, err := os.Stat(tc.File); os.IsNotExist(err) {
				t.Skipf("test PDF not found: %s", tc.File)
			}

			q := extractFromFile(t, tc.File)

			// Verify that codes are NOT alphabetically sorted (would indicate wrong ordering).
			sorted := make([]string, len(q.SupplierCodes))
			copy(sorted, q.SupplierCodes)
			sort.Strings(sorted)

			isSorted := true
			for i := range sorted {
				if i < len(q.SupplierCodes) && sorted[i] != q.SupplierCodes[i] {
					isSorted = false
					break
				}
			}

			// Verify exact order matches expected.
			if len(q.SupplierCodes) == len(tc.SupplierCodes) {
				for i, code := range tc.SupplierCodes {
					if q.SupplierCodes[i] != code {
						t.Errorf("supplier code order mismatch at index %d: got %q, want %q", i, q.SupplierCodes[i], code)
						break
					}
				}
			}

			_ = isSorted
		})
	}
}
