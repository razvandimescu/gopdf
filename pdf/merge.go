package pdf

import (
	"fmt"
	"os"
)

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
	data  []byte
	pages []int // 0-indexed; nil = all pages
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
	// Validate by opening.
	_, err := Open(data)
	if err != nil {
		return fmt.Errorf("invalid PDF: %w", err)
	}
	var pageList []int
	if len(pages) > 0 {
		pageList = pages
	}
	m.sources = append(m.sources, mergeSource{data: data, pages: pageList})
	return nil
}

// Merge produces the combined PDF.
func (m *Merger) Merge() ([]byte, error) {
	if len(m.sources) == 0 {
		return nil, fmt.Errorf("no PDFs to merge")
	}

	w := NewWriter()

	// Allocate the Pages and Catalog refs upfront (needed for /Parent).
	pagesRef := w.AllocRef()
	catalogRef := w.AllocRef()

	var pageRefs []Ref

	for srcIdx, src := range m.sources {
		reader, err := Open(src.data)
		if err != nil {
			return nil, fmt.Errorf("source %d: %w", srcIdx, err)
		}
		allPages, err := reader.Pages()
		if err != nil {
			return nil, fmt.Errorf("source %d pages: %w", srcIdx, err)
		}

		// Determine which pages to include.
		// Negative indices count from the end: -1 = last page, -2 = second-to-last.
		var selectedPages []Dict
		if src.pages == nil {
			selectedPages = allPages
		} else {
			n := len(allPages)
			for _, idx := range src.pages {
				if idx < 0 {
					idx = n + idx
				}
				if idx < 0 || idx >= n {
					return nil, fmt.Errorf("source %d: page %d out of range (0-%d)", srcIdx, idx, n-1)
				}
				selectedPages = append(selectedPages, allPages[idx])
			}
		}

		// Object copy context for this source.
		ctx := &copyContext{
			reader:   reader,
			writer:   w,
			refCache: make(map[int]Ref),
		}

		for _, pageDict := range selectedPages {
			// Deep-copy the page and all its dependencies.
			copiedObj := ctx.copyObject(pageDict)
			copiedPage, ok := copiedObj.(Dict)
			if !ok {
				return nil, fmt.Errorf("copied page is not a Dict")
			}

			// Fix /Parent to point to our new Pages node.
			delete(copiedPage, "Parent")
			copiedPage["Parent"] = pagesRef

			// Remove /Type if missing (some PDFs omit it on page dicts).
			if _, ok := copiedPage.Name("Type"); !ok {
				copiedPage["Type"] = Name("Page")
			}

			pageRef := w.AllocRef()
			if err := w.WriteObject(pageRef, copiedPage); err != nil {
				return nil, fmt.Errorf("writing page: %w", err)
			}
			pageRefs = append(pageRefs, pageRef)
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

	// Build the Catalog.
	catalogDict := Dict{
		"Type":  Name("Catalog"),
		"Pages": pagesRef,
	}
	if err := w.WriteObject(catalogRef, catalogDict); err != nil {
		return nil, fmt.Errorf("writing Catalog: %w", err)
	}

	return w.Finish(catalogRef)
}

// copyContext tracks object remapping for a single source document.
type copyContext struct {
	reader   *Reader
	writer   *Writer
	refCache map[int]Ref // source obj num → new ref
}

// copyObject deep-copies a PDF object, remapping all Refs to new numbers.
func (ctx *copyContext) copyObject(obj any) any {
	switch v := obj.(type) {
	case Ref:
		// Check cache first.
		if newRef, ok := ctx.refCache[v.Num]; ok {
			return newRef
		}
		// Allocate a new ref before resolving (handles circular refs).
		newRef := ctx.writer.AllocRef()
		ctx.refCache[v.Num] = newRef

		// Resolve from source and deep-copy.
		resolved := ctx.reader.Resolve(v)
		if resolved == nil {
			// Dead reference — write null.
			ctx.writer.WriteObject(newRef, nil)
			return newRef
		}

		// Handle streams specially.
		if stream, ok := resolved.(*Stream); ok {
			copiedDict := ctx.copyDict(stream.Dict)
			// Remove old filter/length — WriteStream will set new ones.
			delete(copiedDict, "Filter")
			delete(copiedDict, "Length")
			delete(copiedDict, "DecodeParms")
			ctx.writer.WriteStream(newRef, copiedDict, stream.Data)
			return newRef
		}

		// Non-stream object.
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

func (ctx *copyContext) copyDict(d Dict) Dict {
	newDict := make(Dict, len(d))
	for k, v := range d {
		// Skip /Parent — we set it ourselves.
		if k == "Parent" {
			continue
		}
		newDict[k] = ctx.copyObject(v)
	}
	return newDict
}
