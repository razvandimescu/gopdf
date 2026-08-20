package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/razvandimescu/gopdf/pdf"
)

// runDump extracts labels from the XFA template and writes them to a TSV.
// If the TSV already exists, existing EN translations are preserved (only
// new RO entries get appended).
func runDump(inPDF, outTSV string) error {
	data, err := os.ReadFile(inPDF)
	if err != nil {
		return err
	}
	r, err := pdf.Open(data)
	if err != nil {
		return err
	}
	objNum, xml := findXFATemplate(r)
	if objNum == 0 {
		return fmt.Errorf("no XFA template stream found — is this an XFA-dynamic PDF?")
	}

	labels := extractLabels(xml)

	// ro → en from a prior run, if any. Only a missing file is benign: os.Create
	// below truncates outTSV, so treating a read error as "no translations" would
	// silently destroy the accumulated work this function exists to preserve.
	existing, err := loadTSV(outTSV)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("reading %s: %w (refusing to overwrite it)", outTSV, err)
	}

	out, err := os.Create(outTSV)
	if err != nil {
		return err
	}
	defer out.Close()
	bw := bufio.NewWriter(out)
	defer bw.Flush()

	fmt.Fprintf(bw, "# XFA labels extracted from %s (obj %d, %d unique labels)\n",
		inPDF, objNum, len(labels))
	fmt.Fprintln(bw, "# columns: ro<TAB>en<TAB>context")
	fmt.Fprintln(bw, "# leave en empty to skip a label (preserves Romanian)")
	for _, l := range labels {
		en := existing[l.RO]
		fmt.Fprintf(bw, "%s\t%s\t%s\n", l.RO, en, l.Context)
	}

	carried := 0
	for _, l := range labels {
		if existing[l.RO] != "" {
			carried++
		}
	}
	fmt.Fprintf(os.Stderr, "wrote %d labels to %s (XFA template = obj %d, %d translations carried over)\n",
		len(labels), outTSV, objNum, carried)
	return nil
}
