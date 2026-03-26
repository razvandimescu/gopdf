package pdf

import (
	"bytes"
	"compress/lzw"
	"compress/zlib"
	"encoding/ascii85"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// compressedRef records an object stored inside an ObjStm.
type compressedRef struct {
	StreamObj int // object number of the containing ObjStm
	Index     int // index within the ObjStm
}

// Reader reads and resolves objects from a PDF file.
type Reader struct {
	data       []byte
	xref       map[int]int64          // object number → byte offset (type 1)
	compressed map[int]compressedRef  // object number → ObjStm ref (type 2)
	trailer    Dict
	cache      map[int]any
}

// Open parses a PDF from raw bytes.
func Open(data []byte) (*Reader, error) {
	r := &Reader{
		data:       data,
		xref:       make(map[int]int64),
		compressed: make(map[int]compressedRef),
		cache:      make(map[int]any),
	}
	if err := r.parseXRef(); err != nil {
		return nil, fmt.Errorf("parsing xref: %w", err)
	}
	return r, nil
}

// parseXRef locates and reads the xref table and trailer.
func (r *Reader) parseXRef() error {
	// Find startxref near end of file.
	tail := r.data
	if len(tail) > 1024 {
		tail = tail[len(tail)-1024:]
	}
	idx := bytes.LastIndex(tail, []byte("startxref"))
	if idx < 0 {
		return fmt.Errorf("startxref not found")
	}
	// The offset might be relative to tail, recalculate.
	startxrefPos := len(r.data) - len(tail) + idx
	s := string(r.data[startxrefPos:])

	// Parse "startxref\r?\n<offset>".
	s = s[len("startxref"):]
	s = strings.TrimLeft(s, " \t\r\n")
	// Read digits.
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return fmt.Errorf("no offset after startxref")
	}
	offset, err := strconv.ParseInt(s[:end], 10, 64)
	if err != nil {
		return fmt.Errorf("parsing startxref offset: %w", err)
	}

	return r.readXRefAt(int(offset))
}

func (r *Reader) readXRefAt(pos int) error {
	// Check if it's a traditional xref table or an xref stream.
	lex := NewLexer(r.data)
	lex.SetPos(pos)
	lex.skipWhitespaceAndComments()

	// Read first keyword.
	tok, err := lex.NextToken()
	if err != nil {
		return err
	}

	if tok.Type == TKeyword && tok.Str == "xref" {
		return r.readTraditionalXRef(lex)
	}

	// Might be an xref stream (PDF 1.5+): N G obj << ... >> stream
	if tok.Type == TNumber && tok.IsInt {
		return r.readXRefStream(pos)
	}

	return fmt.Errorf("unexpected token at xref position: %q", tok.Str)
}

func (r *Reader) readTraditionalXRef(lex *Lexer) error {
	// Parse xref subsections.
	for {
		lex.skipWhitespaceAndComments()
		if lex.AtEnd() {
			break
		}

		// Peek for "trailer" keyword.
		savedPos := lex.Pos()
		tok, err := lex.NextToken()
		if err != nil {
			return err
		}
		if tok.Type == TKeyword && tok.Str == "trailer" {
			break
		}
		lex.SetPos(savedPos)

		// Read subsection header: startObj count
		tok, err = lex.NextToken()
		if err != nil {
			return err
		}
		if tok.Type != TNumber {
			return fmt.Errorf("expected subsection start number, got %q", tok.Str)
		}
		startObj := tok.Int

		tok, err = lex.NextToken()
		if err != nil {
			return err
		}
		if tok.Type != TNumber {
			return fmt.Errorf("expected subsection count, got %q", tok.Str)
		}
		count := tok.Int

		// Read entries: offset gen f/n
		for i := 0; i < count; i++ {
			tok1, err := lex.NextToken()
			if err != nil {
				return err
			}
			tok2, err := lex.NextToken()
			if err != nil {
				return err
			}
			tok3, err := lex.NextToken()
			if err != nil {
				return err
			}

			objNum := startObj + i
			if tok3.Str == "n" {
				offset, _ := strconv.ParseInt(tok1.Str, 10, 64)
				if _, exists := r.xref[objNum]; !exists {
					r.xref[objNum] = offset
				}
			}
			_ = tok2 // gen number, not needed for basic reading
		}
	}

	// Parse trailer dictionary.
	parser := &Parser{lex: lex}
	obj, err := parser.ParseObject()
	if err != nil {
		return fmt.Errorf("parsing trailer dict: %w", err)
	}
	d, ok := obj.(Dict)
	if !ok {
		return fmt.Errorf("trailer is not a dict")
	}

	if r.trailer == nil {
		r.trailer = d
	}

	// Follow Prev link for incremental updates.
	if prev, ok := d.Int("Prev"); ok {
		return r.readXRefAt(prev)
	}

	return nil
}

