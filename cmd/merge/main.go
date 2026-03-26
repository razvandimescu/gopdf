package main

import (
	"fmt"
	"os"

	"github.com/razvandimescu/gopdf/pdf"
)

func main() {
	args := os.Args[1:]
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: merge input1.pdf input2.pdf ... > output.pdf\n")
		os.Exit(1)
	}

	// Check for -o flag.
	output := ""
	var inputs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-o" && i+1 < len(args) {
			output = args[i+1]
			i++
		} else {
			inputs = append(inputs, args[i])
		}
	}

	merged, err := pdf.MergeFiles(inputs...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if output != "" {
		if err := os.WriteFile(output, merged, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", output, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Merged %d files → %s (%d bytes)\n", len(inputs), output, len(merged))
	} else {
		os.Stdout.Write(merged)
	}
}
