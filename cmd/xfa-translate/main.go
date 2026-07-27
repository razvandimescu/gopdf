// xfa-translate translates the static labels of an XFA-dynamic PDF form by
// rewriting the embedded XFA template stream. Designed for the Romanian ANAF
// fiscal forms (Bilanț / F10 / F20 / F30) but generic for any XFA template.
//
//	xfa-translate dump <in.pdf> <out.tsv>
//	    Extract all translatable labels to a TSV (columns: ro, en, context).
//	    If the file already exists, existing EN translations are preserved.
//
//	xfa-translate apply <in.pdf> <map.tsv> <out.pdf>
//	    Rewrite the PDF with EN substitutions from the TSV.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "dump":
		if len(os.Args) != 4 {
			usage()
			os.Exit(2)
		}
		err = runDump(os.Args[2], os.Args[3])
	case "apply":
		if len(os.Args) != 5 {
			usage()
			os.Exit(2)
		}
		err = runApply(os.Args[2], os.Args[3], os.Args[4])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  xfa-translate dump  <in.pdf> <out.tsv>")
	fmt.Fprintln(os.Stderr, "  xfa-translate apply <in.pdf> <map.tsv> <out.pdf>")
}
