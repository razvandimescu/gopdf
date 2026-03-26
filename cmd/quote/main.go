package main

import (
	"fmt"
	"os"

	"github.com/razvandimescu/gopdf/pdf"
)

func main() {
	path := "input.pdf"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}

	doc, err := pdf.OpenFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening %s: %v\n", path, err)
		os.Exit(1)
	}

	quote := ExtractQuote(doc)
	fmt.Print(quote)
}
