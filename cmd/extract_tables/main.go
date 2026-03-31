package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/razvandimescu/gopdf/pdf"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: extract_tables <file.pdf>\n")
		os.Exit(1)
	}

	doc, err := pdf.OpenFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening PDF: %v\n", err)
		os.Exit(1)
	}

	opts := &pdf.TableOpts{
		Headers:   []string{"Data", "Descriere", "Debit", "Credit"},
		MergeGap:  16,
		MaxRowGap: 30,
		RowFilter: func(cells []string) bool {
			for _, c := range cells {
				if isDisclaimer(c) {
					return false
				}
			}
			return true
		},
		WrapTolerance: -1,
	}

	var allPages [][]pdf.TextSpan
	for i := 0; i < doc.NumPages(); i++ {
		spans, _ := doc.Page(i).TextSpans()
		allPages = append(allPages, spans)
	}

	table := pdf.FindTableAcrossPages(allPages, opts)
	if table == nil {
		fmt.Println("No table found.")
		return
	}

	fmt.Printf("%-4s | %-18s | %s | %10s | %10s\n", "#", "Data", "Descriere", "Debit", "Credit")
	fmt.Println(strings.Repeat("-", 120))

	for i := range table.Rows {
		data := table.CellByName(i, "Data")
		desc := table.CellByName(i, "Descriere")
		debit := table.CellByName(i, "Debit")
		credit := table.CellByName(i, "Credit")
		fmt.Printf("%-4d | %-18s | %s | %10s | %10s\n", i+1, data, desc, debit, credit)
	}
	fmt.Printf("\nTotal: %d rows across %d pages\n", len(table.Rows), doc.NumPages())
}

func isDisclaimer(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "fondurile disponibile") ||
		strings.Contains(lower, "garantare a depozitelor") ||
		strings.Contains(lower, "depozitele garantate") ||
		strings.Contains(lower, "comisioanele aplicate") ||
		text == "www.unicredit.ro"
}
