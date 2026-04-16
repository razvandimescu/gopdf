package pdf

import (
	"crypto/sha256"
	"fmt"
	"os"
)

// OversizeBehavior controls what happens when a merge exceeds MaxSize.
type OversizeBehavior int

const (
	OversizeFail     OversizeBehavior = iota // return error with estimated size
	OversizeTruncate                         // include as many pages as fit
	OversizeShrink                           // deduplicate streams + strip metadata, error if still over
)

// MergeOptions configures size-constrained merging.
type MergeOptions struct {
	MaxSize          int64            // maximum output size in bytes; 0 = unlimited
	OversizeBehavior OversizeBehavior // action when limit is exceeded
}

// MergeResult holds the output of MergeWithOptions.
type MergeResult struct {
	Data          []byte // the merged PDF (nil when OversizeError is returned)
	TotalPages    int    // total pages across all sources
	IncludedPages int    // pages actually in the output
}

// OversizeError is returned when the merged PDF exceeds MaxSize.
type OversizeError struct {
	EstimatedSize int64
	MaxSize       int64
}

func (e *OversizeError) Error() string {
	return fmt.Sprintf("merged PDF estimated at %d bytes exceeds limit of %d bytes",
		e.EstimatedSize, e.MaxSize)
}

// MergeFiles merges PDF files by path, returning the combined PDF bytes.
func MergeFiles(paths ...string) ([]byte, error) {
	m := NewMerger()
	for _, p := range paths {
		if err := m.AddFile(p); err != nil {
			return nil, fmt.Errorf("adding %s: %w", p, err)
		}
	}
	return m.Merge()
}

// MergeBytes merges in-memory PDFs, returning the combined PDF bytes.
func MergeBytes(pdfs ...[]byte) ([]byte, error) {
	m := NewMerger()
	for i, data := range pdfs {
		if err := m.Add(data); err != nil {
			return nil, fmt.Errorf("adding PDF %d: %w", i, err)
		}
	}
	return m.Merge()
}

// Merger combines pages from multiple PDFs into a single document.
type Merger struct {
	sources []mergeSource
}

type mergeSource struct {
	reader *Reader
	pages  []int // 0-indexed; nil = all pages
}

// NewMerger creates an empty Merger.
func NewMerger() *Merger {
	return &Merger{}
}

// AddFile adds all pages (or specific pages) from a PDF file.
// Page indices are 0-based. If no pages specified, all pages are included.
func (m *Merger) AddFile(path string, pages ...int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return m.Add(data, pages...)
}

// Add adds all pages (or specific pages) from PDF bytes.
func (m *Merger) Add(data []byte, pages ...int) error {
	reader, err := Open(data)
	if err != nil {
		return fmt.Errorf("invalid PDF: %w", err)
	}
	var pageList []int
	if len(pages) > 0 {
		pageList = pages
	}
	m.sources = append(m.sources, mergeSource{reader: reader, pages: pageList})
	return nil
}

// Merge produces the combined PDF.
func (m *Merger) Merge() ([]byte, error) {
	res, err := m.MergeWithOptions(MergeOptions{})
	if err != nil {
		return nil, err
	}
	return res.Data, nil
}

type preparedSource struct {
	reader *Reader
	pages  []Dict
}

func (m *Merger) prepareSources() ([]preparedSource, int, error) {
	var prepared []preparedSource
	totalPages := 0
	for srcIdx, src := range m.sources {
		allPages, err := src.reader.Pages()
		if err != nil {
			return nil, 0, fmt.Errorf("source %d pages: %w", srcIdx, err)
		}
		var selected []Dict
		if src.pages == nil {
			selected = allPages
		} else {
			n := len(allPages)
			for _, idx := range src.pages {
				if idx < 0 {
					idx = n + idx
				}
				if idx < 0 || idx >= n {
					return nil, 0, fmt.Errorf("source %d: page %d out of range (0-%d)", srcIdx, idx, n-1)
				}
				selected = append(selected, allPages[idx])
			}
		}
		totalPages += len(selected)
		prepared = append(prepared, preparedSource{reader: src.reader, pages: selected})
	}
	return prepared, totalPages, nil
}

