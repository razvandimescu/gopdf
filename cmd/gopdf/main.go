// gopdf is the command-line interface to the gopdf library.
//
// Usage:
//
//	gopdf <command> [flags]
//
// Run "gopdf <command> -h" for a command's flags.
package main

import (
	"flag"
	"fmt"
	"os"
)

type command struct {
	name    string
	summary string
	run     func(args []string) error
}

var commands = []command{
	{"merge", "combine PDFs and images (PNG/JPEG/GIF) into one PDF", runMerge},
	{"pages", "keep only some pages of a PDF", runPages},
	{"tables", "extract a table from a PDF as text or CSV", runTables},
	{"watermark", "stamp an image across every page of a PDF", runWatermark},
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	for _, c := range commands {
		if c.name != args[0] {
			continue
		}
		if err := c.run(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "gopdf %s: %v\n", c.name, err)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "gopdf: unknown command %q\n\n", args[0])
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprint(os.Stderr, "Usage: gopdf <command> [flags]\n\nCommands:\n")
	for _, c := range commands {
		fmt.Fprintf(os.Stderr, "  %-10s %s\n", c.name, c.summary)
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// writeOutput sends a result to a file, or to stdout when no path is given.
func writeOutput(path string, data []byte) error {
	if path == "" {
		_, err := os.Stdout.Write(data)
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// parseInterspersed parses flags that appear before, between, or after the
// positional arguments, which the flag package alone stops at.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}
