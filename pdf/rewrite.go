package pdf

import "fmt"

// Rewrite produces a copy of the source PDF with the given stream objects'
// decoded data replaced. streamSubs maps source object numbers to new decoded
// bytes; each replacement is FlateDecode-compressed on write. Non-stream
// objects in the map are ignored. The original creation /ID is preserved so
// Acrobat does not treat the output as a different document; the modification
// half is regenerated, since it did change.
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
		reader:     r,
		writer:     w,
		refCache:   map[int]Ref{rootRef.Num: catalogRef},
		streamSubs: streamSubs,
		fullClone:  true,
	}
	copied := ctx.copyDict(rootDict)
	if err := w.WriteObject(catalogRef, copied); err != nil {
		return nil, fmt.Errorf("writing catalog: %w", err)
	}
	return w.FinishWithID(catalogRef, r.OriginalID())
}
