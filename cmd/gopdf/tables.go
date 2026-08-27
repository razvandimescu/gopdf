package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/razvandimescu/gopdf/pdf"
)

func runTables(args []string) error {
	fs := flag.NewFlagSet("tables", flag.ExitOnError)
	format := fs.String("format", "text", "output format: text or csv")
	out := fs.String("o", "", "output path (default: stdout)")
	headers := fs.String("headers", "", `comma-separated header anchors (e.g. "Date,Description,Amount")`)
	mergeGap := fs.Float64("merge-gap", 0, "merge rows within this Y-distance into one logical row")
	maxRowGap := fs.Float64("max-row-gap", 0, "stop the table when a row gap exceeds this")
	anchor := fs.String("anchor", "", "column name that signals a new row (rows where it is empty merge upwards)")
	filter := fs.String("filter", "", "comma-separated substrings; rows containing any of them are dropped")
	require := fs.String("require", "", "comma-separated column names; a row is kept only if one of them is filled")
	colWidth := fs.Int("col-width", 30, "maximum column width, for -format text")
	password := fs.String("password", "", "password for an encrypted PDF")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: gopdf tables [flags] input.pdf\n\n"+
			"Detects a table and prints its rows. Columns are found from their headers\n"+
			"when -headers is given, and by column geometry otherwise.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	paths, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(paths) != 1 {
		fs.Usage()
		os.Exit(2)
	}
	if *format != "text" && *format != "csv" {
		return fmt.Errorf("unknown -format %q; want text or csv", *format)
	}

	var opts []pdf.Option
	if *password != "" {
		opts = append(opts, pdf.WithPassword(*password))
	}
	doc, err := pdf.OpenFile(paths[0], opts...)
	if err != nil {
		return err
	}

	pages := make([][]pdf.TextSpan, doc.NumPages())
	for i := range pages {
		spans, err := doc.Page(i).TextSpans()
		if err != nil {
			return fmt.Errorf("page %d: %w", i+1, err)
		}
		pages[i] = spans
	}

	table := pdf.FindTableAcrossPages(pages, tableOpts(*headers, *filter, *require, *anchor, *mergeGap, *maxRowGap))
	if table == nil {
		return fmt.Errorf("no table found in %s", paths[0])
	}

	var w io.Writer = os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	if *format == "csv" {
		return writeCSV(w, table)
	}
	writeText(w, table, *colWidth)
	return nil
}

func tableOpts(headers, filter, require, anchor string, mergeGap, maxRowGap float64) *pdf.TableOpts {
	opts := &pdf.TableOpts{
		MergeGap:     mergeGap,
		MaxRowGap:    maxRowGap,
		AnchorColumn: anchor,
		AutoTune:     mergeGap == 0 && maxRowGap == 0,
	}
	if headers != "" {
		opts.Headers = splitList(headers)
	}
	opts.RequireAnyColumn = splitList(require)

	if dropped := splitList(filter); len(dropped) > 0 {
		for i := range dropped {
			dropped[i] = strings.ToLower(dropped[i])
		}
		opts.RowFilter = func(cells []string) bool {
			for _, cell := range cells {
				lower := strings.ToLower(cell)
				for _, d := range dropped {
					if strings.Contains(lower, d) {
						return false
					}
				}
			}
			return true
		}
	}
	return opts
}

// splitList turns a comma-separated flag value into its non-empty entries.
func splitList(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func writeCSV(w io.Writer, table *pdf.Table) error {
	out := csv.NewWriter(w)
	header := make([]string, len(table.Columns))
	for i, col := range table.Columns {
		header[i] = col.Name
	}
	if err := out.Write(header); err != nil {
		return err
	}
	for _, row := range table.Rows {
		record := make([]string, len(table.Columns))
		for i, cell := range row.Cells {
			if i < len(record) {
				record[i] = cell.Text
			}
		}
		if err := out.Write(record); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}

func writeText(w io.Writer, table *pdf.Table, colWidth int) {
	widths := make([]int, len(table.Columns))
	for i, col := range table.Columns {
		widths[i] = max(len(col.Name), colWidth)
	}

	for i, col := range table.Columns {
		if i > 0 {
			fmt.Fprint(w, " | ")
		}
		fmt.Fprintf(w, "%-*s", widths[i], truncate(col.Name, widths[i]))
	}
	fmt.Fprintln(w)

	total := (len(widths) - 1) * 3
	for _, width := range widths {
		total += width
	}
	fmt.Fprintln(w, strings.Repeat("-", total))

	for _, row := range table.Rows {
		for i := range table.Columns {
			if i > 0 {
				fmt.Fprint(w, " | ")
			}
			text := ""
			if i < len(row.Cells) {
				text = row.Cells[i].Text
			}
			if isNumeric(text) { // figures read better against the column edge
				fmt.Fprintf(w, "%*s", widths[i], truncate(text, widths[i]))
			} else {
				fmt.Fprintf(w, "%-*s", widths[i], truncate(text, widths[i]))
			}
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "(%d row%s)\n", len(table.Rows), plural(len(table.Rows)))
}

func truncate(s string, n int) string {
	if len(s) <= n || n < 4 {
		return s
	}
	return s[:n-3] + "..."
}

func isNumeric(s string) bool {
	s = strings.TrimSpace(strings.NewReplacer(",", "", ".", "").Replace(s))
	if s == "" {
		return false
	}
	_, err := strconv.Atoi(s)
	return err == nil
}
