package pdf

import (
	"bytes"
	"fmt"
	"sort"
)

// Rewrite produces a copy of the source PDF with the given stream objects'
// decoded data replaced. streamSubs maps source object numbers to new decoded
// bytes; each replacement is FlateDecode-compressed on write. Non-stream
// objects in the map are ignored. The original /ID is preserved so Adobe
// Acrobat doesn't flag the output as modified.
func (r *Reader) Rewrite(streamSubs map[int][]byte) ([]byte, error) {
	rootRef, ok := r.trailer.Ref("Root")
	if !ok {
		return nil, fmt.Errorf("no Root in trailer")
	}
	rootDict, ok := r.ResolveDict(rootRef)
	if !ok {
		return nil, fmt.Errorf("root is not a dict")
	}

	w := NewWriter()
	catalogRef := w.AllocRef()
	ctx := &copyContext{
		reader:         r,
		writer:         w,
		refCache:       map[int]Ref{rootRef.Num: catalogRef},
		streamSubs:     streamSubs,
		preserveParent: true,
	}
	copied := ctx.copyDict(rootDict)
	if err := w.WriteObject(catalogRef, copied); err != nil {
		return nil, fmt.Errorf("writing catalog: %w", err)
	}
	return w.FinishWithID(catalogRef, r.OriginalID())
}

// FindXFATemplate scans all indirect objects looking for the XFA template
// stream (identified by its decoded XML containing the XFA template
// namespace). Objects are scanned in ascending number order, so the result is
// reproducible when more than one stream carries the marker. Returns the
// object number and decoded bytes, or 0/nil if not found.
func (r *Reader) FindXFATemplate() (int, []byte) {
	const marker = "<template xmlns=\"http://www.xfa.org/schema/xfa-template/"
	objNums := make([]int, 0, len(r.xref))
	for objNum := range r.xref {
		objNums = append(objNums, objNum)
	}
	sort.Ints(objNums)
	for _, objNum := range objNums {
		obj := r.Resolve(Ref{Num: objNum})
		stream, ok := obj.(*Stream)
		if !ok {
			continue
		}
		if len(stream.Data) < len(marker) {
			continue
		}
		if containsBytes(stream.Data, []byte(marker)) {
			return objNum, stream.Data
		}
	}
	return 0, nil
}

// FindXFAStream returns the object number and decoded bytes of the named XFA
// packet (e.g. "template", "datasets", "form") from the catalog's
// AcroForm/XFA array. Returns 0/nil if the packet isn't present.
func (r *Reader) FindXFAStream(name string) (int, []byte) {
	rootRef, ok := r.trailer.Ref("Root")
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
		ref, ok := arr[i+1].(Ref)
		if !ok {
			continue
		}
		stream, ok := r.Resolve(ref).(*Stream)
		if !ok {
			continue
		}
		return ref.Num, stream.Data
	}
	return 0, nil
}

func containsBytes(haystack, needle []byte) bool {
	// Limit search to a head window — the template marker is always near the
	// start of the XFA template stream.
	if len(haystack) > 4096 {
		haystack = haystack[:4096]
	}
	return bytes.Contains(haystack, needle)
}