// MergeWithOptions produces the combined PDF with size constraints.
func (m *Merger) MergeWithOptions(opts MergeOptions) (*MergeResult, error) {
	if len(m.sources) == 0 {
		return nil, fmt.Errorf("no PDFs to merge")
	}

	prepared, totalPages, err := m.prepareSources()
	if err != nil {
		return nil, err
	}

	shrink := opts.MaxSize > 0 && opts.OversizeBehavior == OversizeShrink
	truncate := opts.MaxSize > 0 && opts.OversizeBehavior == OversizeTruncate

	w := NewWriter()
	pagesRef := w.AllocRef()
	catalogRef := w.AllocRef()

	var pageRefs []Ref
	var streamHash map[[32]byte]Ref
	if shrink {
		streamHash = make(map[[32]byte]Ref)
	}

	budgetExceeded := false
	for _, ps := range prepared {
		if budgetExceeded {
			break
		}

		ctx := &copyContext{
			reader:     ps.reader,
			writer:     w,
			refCache:   make(map[int]Ref),
			streamHash: streamHash,
			stripMeta:  shrink,
		}

		for _, pageDict := range ps.pages {
			var cp writerCheckpoint
			if truncate {
				cp = w.checkpoint()
			}

			copiedObj := ctx.copyObject(pageDict)
			copiedPage, ok := copiedObj.(Dict)
			if !ok {
				return nil, fmt.Errorf("copied page is not a Dict")
			}

			delete(copiedPage, "Parent")
			copiedPage["Parent"] = pagesRef
			if _, ok := copiedPage.Name("Type"); !ok {
				copiedPage["Type"] = Name("Page")
			}

			pageRef := w.AllocRef()
			if err := w.WriteObject(pageRef, copiedPage); err != nil {
				return nil, fmt.Errorf("writing page: %w", err)
			}

			if truncate {
				est := estimateMergeSize(w, len(pageRefs)+1)
				if est > opts.MaxSize {
					w.restore(cp)
					budgetExceeded = true
					break
				}
			}

			pageRefs = append(pageRefs, pageRef)
		}
	}

	// Truncate: not even one page fits.
	if budgetExceeded && len(pageRefs) == 0 {
		est := estimateMergeSize(w, 0)
		return &MergeResult{
			TotalPages:    totalPages,
			IncludedPages: 0,
		}, &OversizeError{EstimatedSize: est, MaxSize: opts.MaxSize}
	}

	// Fail / Shrink: full merge done, check size.
	if opts.MaxSize > 0 && !truncate {
		est := estimateMergeSize(w, len(pageRefs))
		if est > opts.MaxSize {
			return &MergeResult{
				TotalPages:    totalPages,
				IncludedPages: len(pageRefs),
			}, &OversizeError{EstimatedSize: est, MaxSize: opts.MaxSize}
		}
	}

	// Build the Pages node.
	kids := make(Array, len(pageRefs))
	for i, ref := range pageRefs {
		kids[i] = ref
	}
	pagesDict := Dict{
		"Type":  Name("Pages"),
		"Kids":  kids,
		"Count": len(pageRefs),
	}
	if err := w.WriteObject(pagesRef, pagesDict); err != nil {
		return nil, fmt.Errorf("writing Pages: %w", err)
	}

	catalogDict := Dict{
		"Type":  Name("Catalog"),
		"Pages": pagesRef,
	}
	if err := w.WriteObject(catalogRef, catalogDict); err != nil {
		return nil, fmt.Errorf("writing Catalog: %w", err)
	}

	data, err := w.Finish(catalogRef)
	if err != nil {
		return nil, err
	}

	return &MergeResult{
		Data:          data,
		TotalPages:    totalPages,
		IncludedPages: len(pageRefs),
	}, nil
}

// estimateMergeSize returns the estimated final PDF size given the current
// writer state and the number of page refs for the Kids array.
func estimateMergeSize(w *Writer, numPageRefs int) int64 {
	body := int64(w.buf.Len())
	// Unwritten objects: Pages dict, Catalog dict, Info dict.
	body += int64(80+numPageRefs*15) + 60 + 120
	// xref: header + 20 bytes per entry (+2 for Info + free entry 0).
	body += 15 + int64(w.nextObj+2)*20
	// trailer + startxref + %%EOF.
	body += 200
	return body
}

