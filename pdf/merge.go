package pdf

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image/jpeg"
	"os"
)

// OversizeBehavior controls what happens when a merge exceeds MaxSize.
type OversizeBehavior int

const (
	OversizeFail     OversizeBehavior = iota // return error with raw (unoptimized) size
	OversizeTruncate                         // dedup + strip + JPEG recompress + include as many pages as fit
	OversizeShrink                           // dedup + strip + JPEG recompress to hit target, error if still over
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
	Size    int64 // actual or estimated output size
	MaxSize int64
}

func (e *OversizeError) Error() string {
	return fmt.Sprintf("merged PDF size %d bytes exceeds limit of %d bytes",
		e.Size, e.MaxSize)
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

// mergeConfig controls a single build pass.
type mergeConfig struct {
	optimize     bool  // dedup + metadata stripping
	imageQuality int   // JPEG recompression quality (0 = passthrough, 1-100 = re-encode)
	maxSize      int64 // truncation limit (0 = no truncation)
}

// buildMergedPDF runs one merge pass and returns the finished PDF bytes.
// Returns (nil, 0, nil) when truncation results in zero pages.
func buildMergedPDF(prepared []preparedSource, cfg mergeConfig) ([]byte, int, error) {
	w := NewWriter()
	pagesRef := w.AllocRef()
	catalogRef := w.AllocRef()

	var pageRefs []Ref
	var streamHash map[[32]byte]Ref
	if cfg.optimize {
		streamHash = make(map[[32]byte]Ref)
	}

	budgetExceeded := false
	for _, ps := range prepared {
		if budgetExceeded {
			break
		}
		ctx := &copyContext{
			reader:       ps.reader,
			writer:       w,
			refCache:     make(map[int]Ref),
			streamHash:   streamHash,
			stripMeta:    cfg.optimize,
			imageQuality: cfg.imageQuality,
		}
		for _, pageDict := range ps.pages {
			var cp writerCheckpoint
			if cfg.maxSize > 0 {
				cp = w.checkpoint()
			}

			copiedObj := ctx.copyObject(pageDict)
			copiedPage, ok := copiedObj.(Dict)
			if !ok {
				return nil, 0, fmt.Errorf("copied page is not a Dict")
			}

			delete(copiedPage, "Parent")
			copiedPage["Parent"] = pagesRef
			if _, ok := copiedPage.Name("Type"); !ok {
				copiedPage["Type"] = Name("Page")
			}

			pageRef := w.AllocRef()
			if err := w.WriteObject(pageRef, copiedPage); err != nil {
				return nil, 0, fmt.Errorf("writing page: %w", err)
			}

			if cfg.maxSize > 0 {
				est := w.estimateFinishedSize(len(pageRefs) + 1)
				if est > cfg.maxSize {
					w.restore(cp)
					budgetExceeded = true
					break
				}
			}

			pageRefs = append(pageRefs, pageRef)
		}
	}

	if len(pageRefs) == 0 {
		return nil, 0, nil
	}

	kids := make(Array, len(pageRefs))
	for i, ref := range pageRefs {
		kids[i] = ref
	}
	if err := w.WriteObject(pagesRef, Dict{
		"Type":  Name("Pages"),
		"Kids":  kids,
		"Count": len(pageRefs),
	}); err != nil {
		return nil, 0, fmt.Errorf("writing Pages: %w", err)
	}
	if err := w.WriteObject(catalogRef, Dict{
		"Type":  Name("Catalog"),
		"Pages": pagesRef,
	}); err != nil {
		return nil, 0, fmt.Errorf("writing Catalog: %w", err)
	}

	data, err := w.FinishWithID(catalogRef, prepared[0].reader.OriginalID())
	if err != nil {
		return nil, 0, err
	}
	return data, len(pageRefs), nil
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

	result := func(data []byte, n int) (*MergeResult, error) {
		return &MergeResult{Data: data, TotalPages: totalPages, IncludedPages: n}, nil
	}
	oversizeErr := func(size int64, n int) (*MergeResult, error) {
		return &MergeResult{TotalPages: totalPages, IncludedPages: n},
			&OversizeError{Size: size, MaxSize: opts.MaxSize}
	}

	// No size limit: plain merge.
	if opts.MaxSize <= 0 {
		data, n, err := buildMergedPDF(prepared, mergeConfig{})
		if err != nil {
			return nil, err
		}
		return result(data, n)
	}

	switch opts.OversizeBehavior {
	case OversizeFail:
		data, n, err := buildMergedPDF(prepared, mergeConfig{})
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > opts.MaxSize {
			return oversizeErr(int64(len(data)), n)
		}
		return result(data, n)

	case OversizeTruncate:
		data, n, err := buildMergedPDF(prepared, mergeConfig{
			optimize: true,
			maxSize:  opts.MaxSize,
		})
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return oversizeErr(0, 0)
		}
		return result(data, n)

	case OversizeShrink:
		// Pass 1: lossless (dedup + metadata strip).
		data, n, err := buildMergedPDF(prepared, mergeConfig{optimize: true})
		if err != nil {
			return nil, err
		}
		if int64(len(data)) <= opts.MaxSize {
			return result(data, n)
		}

		// Pass 2: JPEG recompression at ratio-derived quality.
		ratio := float64(opts.MaxSize) / float64(len(data))
		quality := int(ratio * 85)
		if quality < 10 {
			quality = 10
		}
		if quality > 85 {
			quality = 85
		}
		data, n, err = buildMergedPDF(prepared, mergeConfig{optimize: true, imageQuality: quality})
		if err != nil {
			return nil, err
		}
		if int64(len(data)) <= opts.MaxSize {
			return result(data, n)
		}
		return oversizeErr(int64(len(data)), n)
	}

	return nil, fmt.Errorf("unknown oversize behavior: %d", opts.OversizeBehavior)
}

