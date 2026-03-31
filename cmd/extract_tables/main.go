package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/razvandimescu/gopdf/pdf"
)

func main() {
	headers := flag.String("headers", "", "comma-separated header anchors (e.g. \"Date,Description,Amount\")")
	mergeGap := flag.Float64("merge-gap", 0, "merge rows within this Y-distance into one logical row")
	maxRowGap := flag.Float64("max-row-gap", 0, "stop table when row gap exceeds this")
	anchorCol := flag.String("anchor", "", "column name that signals a new row (merge rows where this is empty)")
	filterOut := flag.String("filter", "", "comma-separated substrings — rows containing any of these are excluded")
	colWidth := flag.Int("col-width", 30, "max column width for display")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: extract_tables [flags] <file.pdf>\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	doc, err := pdf.OpenFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var allPages [][]pdf.TextSpan
	for i := 0; i < doc.NumPages(); i++ {
		spans, _ := doc.Page(i).TextSpans()
		allPages = append(allPages, spans)
	}

	var filters []string
	if *filterOut != "" {
		for _, f := range strings.Split(*filterOut, ",") {
			f = strings.TrimSpace(f)
			if f != "" {
				filters = append(filters, strings.ToLower(f))
			}
		}
	}

	opts := &pdf.TableOpts{
		MergeGap:     *mergeGap,
		MaxRowGap:    *maxRowGap,
		AnchorColumn: *anchorCol,
		AutoTune:     *mergeGap == 0 && *maxRowGap == 0,
	}
	if len(filters) > 0 {
		opts.RowFilter = func(cells []string) bool {
			for _, c := range cells {
				cl := strings.ToLower(c)
				for _, f := range filters {
					if strings.Contains(cl, f) {
						return false
					}
				}
			}
			return true
		}
	}
	if *headers != "" {
		opts.Headers = strings.Split(*headers, ",")
		for i := range opts.Headers {
			opts.Headers[i] = strings.TrimSpace(opts.Headers[i])
		}
	}

	table := pdf.FindTableAcrossPages(allPages, opts)
	if table == nil {
		fmt.Println("No tables found.")
		return
	}

	printTable(table, *colWidth)
}

func printTable(tbl *pdf.Table, colWidth int) {
	ncols := len(tbl.Columns)
	widths := make([]int, ncols)
	for i, col := range tbl.Columns {
		widths[i] = max(len(col.Name), colWidth)
	}

	// Header
	for i, col := range tbl.Columns {
		if i > 0 {
			fmt.Print(" | ")
		}
		fmt.Printf("%-*s", widths[i], truncate(col.Name, widths[i]))
	}
	fmt.Println()
	total := 0
	for _, w := range widths {
		total += w
	}
	fmt.Println(strings.Repeat("-", total+(ncols-1)*3))

	// Rows
	for _, row := range tbl.Rows {
		for ci := range tbl.Columns {
			if ci > 0 {
				fmt.Print(" | ")
			}
			text := ""
			if ci < len(row.Cells) {
				text = row.Cells[ci].Text
			}
			// Right-align if numeric
			if isNumeric(text) {
				fmt.Printf("%*s", widths[ci], truncate(text, widths[ci]))
			} else {
				fmt.Printf("%-*s", widths[ci], truncate(text, widths[ci]))
			}
		}
		fmt.Println()
	}
	fmt.Printf("(%d rows)\n", len(tbl.Rows))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func isNumeric(s string) bool {
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, ".", "")
	s = strings.TrimSpace(s)
	_, err := strconv.Atoi(s)
	return err == nil && s != ""
}