func (r *Reader) readXRefStream(pos int) error {
	// Parse the xref stream object.
	lex := NewLexer(r.data)
	lex.SetPos(pos)
	parser := &Parser{lex: lex}

	// Read: objNum gen obj
	tok, _ := parser.lex.NextToken() // objNum
	objNum := tok.Int
	parser.lex.NextToken() // gen
	parser.lex.NextToken() // "obj"

	// Parse the stream dict.
	obj, err := parser.ParseObject()
	if err != nil {
		return fmt.Errorf("parsing xref stream dict: %w", err)
	}
	d, ok := obj.(Dict)
	if !ok {
		return fmt.Errorf("xref stream object is not a dict")
	}

	// Read the stream data.
	streamData, err := r.readStreamData(lex, d)
	if err != nil {
		return fmt.Errorf("reading xref stream data: %w", err)
	}

	if r.trailer == nil {
		r.trailer = d
	}

	// Parse W array for field widths.
	wArr, ok := d.Array("W")
	if !ok || len(wArr) < 3 {
		return fmt.Errorf("xref stream missing W array")
	}
	w := [3]int{asInt(wArr[0]), asInt(wArr[1]), asInt(wArr[2])}
	entrySize := w[0] + w[1] + w[2]

	// Parse Size.
	size, _ := d.Int("Size")

	// Parse Index array (default: [0 Size]).
	indexArr, hasIndex := d.Array("Index")
	if !hasIndex {
		indexArr = Array{0, size}
	}

	_ = objNum
	offset := 0
	for i := 0; i+1 < len(indexArr); i += 2 {
		startObj := asInt(indexArr[i])
		count := asInt(indexArr[i+1])
		for j := 0; j < count; j++ {
			if offset+entrySize > len(streamData) {
				break
			}
			field := func(width int) int64 {
				var val int64
				for k := 0; k < width; k++ {
					val = val<<8 | int64(streamData[offset])
					offset++
				}
				return val
			}

			typ := int64(1) // default type
			if w[0] > 0 {
				typ = field(w[0])
			}
			f2 := field(w[1])
			f3 := field(w[2])

			num := startObj + j
			switch typ {
			case 1: // in-use, uncompressed
				if _, exists := r.xref[num]; !exists {
					r.xref[num] = f2
				}
			case 2: // compressed in object stream
				if _, exists := r.compressed[num]; !exists {
					r.compressed[num] = compressedRef{
						StreamObj: int(f2),
						Index:     int(f3),
					}
				}
			}
		}
	}

	// Follow Prev.
	if prev, ok := d.Int("Prev"); ok {
		return r.readXRefAt(prev)
	}

	return nil
}