// copyContext tracks object remapping for a single source document.
type copyContext struct {
	reader     *Reader
	writer     *Writer
	refCache   map[int]Ref      // source obj num → new ref
	streamHash map[[32]byte]Ref // content hash → ref; shared across sources; nil = no dedup
	stripMeta  bool
}

var metadataKeys = map[Name]bool{
	"Metadata":       true,
	"StructTreeRoot": true,
	"PieceInfo":      true,
	"Thumb":          true,
	"MarkInfo":       true,
	"OutputIntents":  true,
}

// copyObject deep-copies a PDF object, remapping all Refs to new numbers.
func (ctx *copyContext) copyObject(obj any) any {
	switch v := obj.(type) {
	case Ref:
		if newRef, ok := ctx.refCache[v.Num]; ok {
			return newRef
		}

		resolved := ctx.reader.Resolve(v)
		if resolved == nil {
			newRef := ctx.writer.AllocRef()
			ctx.refCache[v.Num] = newRef
			ctx.writer.WriteObject(newRef, nil)
			return newRef
		}

		// Stream dedup: reuse byte-identical streams across sources.
		if stream, ok := resolved.(*Stream); ok && ctx.streamHash != nil {
			hash := sha256.Sum256(stream.Data)
			if existingRef, ok := ctx.streamHash[hash]; ok {
				ctx.refCache[v.Num] = existingRef
				return existingRef
			}
			newRef := ctx.writer.AllocRef()
			ctx.refCache[v.Num] = newRef
			ctx.copyStream(newRef, stream)
			ctx.streamHash[hash] = newRef
			return newRef
		}

		// Allocate before recursion to handle circular references.
		newRef := ctx.writer.AllocRef()
		ctx.refCache[v.Num] = newRef

		if stream, ok := resolved.(*Stream); ok {
			ctx.copyStream(newRef, stream)
			return newRef
		}

		copied := ctx.copyObject(resolved)
		ctx.writer.WriteObject(newRef, copied)
		return newRef

	case Dict:
		return ctx.copyDict(v)

	case Array:
		newArr := make(Array, len(v))
		for i, elem := range v {
			newArr[i] = ctx.copyObject(elem)
		}
		return newArr

	case Name, string, int, float64, bool, nil:
		return v

	default:
		return v
	}
}

// copyStream writes a deep-copied stream object to the writer.
func (ctx *copyContext) copyStream(ref Ref, stream *Stream) {
	copiedDict := ctx.copyDict(stream.Dict)
	if isPassthroughFilter(stream.Dict) {
		copiedDict["Length"] = len(stream.Data)
		ctx.writer.WriteObject(ref, &Stream{Dict: copiedDict, Data: stream.Data})
	} else {
		delete(copiedDict, "Filter")
		delete(copiedDict, "Length")
		delete(copiedDict, "DecodeParms")
		ctx.writer.WriteStream(ref, copiedDict, stream.Data)
	}
}

// isPassthroughFilter returns true if the stream uses a filter that our reader
// doesn't decode (images, etc.), meaning Data contains the raw encoded bytes
// and the original Filter must be preserved.
func isPassthroughFilter(d Dict) bool {
	var filters []Name
	if f, ok := d.Name("Filter"); ok {
		filters = []Name{f}
	} else if fa, ok := d.Array("Filter"); ok {
		for _, item := range fa {
			if n, ok := item.(Name); ok {
				filters = append(filters, n)
			}
		}
	}
	for _, f := range filters {
		switch f {
		case "DCTDecode", "JPXDecode", "CCITTFaxDecode", "JBIG2Decode":
			return true
		}
	}
	return false
}

func (ctx *copyContext) copyDict(d Dict) Dict {
	newDict := make(Dict, len(d))
	for k, v := range d {
		if k == "Parent" {
			continue
		}
		if ctx.stripMeta && metadataKeys[k] {
			continue
		}
		newDict[k] = ctx.copyObject(v)
	}
	return newDict
}
