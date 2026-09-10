package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/razvandimescu/gopdf/pdf"
)

func runPages(args []string) error {
	fs := flag.NewFlagSet("pages", flag.ExitOnError)
	out := fs.String("o", "", "output PDF path (default: stdout)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: gopdf pages <range> [flags] input.pdf\n\n"+
			"Keeps only the pages named by the range, in the order written.\n"+
			"Pages count from 1: 3, 1-2, 5- (to the end), or 1,3,5-7.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 2 {
		fs.Usage()
		os.Exit(2)
	}
	spec, path := positional[0], positional[1]
	// A PDF where the range belongs is an argument-order slip, not a range.
	if _, err := os.Stat(spec); err == nil {
		return fmt.Errorf("put the page range first: gopdf pages 1-2 %s", spec)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	doc, err := pdf.OpenBytes(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	pages, err := parsePages(spec, doc.NumPages())
	if err != nil {
		return err
	}

	m := pdf.NewMerger()
	if err := m.Add(data, pages...); err != nil {
		return err
	}
	output, err := m.Merge()
	if err != nil {
		return err
	}

	return writeOutput(*out, output)
}

// parsePages turns a 1-based range like "1,3,5-7" into 0-based indices into an
// n-page document, keeping the order written so "3,1" reverses two pages.
func parsePages(spec string, n int) ([]int, error) {
	var pages []int
	for _, item := range strings.Split(spec, ",") {
		first, last := item, item
		toEnd := false
		if from, to, isRange := strings.Cut(item, "-"); isRange {
			first, last, toEnd = from, to, to == ""
		}
		start, err := pageNumber(first, spec)
		if err != nil {
			return nil, err
		}
		end := n
		if !toEnd {
			if end, err = pageNumber(last, spec); err != nil {
				return nil, err
			}
			if end < start {
				return nil, fmt.Errorf("%s counts backwards; write %d-%d", item, end, start)
			}
		}
		if start > n || end > n {
			return nil, fmt.Errorf("page %d is out of range; the document has %d page%s",
				max(start, end), n, plural(n))
		}
		for p := start; p <= end; p++ {
			pages = append(pages, p-1)
		}
	}
	return pages, nil
}

func pageNumber(s, spec string) (int, error) {
	// Digits only: with a sign accepted, "1--2" would read as page 1 to page
	// -2 rather than as the malformed range it is.
	digits := strings.TrimSpace(s)
	page, err := strconv.Atoi(digits)
	if err != nil || strings.TrimLeft(digits, "0123456789") != "" {
		return 0, badRange(spec)
	}
	if page < 1 {
		return 0, fmt.Errorf("pages are numbered from 1")
	}
	return page, nil
}

func badRange(spec string) error {
	return fmt.Errorf("bad range %q: want pages like 1, 1-2, 5- or 1,3,5-7", spec)
}