// readStreamData reads raw stream bytes after a dict, handling FlateDecode.
func (r *Reader) readStreamData(lex *Lexer, d Dict) ([]byte, error) {
	// Expect "stream" keyword.
	lex.skipWhitespaceAndComments()
	pos := lex.Pos()

	// Find "stream" keyword followed by EOL.
	idx := bytes.Index(r.data[pos:], []byte("stream"))
	if idx < 0 {
		return nil, fmt.Errorf("stream keyword not found")
	}
	dataStart := pos + idx + len("stream")
	// Skip the EOL after "stream".
	if dataStart < len(r.data) && r.data[dataStart] == '\r' {
		dataStart++
	}
	if dataStart < len(r.data) && r.data[dataStart] == '\n' {
		dataStart++
	}

	// Resolve indirect Length references (e.g. /Length 42 0 R).
	var length int
	var ok bool
	if lengthObj, has := d["Length"]; has {
		resolved := r.Resolve(lengthObj)
		switch v := resolved.(type) {
		case int:
			length, ok = v, true
		case float64:
			length, ok = int(v), true
		}
	}
	if !ok {
		// Try to find endstream.
		endIdx := bytes.Index(r.data[dataStart:], []byte("endstream"))
		if endIdx < 0 {
			return nil, fmt.Errorf("cannot determine stream length")
		}
		length = endIdx
		// Trim trailing whitespace before endstream.
		for length > 0 && (r.data[dataStart+length-1] == '\r' || r.data[dataStart+length-1] == '\n') {
			length--
		}
	}

	raw := r.data[dataStart : dataStart+length]

	// Build filter chain.
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

	// Build matching DecodeParms chain.
	var parmsList []Dict
	if dp, ok := d.Dict("DecodeParms"); ok {
		parmsList = []Dict{dp}
	} else if dpa, ok := d.Array("DecodeParms"); ok {
		for _, item := range dpa {
			if dd, ok := item.(Dict); ok {
				parmsList = append(parmsList, dd)
			} else {
				parmsList = append(parmsList, nil)
			}
		}
	}

	// Apply filters in order.
	data := raw
	for i, f := range filters {
		var parms Dict
		if i < len(parmsList) {
			parms = parmsList[i]
		}
		var err error
		data, err = applyFilter(data, f, parms)
		if err != nil {
			return nil, fmt.Errorf("filter %s: %w", f, err)
		}
	}
	return data, nil
}

func applyFilter(data []byte, filter Name, parms Dict) ([]byte, error) {
	switch filter {
	case "FlateDecode":
		decoded, err := decompress(data)
		if err != nil {
			return nil, err
		}
		if parms != nil {
			return applyPredictorWithParms(decoded, parms)
		}
		return decoded, nil

	case "LZWDecode":
		r := lzw.NewReader(bytes.NewReader(data), lzw.MSB, 8)
		decoded, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			return nil, fmt.Errorf("lzw: %w", err)
		}
		if parms != nil {
			return applyPredictorWithParms(decoded, parms)
		}
		return decoded, nil

	case "ASCII85Decode":
		// Strip ~> end marker if present.
		s := data
		if idx := bytes.Index(s, []byte("~>")); idx >= 0 {
			s = s[:idx]
		}
		dst := make([]byte, 4*len(s))
		n, _, err := ascii85.Decode(dst, s, true)
		if err != nil {
			return nil, fmt.Errorf("ascii85: %w", err)
		}
		return dst[:n], nil

	case "ASCIIHexDecode":
		return decodeASCIIHex(data)

	default:
		// Unknown filter — return as-is (e.g. DCTDecode for images).
		return data, nil
	}
}