// filterNames extracts the filter name(s) from a stream dict's /Filter entry.
func filterNames(d Dict) []Name {
	if f, ok := d.Name("Filter"); ok {
		return []Name{f}
	}
	if fa, ok := d.Array("Filter"); ok {
		var names []Name
		for _, item := range fa {
			if n, ok := item.(Name); ok {
				names = append(names, n)
			}
		}
		return names
	}
	return nil
}

// copyContext tracks object remapping for a single source document.
type copyContext struct {
	reader       *Reader
	writer       *Writer
	refCache     map[int]Ref      // source obj num → new ref
	streamHash   map[[32]byte]Ref // content hash → ref; shared across sources; nil = no dedup
	stripMeta    bool
	imageQuality int // JPEG recompression quality; 0 = passthrough
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
		data := stream.Data
		if ctx.imageQuality > 0 && isDCTDecode(stream.Dict) {
			if recompressed := recompressJPEG(data, ctx.imageQuality); recompressed != nil {
				data = recompressed
			}
		}
		copiedDict["Length"] = len(data)
		ctx.writer.WriteObject(ref, &Stream{Dict: copiedDict, Data: data})
	} else {
		delete(copiedDict, "Filter")
		delete(copiedDict, "Length")
		delete(copiedDict, "DecodeParms")
		ctx.writer.WriteStream(ref, copiedDict, stream.Data)
	}
}

// recompressJPEG decodes and re-encodes JPEG data at the given quality.
// Returns nil if recompression fails, produces a larger result, or the
// input is too small to benefit (<4KB).
func recompressJPEG(data []byte, quality int) []byte {
	if len(data) < 4096 {
		return nil
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil
	}
	if buf.Len() >= len(data) {
		return nil // recompression didn't help
	}
	return buf.Bytes()
}

func isDCTDecode(d Dict) bool {
	filters := filterNames(d)
	return len(filters) > 0 && filters[0] == "DCTDecode"
}

// isPassthroughFilter returns true if the stream uses a filter that our reader
// doesn't decode (images, etc.), meaning Data contains the raw encoded bytes
// and the original Filter must be preserved.
func isPassthroughFilter(d Dict) bool {
	for _, f := range filterNames(d) {
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
