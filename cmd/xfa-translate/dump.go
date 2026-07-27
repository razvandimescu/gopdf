package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

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
	objNum, xml := r.FindXFATemplate()
	if objNum == 0 {
		return fmt.Errorf("no XFA template stream found — is this an XFA-dynamic PDF?")
	}

	labels := extractLabels(xml)

	existing := map[string]string{} // ro → en (from a prior run, if any)
	if f, err := os.Open(outTSV); err == nil {
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "#") || line == "" {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) >= 2 && parts[1] != "" {
				existing[parts[0]] = parts[1]
			}
		}
		f.Close()
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
