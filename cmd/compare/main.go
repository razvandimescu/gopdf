package main

import (
	"fmt"
	"os"
	"strings"

	lpdf "github.com/ledongthuc/pdf"
)

func main() {
	path := "input.pdf"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}

	f, r, err := lpdf.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening %s: %v\n", path, err)
		os.Exit(1)
	}
	defer f.Close()

	fmt.Printf("PDF has %d page(s)\n\n", r.NumPage())

	for i := 1; i <= r.NumPage(); i++ {
		fmt.Printf("=== Page %d ===\n", i)
		p := r.Page(i)
		if p.V.IsNull() {
			fmt.Println("  (null page)")
			continue
		}

		rows, err := p.GetTextByRow()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  error extracting text: %v\n", err)
			continue
		}

		for _, row := range rows {
			var parts []string
			for _, word := range row.Content {
				parts = append(parts, word.S)
			}
			fmt.Println(strings.Join(parts, " "))
		}
		fmt.Println()
	}
}
