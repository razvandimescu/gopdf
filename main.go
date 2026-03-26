package main

import (
	"fmt"
	"os"

	"gopdf/pdf"
)

func main() {
	path := "input.pdf"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", path, err)
		os.Exit(1)
	}

	reader, err := pdf.Open(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error parsing PDF: %v\n", err)
		os.Exit(1)
	}

	pages, err := reader.Pages()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting pages: %v\n", err)
		os.Exit(1)
	}

	// Extract positioned text spans from all pages.
	var allSpans [][]pdf.TextSpan
	for _, page := range pages {
		content, err := reader.PageContent(page)
		if err != nil {
			continue
		}
		fonts := reader.PageFonts(page)
		spans := pdf.ExtractText(content, fonts, reader)
		allSpans = append(allSpans, spans)
	}

	// Structured extraction.
	quote := ExtractQuote(allSpans)
	fmt.Print(quote)
}