func decompress(data []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("zlib init: %w", err)
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

func applyPredictorWithParms(data []byte, dp Dict) ([]byte, error) {
	predictor, _ := dp.Int("Predictor")
	if predictor <= 1 {
		return data, nil
	}

	columns, _ := dp.Int("Columns")
	if columns == 0 {
		columns = 1
	}

	if predictor >= 10 {
		return pngUnpredict(data, columns)
	}

	// TIFF predictor 2 — not yet implemented.
	return data, nil
}

func decodeASCIIHex(data []byte) ([]byte, error) {
	var buf []byte
	var hi byte
	haveHi := false
	for _, b := range data {
		if b == '>' {
			break
		}
		if isWhitespace(b) {
			continue
		}
		var nib byte
		switch {
		case b >= '0' && b <= '9':
			nib = b - '0'
		case b >= 'a' && b <= 'f':
			nib = b - 'a' + 10
		case b >= 'A' && b <= 'F':
			nib = b - 'A' + 10
		default:
			continue
		}
		if !haveHi {
			hi = nib
			haveHi = true
		} else {
			buf = append(buf, hi<<4|nib)
			haveHi = false
		}
	}
	if haveHi {
		buf = append(buf, hi<<4)
	}
	return buf, nil
}

// pngUnpredict reverses PNG row filters.
func pngUnpredict(data []byte, columns int) ([]byte, error) {
	rowSize := columns + 1 // +1 for filter byte
	if len(data)%rowSize != 0 {
		// Try without assuming filter byte (some PDFs).
		if len(data)%columns == 0 {
			return data, nil
		}
		return nil, fmt.Errorf("PNG predictor: data len %d not divisible by row size %d", len(data), rowSize)
	}

	nRows := len(data) / rowSize
	out := make([]byte, 0, nRows*columns)
	prev := make([]byte, columns)

	for row := 0; row < nRows; row++ {
		offset := row * rowSize
		filterByte := data[offset]
		rowData := data[offset+1 : offset+rowSize]
		current := make([]byte, columns)

		switch filterByte {
		case 0: // None
			copy(current, rowData)
		case 1: // Sub
			for i := 0; i < columns; i++ {
				left := byte(0)
				if i > 0 {
					left = current[i-1]
				}
				current[i] = rowData[i] + left
			}
		case 2: // Up
			for i := 0; i < columns; i++ {
				current[i] = rowData[i] + prev[i]
			}
		case 3: // Average
			for i := 0; i < columns; i++ {
				left := byte(0)
				if i > 0 {
					left = current[i-1]
				}
				current[i] = rowData[i] + byte((int(left)+int(prev[i]))/2)
			}
		case 4: // Paeth
			for i := 0; i < columns; i++ {
				left := byte(0)
				if i > 0 {
					left = current[i-1]
				}
				up := prev[i]
				upLeft := byte(0)
				if i > 0 {
					upLeft = prev[i-1]
				}
				current[i] = rowData[i] + paethPredictor(left, up, upLeft)
			}
		default:
			copy(current, rowData)
		}

		out = append(out, current...)
		copy(prev, current)
	}

	return out, nil
}

func paethPredictor(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa := abs(p - int(a))
	pb := abs(p - int(b))
	pc := abs(p - int(c))
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Resolve dereferences a Ref to its underlying object.
// Non-Ref values are returned as-is.
func (r *Reader) Resolve(obj any) any {
	ref, ok := obj.(Ref)
	if !ok {
		return obj
	}
	if cached, ok := r.cache[ref.Num]; ok {
		return cached
	}

	// Check for compressed object first.
	if cref, ok := r.compressed[ref.Num]; ok {
		parsed := r.resolveFromObjStm(cref)
		if parsed != nil {
			r.cache[ref.Num] = parsed
			return parsed
		}
	}

	offset, ok := r.xref[ref.Num]
	if !ok {
		return nil
	}
	parsed, err := r.parseObjectAt(int(offset))
	if err != nil {
		return nil
	}
	r.cache[ref.Num] = parsed
	return parsed
}

// resolveFromObjStm extracts an object from a compressed object stream.
func (r *Reader) resolveFromObjStm(cref compressedRef) any {
	// First, resolve the ObjStm itself (it must be a regular stream).
	stmObj := r.Resolve(Ref{Num: cref.StreamObj})
	stream, ok := stmObj.(*Stream)
	if !ok {
		return nil
	}

	stmDict := stream.Dict
	n, _ := stmDict.Int("N")
	first, _ := stmDict.Int("First")

	if n == 0 || first == 0 {
		return nil
	}

	// Parse the header: N pairs of (objNum byteOffset).
	parser := NewParser(stream.Data)

	type objEntry struct {
		Num    int
		Offset int
	}
	entries := make([]objEntry, n)
	for i := 0; i < n; i++ {
		numObj, err := parser.ParseObject()
		if err != nil {
			return nil
		}
		offObj, err := parser.ParseObject()
		if err != nil {
			return nil
		}
		entries[i] = objEntry{Num: asInt(numObj), Offset: asInt(offObj)}
	}

	// Parse the object at the given index.
	if cref.Index >= len(entries) {
		return nil
	}

	objOffset := first + entries[cref.Index].Offset
	p := NewParser(stream.Data[objOffset:])
	obj, err := p.ParseObject()
	if err != nil {
		return nil
	}

	// Cache all objects from this ObjStm while we're at it.
	for i, entry := range entries {
		if _, exists := r.cache[entry.Num]; exists {
			continue
		}
		off := first + entry.Offset
		pp := NewParser(stream.Data[off:])
		o, err := pp.ParseObject()
		if err != nil {
			continue
		}
		r.cache[entry.Num] = o
		_ = i
	}

	return obj
}

// ResolveDict resolves and type-asserts to Dict.
func (r *Reader) ResolveDict(obj any) (Dict, bool) {
	v := r.Resolve(obj)
	d, ok := v.(Dict)
	return d, ok
}

// ResolveArray resolves and type-asserts to Array.
func (r *Reader) ResolveArray(obj any) (Array, bool) {
	v := r.Resolve(obj)
	a, ok := v.(Array)
	return a, ok
}

func (r *Reader) parseObjectAt(pos int) (any, error) {
	lex := NewLexer(r.data)
	lex.SetPos(pos)
	parser := &Parser{lex: lex}

	// Read "N G obj".
	parser.lex.NextToken() // N
	parser.lex.NextToken() // G
	tok, err := parser.lex.NextToken()
	if err != nil {
		return nil, err
	}
	if tok.Type != TKeyword || tok.Str != "obj" {
		return nil, fmt.Errorf("expected 'obj', got %q", tok.Str)
	}

	obj, err := parser.ParseObject()
	if err != nil {
		return nil, err
	}

	// Check for stream.
	if d, ok := obj.(Dict); ok {
		lex.skipWhitespaceAndComments()
		savedPos := lex.Pos()
		// Check for "stream" keyword.
		if savedPos+6 <= len(r.data) && string(r.data[savedPos:savedPos+6]) == "stream" {
			data, err := r.readStreamData(lex, d)
			if err != nil {
				return nil, err
			}
			return &Stream{Dict: d, Data: data}, nil
		}
	}

	return obj, nil
}

// Trailer returns the trailer dictionary.
func (r *Reader) Trailer() Dict { return r.trailer }

// XRef returns the cross-reference table (for debugging).
func (r *Reader) XRef() map[int]int64 { return r.xref }

// Pages returns all page dictionaries in order.
func (r *Reader) Pages() ([]Dict, error) {
	root, ok := r.trailer.Ref("Root")
	if !ok {
		return nil, fmt.Errorf("no Root in trailer")
	}
	catalog, ok := r.ResolveDict(root)
	if !ok {
		return nil, fmt.Errorf("Root is not a dict")
	}
	pagesRef, ok := catalog["Pages"]
	if !ok {
		return nil, fmt.Errorf("no Pages in catalog")
	}
	var pages []Dict
	r.collectPages(pagesRef, &pages, make(Dict))
	return pages, nil
}

// inheritableKeys are page attributes inherited from ancestor Pages nodes (PDF spec 7.7.3.4).
var inheritableKeys = []Name{"Resources", "MediaBox", "CropBox", "Rotate"}

func (r *Reader) collectPages(obj any, pages *[]Dict, inherited Dict) {
	d, ok := r.ResolveDict(obj)
	if !ok {
		return
	}

	// Build merged inheritable attributes: child overrides parent.
	merged := make(Dict)
	for k, v := range inherited {
		merged[k] = v
	}
	for _, key := range inheritableKeys {
		if v, ok := d[key]; ok {
			merged[key] = v
		}
	}

	typ, _ := d.Name("Type")
	switch typ {
	case "Pages":
		kids, ok := d["Kids"]
		if !ok {
			return
		}
		arr, ok := r.ResolveArray(kids)
		if !ok {
			return
		}
		for _, kid := range arr {
			r.collectPages(kid, pages, merged)
		}
	case "Page":
		// Apply inherited attributes the page doesn't define itself.
		for _, key := range inheritableKeys {
			if _, ok := d[key]; !ok {
				if v, ok := merged[key]; ok {
					d[key] = v
				}
			}
		}
		*pages = append(*pages, d)
	}
}

// PageContent returns the decompressed content stream(s) for a page.
func (r *Reader) PageContent(page Dict) ([]byte, error) {
	contents, ok := page["Contents"]
	if !ok {
		return nil, nil
	}

	switch c := r.Resolve(contents).(type) {
	case *Stream:
		return c.Data, nil
	case Array:
		// Multiple content streams — concatenate.
		var buf bytes.Buffer
		for _, item := range c {
			resolved := r.Resolve(item)
			if s, ok := resolved.(*Stream); ok {
				buf.Write(s.Data)
				buf.WriteByte('\n')
			}
		}
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("unexpected Contents type: %T", c)
	}
}

// PageFonts returns the font dictionary for a page (from Resources).
func (r *Reader) PageFonts(page Dict) map[Name]Dict {
	return r.fontsFromDict(page)
}

// PageResources returns the resolved Resources dict for a page.
func (r *Reader) PageResources(page Dict) Dict {
	res, ok := page["Resources"]
	if !ok {
		return nil
	}
	resDict, ok := r.ResolveDict(res)
	if !ok {
		return nil
	}
	return resDict
}

func (r *Reader) fontsFromDict(d Dict) map[Name]Dict {
	fonts := make(map[Name]Dict)
	res, ok := d["Resources"]
	if !ok {
		return fonts
	}
	resDict, ok := r.ResolveDict(res)
	if !ok {
		return fonts
	}
	fontObj, ok := resDict["Font"]
	if !ok {
		return fonts
	}
	fontDict, ok := r.ResolveDict(fontObj)
	if !ok {
		return fonts
	}
	for name, ref := range fontDict {
		if fd, ok := r.ResolveDict(ref); ok {
			fonts[name] = fd
		}
	}
	return fonts
}

// ToUnicodeMap parses a ToUnicode CMap for a font, returning charcode → string mapping.
func (r *Reader) ToUnicodeMap(font Dict) map[uint16]string {
	touObj, ok := font["ToUnicode"]
	if !ok {
		return nil
	}
	resolved := r.Resolve(touObj)
	stream, ok := resolved.(*Stream)
	if !ok {
		return nil
	}
	return parseCMap(stream.Data)
}

// parseCMap extracts character-to-unicode mappings from a CMap stream.
func parseCMap(data []byte) map[uint16]string {
	m := make(map[uint16]string)
	s := string(data)

	// Parse bfchar sections.
	for {
		idx := strings.Index(s, "beginbfchar")
		if idx < 0 {
			break
		}
		s = s[idx+len("beginbfchar"):]
		endIdx := strings.Index(s, "endbfchar")
		if endIdx < 0 {
			break
		}
		section := s[:endIdx]
		s = s[endIdx:]

		// Each mapping: <src> <dst>
		parseBfEntries(section, m)
	}

	// Parse bfrange sections.
	s = string(data)
	for {
		idx := strings.Index(s, "beginbfrange")
		if idx < 0 {
			break
		}
		s = s[idx+len("beginbfrange"):]
		endIdx := strings.Index(s, "endbfrange")
		if endIdx < 0 {
			break
		}
		section := s[:endIdx]
		s = s[endIdx:]

		parseBfRange(section, m)
	}

	return m
}

func parseBfEntries(section string, m map[uint16]string) {
	tokens := extractHexTokens(section)
	for i := 0; i+1 < len(tokens); i += 2 {
		src := hexToUint16(tokens[i])
		dst := hexToUnicode(tokens[i+1])
		m[src] = dst
	}
}

func parseBfRange(section string, m map[uint16]string) {
	// Handles two forms:
	//   <srcLo> <srcHi> <dstStart>          — contiguous range
	//   <srcLo> <srcHi> [<d1> <d2> ...]     — array of individual mappings
	s := strings.TrimSpace(section)
	for len(s) > 0 {
		// Read srcLo.
		srcLoHex, rest := nextHexToken(s)
		if srcLoHex == "" {
			break
		}
		s = rest
		// Read srcHi.
		srcHiHex, rest := nextHexToken(s)
		if srcHiHex == "" {
			break
		}
		s = rest
		srcLo := hexToUint16(srcLoHex)
		srcHi := hexToUint16(srcHiHex)

		s = strings.TrimSpace(s)
		if len(s) > 0 && s[0] == '[' {
			// Array form: [<d1> <d2> ...]
			endBracket := strings.IndexByte(s, ']')
			if endBracket < 0 {
				break
			}
			arrContent := s[1:endBracket]
			s = strings.TrimSpace(s[endBracket+1:])
			dstTokens := extractHexTokens(arrContent)
			for i, code := 0, srcLo; code <= srcHi && i < len(dstTokens); i, code = i+1, code+1 {
				m[code] = hexToUnicode(dstTokens[i])
			}
		} else {
			// Contiguous form: <dstStart>
			dstHex, rest := nextHexToken(s)
			if dstHex == "" {
				break
			}
			s = rest
			dstStart := hexToUint16(dstHex)
			for code := srcLo; code <= srcHi; code++ {
				uni := dstStart + (code - srcLo)
				m[code] = string(rune(uni))
			}
		}
	}
}

// nextHexToken extracts the next <hex> token from s, returning (hex content, rest of string).
func nextHexToken(s string) (string, string) {
	s = strings.TrimSpace(s)
	start := strings.IndexByte(s, '<')
	if start < 0 {
		return "", ""
	}
	end := strings.IndexByte(s[start+1:], '>')
	if end < 0 {
		return "", ""
	}
	return s[start+1 : start+1+end], s[start+1+end+1:]
}

func extractHexTokens(s string) []string {
	var tokens []string
	for {
		start := strings.IndexByte(s, '<')
		if start < 0 {
			break
		}
		end := strings.IndexByte(s[start+1:], '>')
		if end < 0 {
			break
		}
		tokens = append(tokens, s[start+1:start+1+end])
		s = s[start+1+end+1:]
	}
	return tokens
}

func hexToUint16(s string) uint16 {
	v, _ := strconv.ParseUint(s, 16, 16)
	return uint16(v)
}

func hexToUnicode(hex string) string {
	if len(hex) <= 4 {
		return string(rune(hexToUint16(hex)))
	}
	// Multi-byte: pairs of uint16 as UTF-16 (may contain surrogate pairs).
	var runes []rune
	for i := 0; i+3 < len(hex); i += 4 {
		u := rune(hexToUint16(hex[i : i+4]))
		// Check for UTF-16 surrogate pair.
		if u >= 0xD800 && u <= 0xDBFF && i+7 < len(hex) {
			lo := rune(hexToUint16(hex[i+4 : i+8]))
			if lo >= 0xDC00 && lo <= 0xDFFF {
				u = 0x10000 + (u-0xD800)*0x400 + (lo - 0xDC00)
				i += 4 // skip the low surrogate
			}
		}
		runes = append(runes, u)
	}
	return string(runes)
}

// FontEncoding returns the byte→glyph-name mapping for a font.
// Handles /Encoding as a Name or as a Dict with /BaseEncoding + /Differences.
func (r *Reader) FontEncoding(font Dict) map[byte]string {
	encObj, ok := font["Encoding"]
	if !ok {
		return nil
	}

	// Case 1: /Encoding is a Name (e.g. /WinAnsiEncoding).
	resolved := r.Resolve(encObj)
	if encName, ok := resolved.(Name); ok {
		return predefinedEncoding(string(encName))
	}

	// Case 2: /Encoding is a Dict with optional /BaseEncoding and /Differences.
	encDict, ok := resolved.(Dict)
	if !ok {
		return nil
	}

	// Start from base encoding if specified.
	var diffs map[byte]string
	if baseName, ok := encDict.Name("BaseEncoding"); ok {
		diffs = predefinedEncoding(string(baseName))
	}
	if diffs == nil {
		diffs = make(map[byte]string)
	}

	// Apply /Differences overlay.
	diffArr, ok := encDict.Array("Differences")
	if !ok {
		return diffs
	}
	var code byte
	for _, item := range diffArr {
		switch v := item.(type) {
		case int:
			code = byte(v)
		case float64:
			code = byte(v)
		case Name:
			diffs[code] = string(v)
			code++
		}
	}
	return diffs
}

// predefinedEncoding returns the byte→glyph-name map for a named encoding.
func predefinedEncoding(name string) map[byte]string {
	switch name {
	case "WinAnsiEncoding":
		return copyMap(winansiGlyphNames)
	case "MacRomanEncoding":
		return copyMap(macRomanGlyphNames)
	default:
		return nil
	}
}

func copyMap(m map[byte]string) map[byte]string {
	c := make(map[byte]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// winansiGlyphNames maps WinAnsiEncoding byte values (0x80-0x9F) to glyph names.
var winansiGlyphNames = map[byte]string{
	0x80: "Euro", 0x82: "quotesinglbase", 0x83: "florin", 0x84: "quotedblbase",
	0x85: "ellipsis", 0x86: "dagger", 0x87: "daggerdbl", 0x88: "circumflex",
	0x89: "perthousand", 0x8A: "Scaron", 0x8B: "guilsinglleft", 0x8C: "OE",
	0x8E: "Zcaron", 0x91: "quoteleft", 0x92: "quoteright", 0x93: "quotedblleft",
	0x94: "quotedblright", 0x95: "bullet", 0x96: "endash", 0x97: "emdash",
	0x98: "tilde", 0x99: "trademark", 0x9A: "scaron", 0x9B: "guilsinglright",
	0x9C: "oe", 0x9E: "zcaron", 0x9F: "Ydieresis",
}

// macRomanGlyphNames maps MacRomanEncoding byte values (0x80-0xFF) to glyph names.
var macRomanGlyphNames = map[byte]string{
	0x80: "Adieresis", 0x81: "Aring", 0x82: "Ccedilla", 0x83: "Eacute",
	0x84: "Ntilde", 0x85: "Odieresis", 0x86: "Udieresis", 0x87: "aacute",
	0x88: "agrave", 0x89: "acircumflex", 0x8A: "adieresis", 0x8B: "atilde",
	0x8C: "aring", 0x8D: "ccedilla", 0x8E: "eacute", 0x8F: "egrave",
	0x90: "ecircumflex", 0x91: "edieresis", 0x92: "iacute", 0x93: "igrave",
	0x94: "icircumflex", 0x95: "idieresis", 0x96: "ntilde", 0x97: "oacute",
	0x98: "ograve", 0x99: "ocircumflex", 0x9A: "odieresis", 0x9B: "otilde",
	0x9C: "uacute", 0x9D: "ugrave", 0x9E: "ucircumflex", 0x9F: "udieresis",
	0xA0: "dagger", 0xA1: "degree", 0xA2: "cent", 0xA3: "sterling",
	0xA4: "section", 0xA5: "bullet", 0xA6: "paragraph", 0xA7: "germandbls",
	0xA8: "registered", 0xA9: "copyright", 0xAA: "trademark", 0xAB: "acute",
	0xAC: "dieresis", 0xAE: "AE", 0xAF: "Oslash",
	0xB1: "plusminus", 0xB5: "mu", 0xB6: "partialdiff",
	0xB7: "summation", 0xB8: "product", 0xB9: "pi", 0xBA: "integral",
	0xBB: "ordfeminine", 0xBC: "ordmasculine", 0xBE: "ae", 0xBF: "oslash",
	0xC0: "questiondown", 0xC1: "exclamdown", 0xC2: "logicalnot",
	0xC3: "radical", 0xC4: "florin", 0xC5: "approxequal", 0xC6: "Delta",
	0xC7: "guillemotleft", 0xC8: "guillemotright", 0xC9: "ellipsis",
	0xCA: "space", 0xCB: "Agrave", 0xCC: "Atilde", 0xCD: "Otilde",
	0xCE: "OE", 0xCF: "oe", 0xD0: "endash", 0xD1: "emdash",
	0xD2: "quotedblleft", 0xD3: "quotedblright", 0xD4: "quoteleft",
	0xD5: "quoteright", 0xD6: "divide", 0xD8: "ydieresis",
	0xD9: "Ydieresis", 0xDA: "fraction", 0xDB: "Euro",
	0xDC: "guilsinglleft", 0xDD: "guilsinglright", 0xDE: "fi", 0xDF: "fl",
	0xE0: "daggerdbl", 0xE1: "periodcentered", 0xE2: "quotesinglbase",
	0xE3: "quotedblbase", 0xE4: "perthousand", 0xE5: "Acircumflex",
	0xE6: "Ecircumflex", 0xE7: "Aacute", 0xE8: "Edieresis", 0xE9: "Egrave",
	0xEA: "Iacute", 0xEB: "Icircumflex", 0xEC: "Idieresis", 0xED: "Igrave",
	0xEE: "Oacute", 0xEF: "Ocircumflex", 0xF1: "Ograve",
	0xF2: "Uacute", 0xF3: "Ucircumflex", 0xF4: "Ugrave",
	0xF5: "dotlessi", 0xF6: "circumflex", 0xF7: "tilde",
	0xF8: "macron", 0xF9: "breve", 0xFA: "dotaccent", 0xFB: "ring",
	0xFC: "cedilla", 0xFD: "hungarumlaut", 0xFE: "ogonek", 0xFF: "caron",
}
