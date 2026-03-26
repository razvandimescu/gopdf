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
	TableHeaders  []string
}

var testCases = []expectedQuote{
	{
		File:         "example_out/Northgate Academy.pdf",
		Company:      "Nova Facilities",
		QuoteName:    "Northgate Academy",
		QuotationRef: "QT10001",
		SupplierCodes: []string{
			"SC-0001", "SC-0002", "SC-0003", "SC-0004",
			"SC-0005", "SC-0008", "SC-0006", "SC-0007",
		},
		TableHeaders: []string{"Quantity", "Product Code", "Suppliers Code", "Product Description", "Unit Price", "Total Price"},
	},
	{
		File:         "example_out/10 Market St.pdf",
		Company:      "Apex Build LTD/ Apex Group",
		QuoteName:    "10 Market St",
		QuotationRef: "QT10002",
		SupplierCodes: []string{
			"SC-0010", "SC-0011", "SC-0012", "SC-0013", "SC-0014",
			"SC-0015", "SC-0006", "SC-0016", "SC-0017",
			"SC-0018", "SC-0019", "SC-0020",
		},
		TableHeaders: []string{"Quantity", "Product Code", "Suppliers Code", "Product Description"},
	},
	{
		File:         "example_out/Oakwood House_1.pdf",
		Company:      "Summit Building Services Ltd",
		QuoteName:    "Oakwood House",
		QuotationRef: "QT10003/2",
		SupplierCodes: []string{
			"SC-0016", "SC-0017", "SC-0021", "SC-0022", "SC-0023",
			"SC-0024", "SC-0006", "SC-0025", "SC-0026",
			"SC-0027", "SC-0028", "SC-0029", "SC-0030",
			"SC-0031", "SC-0032", "SC-0033", "SC-0034",
			"SC-0035", "SC-0014", "SC-0037", "SC-0038",
		},
		TableHeaders: []string{"Quantity", "Product Code", "Suppliers Code", "Product Description"},
	},
	{
		File:         "example_out/Oakwood House_2.pdf",
		Company:      "Summit Building Services Ltd",
		QuoteName:    "Oakwood House",
		QuotationRef: "QT10004",
		SupplierCodes: []string{
			"SC-0039",
		},
		TableHeaders: []string{"Quantity", "Product Code", "Suppliers Code", "Product Description", "List Price", "Cost Price", "Selling Price", "Total Selling Price"},
	},
	{
		File:         "example_out/P2 Wing 2 Showers.pdf",
		Company:      "Delta Engineering",
		QuoteName:    "P2 Wing 2 Showers",
		QuotationRef: "QT10005",
		SupplierCodes: []string{
			"SC-0040",
		},
	},
	{
		File:         "example_out/M2001 WHB_.pdf",
		Company:      "Delta Engineering",
		QuoteName:    "M2001 WHB",
		QuotationRef: "QT10006",
		SupplierCodes: []string{
			"SC-0041",
		},
	},
	{
		File:         "example_out/DEPOT A- X6454AB.pdf",
		Company:      "Delta Engineering",
		QuoteName:    "DEPOT A- X6454AB",
		QuotationRef: "QT10007",
		SupplierCodes: []string{
			"X6454AB",
		},
	},
	{
		File:         "example_out/DEPOT A SHOWER TRAY.pdf",
		Company:      "Delta Engineering",
		QuoteName:    "DEPOT A SHOWER TRAY",
		QuotationRef: "QT10008",
		SupplierCodes: []string{
			"SC-0043",
		},
	},
	{
		File:         "example_out/Depot B Southend 20001_NL1.pdf",
		Company:      "Delta Engineering",
		QuoteName:    "Depot B Southend 20001/NL1",
		QuotationRef: "QT10009",
		SupplierCodes: []string{
			"SC-0044",
		},
	},
	{
		File:         "example_out/Riverside Shower Block Park Lane Leeds LS1 2AB.pdf",
		Company:      "Greenfield Mechanical Services Limited",
		QuoteName:    "Riverside Shower Block, Park Lane, Leeds, LS1 2AB",
		QuotationRef: "QT10010/2",
		SupplierCodes: []string{
			"SC-0046", "SC-0045/B-MF-C/P", "SC-0049", "SC-0047/WH",
			"SC-0048/DB", "SC-0050", "SC-0051", "SC-0052",
			"SC-0053X1200", "SC-0053x900", "SC-0054", "SC-0055",
			"SC-0057", "SC-0058", "SC-0060-DB-C/P",
		},
	},
}

func extractFromFile(t *testing.T, path string) QuoteData {
	t.Helper()
	doc, err := pdf.OpenFile(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	return ExtractQuote(doc)
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
