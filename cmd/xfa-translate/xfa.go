package main

import (
	"bytes"
	"sort"

	"github.com/razvandimescu/gopdf/pdf"
)

const xfaTemplateMarker = "<template xmlns=\"http://www.xfa.org/schema/xfa-template/"

// findXFATemplate returns the object number and decoded bytes of the XFA
// template packet. It prefers the catalog's AcroForm/XFA array and falls back
// to scanning every indirect object for the template namespace marker, which
// covers files whose AcroForm linkage is missing or malformed. Returns 0/nil
// if no template is found.
func findXFATemplate(r *pdf.Reader) (int, []byte) {
	// The AcroForm entry is a claim, not a guarantee: generators exist that list
	// an empty "template" packet while the real content sits in xdp:xdp, and a
	// stream using a filter the reader lacks arrives here as compressed bytes,
	// since applyFilter passes unknown filters through untouched. Confirming the
	// marker keeps the fast path from handing runApply a stream it would then
	// rewrite over the real template.
	if objNum, data := findXFAStream(r, "template"); objNum != 0 &&
		containsMarker(data, []byte(xfaTemplateMarker)) {
		return objNum, data
	}

	xref := r.XRef()
	objNums := make([]int, 0, len(xref))
	for objNum := range xref {
		objNums = append(objNums, objNum)
	}
	sort.Ints(objNums) // reproducible when several streams carry the marker
	for _, objNum := range objNums {
		stream, ok := r.Resolve(pdf.Ref{Num: objNum}).(*pdf.Stream)
		if !ok {
			continue
		}
		if containsMarker(stream.Data, []byte(xfaTemplateMarker)) {
			return objNum, stream.Data
		}
	}
	return 0, nil
}

// findXFAStream returns the object number and decoded bytes of the named XFA
// packet (e.g. "template", "datasets", "form") from the catalog's AcroForm/XFA
// array. Returns 0/nil if the packet isn't present.
func findXFAStream(r *pdf.Reader, name string) (int, []byte) {
	rootRef, ok := r.Trailer().Ref("Root")
	if !ok {
		return 0, nil
	}
	root, ok := r.ResolveDict(rootRef)
	if !ok {
		return 0, nil
	}
	acroRef, ok := root.Ref("AcroForm")
	if !ok {
		return 0, nil
	}
	acro, ok := r.ResolveDict(acroRef)
	if !ok {
		return 0, nil
	}
	arr, ok := r.ResolveArray(acro["XFA"])
	if !ok {
		return 0, nil
	}
	for i := 0; i+1 < len(arr); i += 2 {
		s, ok := arr[i].(string)
		if !ok || s != name {
			continue
		}
		ref, ok := arr[i+1].(pdf.Ref)
		if !ok {
			continue
		}
		stream, ok := r.Resolve(ref).(*pdf.Stream)
		if !ok {
			continue
		}
		return ref.Num, stream.Data
	}
	return 0, nil
}

// containsMarker searches only the head of the stream — the template marker
// always appears near the start of the XFA template packet.
func containsMarker(data, marker []byte) bool {
	if len(data) > 4096 {
		data = data[:4096]
	}
	return bytes.Contains(data, marker)
}
