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
	{"merge", "combine PDFs and images (PNG/JPEG) into one PDF", runMerge},
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

func humanSize(n int) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for size := int64(n) / unit; size >= unit; size /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
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
