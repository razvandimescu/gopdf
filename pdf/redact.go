package pdf

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// glyph is one character code as it was drawn: the pen positions either side of
// it in device space, and the text-space distance the pen travelled over it.
type glyph struct {
	code   []byte  // 1 or 2 bytes, exactly as written in the string
	x0, y0 float64 // pen before the glyph
	x1, y1 float64 // pen after it
	adv    float64 // text-space advance, including character/word spacing and Tz
	drop   bool    // marked for removal
}

// showItem is one element of a text-showing operation: either the glyphs of a
// string, or a TJ kerning number.
type showItem struct {
	glyphs []glyph
	kern   float64
	isKern bool
}

// textRun is one shown string: what it says, and the glyphs that said it. The
// glyphs are the operation's own — the same backing array, not a copy — so
// marking one here is marking the code that will be rewritten.
type textRun struct {
	text           string
	glyphs         []glyph
	fontSize       float64
	startX, startY float64
	endX           float64
}

// showOp is one text-showing operation located in the stream that holds it.
// start:end spans its operands and the operator itself, so replacing that range
// rewrites the operation whole. tc and tw are recorded because the " operator
// carries them as operands and a replacement has to set them again.
type showOp struct {
	start, end int
	op         string
	tc, tw     float64
	scale      float64 // font size times horizontal scaling: TJ's unit
	items      []showItem
}

// showFrame is the stream being recorded and the transform from its own
// coordinates to the page's. A Form XObject draws in its own space, so the
// glyphs it shows have to be carried out to the page before a rectangle
// aimed at the page can be compared with them.
type showFrame struct {
	stream     int
	ctm        [6]float64
	recordable bool // false for a form no substitution can reach on write
}

// showRecorder collects the text-showing operations of a content stream as the
// extractor walks it. Redaction needs the state extraction already tracks —
// fonts, glyph widths, the text and current transformation matrices — so it
// rides along with that walk rather than re-implementing it; a second walk
// could disagree with the first, and then removal would miss the glyphs
// extraction can see.
//
// Operations are keyed by the object number of the stream holding them. Key 0
// is the page's own content: PageContent concatenates a page's content streams,
// so the result belongs to no single object, and Apply writes it back as one.
type showRecorder struct {
	streams map[int][]showOp
	data    map[int][]byte
	cur     showFrame
	pending []showItem
	runs    []textRun
}

func newShowRecorder(content []byte) *showRecorder {
	return &showRecorder{
		streams: make(map[int][]showOp),
		data:    map[int][]byte{0: content},
		cur:     showFrame{ctm: [6]float64{1, 0, 0, 1, 0, 0}, recordable: true},
	}
}

// The recorder's methods are nil-safe: extraction passes a nil recorder when
// nobody is redacting, which is every call but this file's.

func (r *showRecorder) show(text string, fontSize float64, glyphs []glyph) {
	if r == nil {
		return
	}
	for i := range glyphs {
		glyphs[i].x0, glyphs[i].y0 = applyMatrix6(r.cur.ctm, glyphs[i].x0, glyphs[i].y0)
		glyphs[i].x1, glyphs[i].y1 = applyMatrix6(r.cur.ctm, glyphs[i].x1, glyphs[i].y1)
	}
	// Only what the reader's view of the page contains takes part in
	// assembling it. A run of no glyphs has no position to be read in; a run
	// that decodes to nothing draws but contributes no text, and BuildLines
	// never sees it — no decoder here produces one today, but the spec allows
	// a code to stand for the empty string, and the two views must not drift
	// apart if one ever starts to.
	if text != "" && len(glyphs) > 0 {
		r.runs = append(r.runs, textRun{
			text: text, glyphs: glyphs, fontSize: fontSize,
			startX: glyphs[0].x0, startY: glyphs[0].y0,
			endX: glyphs[len(glyphs)-1].x1,
		})
	}
	r.pending = append(r.pending, showItem{glyphs: glyphs})
}

func (r *showRecorder) kern(v float64) {
	if r == nil {
		return
	}
	r.pending = append(r.pending, showItem{kern: v, isKern: true})
}

// finish closes the operator that has just been interpreted. Only the
// text-showing operators leave items pending, so everything else falls through.
func (r *showRecorder) finish(op string, start, end int, fontSize, tc, tw, th float64) {
	if r == nil || len(r.pending) == 0 {
		return
	}
	items := r.pending
	r.pending = nil
	if !r.cur.recordable {
		return
	}
	r.streams[r.cur.stream] = append(r.streams[r.cur.stream], showOp{
		start: start, end: end, op: op,
		tc: tc, tw: tw, scale: fontSize * th / 100,
		items: items,
	})
}

// enter switches recording to a Form XObject's own stream, composing the
// transform that carries its coordinates out to the page, and returns the
// frame to restore afterwards. A form reached by anything but an indirect
// reference cannot be replaced on write, so its operations are dropped rather
// than recorded and silently ignored.
func (r *showRecorder) enter(ref any, data []byte, ctm [6]float64) showFrame {
	if r == nil {
		return showFrame{}
	}
	outer := r.cur
	if n, ok := ref.(Ref); ok {
		r.cur = showFrame{stream: n.Num, ctm: matMul6(ctm, outer.ctm), recordable: true}
		r.data[n.Num] = data
	} else {
		r.cur = showFrame{}
	}
	return outer
}

func (r *showRecorder) leave(outer showFrame) {
	if r == nil {
		return
	}
	r.cur = outer
}

// matchQueries marks the glyphs that spelled any occurrence of any query in
// the text the page draws.
//
// Matching against the glyphs rather than against a rectangle is what makes
// removal exact. Page.Search reports where text is by dividing a span's width
// evenly among its characters, which is an estimate in any proportional font;
// a rectangle built that way can be off by a character or more, and a
// redaction that removes the wrong character while leaving the right one is
// worse than none.
func (r *showRecorder) matchQueries(queries []string) {
	if len(queries) == 0 {
		return
	}
	text, glyphAt := r.assembleText()
	for _, query := range queries {
		if query == "" {
			continue
		}
		for at := 0; at < len(text); {
			i := strings.Index(text[at:], query)
			if i < 0 {
				break
			}
			i += at
			for b := i; b < i+len(query); b++ {
				for g := range glyphAt[b] {
					glyphAt[b][g].drop = true
				}
			}
			at = i + len(query)
		}
	}
}

// assembleText joins what the page draws into one string, alongside the glyphs
// each byte of it came from (none, for the whitespace between runs). The
// reading order and the spacing are the reader's, from BuildLines: a query
// that Page.Search can find has to be findable here too, or removal would
// quietly miss text that redaction covers.
func (r *showRecorder) assembleText() (string, [][]glyph) {
	var text strings.Builder
	glyphAt := make([][]glyph, 0, 64)

	spell := func(s string, glyphs []glyph) {
		text.WriteString(s)
		for range len(s) {
			glyphAt = append(glyphAt, glyphs)
		}
	}

	order := r.readingOrder()
	for i, index := range order {
		run := r.runs[index]
		if i > 0 {
			spell(runSeparator(r.runs[order[i-1]], run), nil)
		}
		runes := utf8.RuneCountInString(run.text)
		for at, j := 0, 0; at < len(run.text); j++ {
			_, size := utf8.DecodeRuneInString(run.text[at:])
			spell(run.text[at:at+size], run.glyphsFor(j, runes))
			at += size
		}
	}
	return text.String(), glyphAt
}

// readingOrder indexes the runs in the order BuildLines would read them: down
// the page by baseline, then left to right within each line. Generators draw
// out of that order often enough — a column at a time, or a word in pieces to
// kern it — and Page.Search reports what BuildLines assembled, so removal has
// to assemble the same thing.
func (r *showRecorder) readingOrder() []int {
	order := make([]int, len(r.runs))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return r.runs[order[a]].startY > r.runs[order[b]].startY
	})

	// Grouped by the tolerance BuildLines groups by, and measured the same
	// way: against the first run of the line rather than the previous one, so
	// a drifting baseline does not walk a line apart one run at a time.
	line := 0
	for i := 1; i <= len(order); i++ {
		if i < len(order) && math.Abs(r.runs[order[i]].startY-r.runs[order[line]].startY) <= lineYTolerance {
			continue
		}
		within := order[line:i]
		sort.SliceStable(within, func(a, b int) bool {
			return r.runs[within[a]].startX < r.runs[within[b]].startX
		})
		line = i
	}
	return order
}

// glyphsFor maps the j-th of runeCount characters back to the glyphs that drew
// it. Codes and characters usually correspond one to one, which this gives
// exactly. Where they do not — one code standing for a ligature, or a font
// whose codes are read two bytes at a time while its text decodes one — the
// range covers every code the character could have come from, so a match takes
// all of them rather than half.
func (r textRun) glyphsFor(j, runeCount int) []glyph {
	n := len(r.glyphs)
	from, to := j*n/runeCount, (j+1)*n/runeCount
	if to <= from {
		to = from + 1
	}
	return r.glyphs[from:to]
}

// runSeparator is the whitespace BuildLines puts between two runs: a newline
// across baselines, and along one the same gap rule the reader's view uses.
func runSeparator(prev, cur textRun) string {
	if math.Abs(cur.startY-prev.startY) > lineYTolerance {
		return "\n"
	}
	end := spanEnd(prev.startX, prev.endX, prev.fontSize, prev.text)
	return spanGap(cur.startX-end, cur.fontSize)
}

// mark marks the glyphs a rectangle takes. Marking and rewriting are separate
// steps because a Form XObject can be drawn by several pages: each page marks
// it, and the one set of bytes behind it is rewritten once, after all of them
// have.
func (r *showRecorder) mark(drop func(glyph) bool) {
	for _, ops := range r.streams {
		for _, op := range ops {
			for _, item := range op.items {
				for i := range item.glyphs {
					if drop(item.glyphs[i]) {
						item.glyphs[i].drop = true
					}
				}
			}
		}
	}
}

func rewriteShowOps(content []byte, ops []showOp) ([]byte, bool) {
	sort.Slice(ops, func(i, j int) bool { return ops[i].start < ops[j].start })

	var out []byte
	copied, changed := 0, false
	for i := 0; i < len(ops); {
		// A stream drawn more than once is walked once per drawing, so the
		// same operation appears repeatedly, at the same offsets but at
		// different places on the page — or on a different page altogether.
		// One set of bytes can only have one fate: a glyph goes if any of
		// those drawings put it under a rectangle.
		j := i
		for j < len(ops) && ops[j].start == ops[i].start && ops[j].end == ops[i].end {
			j++
		}
		group := ops[i:j]
		i = j

		// Ranges are lexical and cannot overlap; the check keeps a stream that
		// somehow produced overlapping ones from slicing backwards.
		if group[0].start < copied {
			continue
		}
		replacement, ok := rewriteShowOp(group)
		if !ok {
			continue
		}
		if out == nil {
			out = make([]byte, 0, len(content))
		}
		out = append(out, content[copied:group[0].start]...)
		out = append(out, replacement...)
		copied = group[0].end
		changed = true
	}
	if !changed {
		return content, false
	}
	return append(out, content[copied:]...), true
}

// rewriteShowOp returns the operators that draw everything the original drew
// except the dropped glyphs, or ok=false when nothing was dropped. Whatever the
// original operator was, the replacement is a TJ: surviving glyphs keep their
// positions because each removed run leaves behind a kerning number worth
// exactly the advance it had.
func rewriteShowOp(group []showOp) (string, bool) {
	op := group[0]

	// Every drawing of these bytes read the same codes, so the others' marks
	// gather onto the first's glyphs.
	for _, other := range group[1:] {
		for i := range other.items {
			if i >= len(op.items) {
				break
			}
			for g := range other.items[i].glyphs {
				if other.items[i].glyphs[g].drop && g < len(op.items[i].glyphs) {
					op.items[i].glyphs[g].drop = true
				}
			}
		}
	}

	any := false
	for _, item := range op.items {
		for _, g := range item.glyphs {
			any = any || g.drop
		}
	}
	if !any {
		return "", false
	}

	var b strings.Builder
	b.WriteString("\n")
	switch op.op {
	case "'":
		b.WriteString("T* ")
	case "\"":
		// The operands this replaces were the word and character spacing.
		b.WriteString(formatOperand(op.tw))
		b.WriteString(" Tw ")
		b.WriteString(formatOperand(op.tc))
		b.WriteString(" Tc T* ")
	}

	b.WriteString("[")
	for _, item := range op.items {
		if item.isKern {
			b.WriteString(formatOperand(item.kern))
			b.WriteByte(' ')
			continue
		}
		writeSurvivingGlyphs(&b, item.glyphs, op.scale)
	}
	b.WriteString("] TJ")
	return b.String(), true
}

// writeSurvivingGlyphs emits one string's glyphs as alternating hex strings and
// kerning numbers. scale converts a text-space advance into TJ's units, which
// are thousandths of a unit of text space before the font size and horizontal
// scaling are applied — the same conversion the extractor's kern handling
// inverts. A zero scale leaves no way to express the gap, so the glyphs are
// dropped without compensation and the rest of the operation closes up.
func writeSurvivingGlyphs(b *strings.Builder, glyphs []glyph, scale float64) {
	var gap float64
	open := false

	closeRun := func() {
		if open {
			b.WriteString("> ")
			open = false
		}
	}
	closeGap := func() {
		if gap != 0 && scale != 0 {
			b.WriteString(formatOperand(-gap * 1000 / scale))
			b.WriteByte(' ')
		}
		gap = 0
	}

	for _, g := range glyphs {
		if g.drop {
			closeRun()
			gap += g.adv
			continue
		}
		closeGap()
		if !open {
			b.WriteByte('<')
			open = true
		}
		b.WriteString(hexEncode(g.code))
	}
	closeRun()
	closeGap()
}

// formatOperand renders a number for a content stream. Exponent notation is not
// valid PDF syntax, so the fixed format is not a preference.
func formatOperand(v float64) string {
	s := strconv.FormatFloat(v, 'f', 3, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	if s == "" || s == "-" || s == "-0" {
		return "0"
	}
	return s
}

// removeRegion is an area of a page whose text is to be deleted.
type removeRegion struct {
	page int
	rect Rect
}

// RemoveText deletes every occurrence of query from the pages' content streams.
// Unlike [Editor.RedactText], which draws a rectangle over the text and leaves
// it in the file, the glyphs are gone from the output: extraction, search and
// copy/paste cannot recover them.
//
// The two compose. Call both to delete the text and mark where it was:
//
//	ed.RemoveText(secret)
//	ed.RedactText(secret, 0, 0, 0)
//
// What is scrubbed, and what is not, is listed on [Editor.RemoveRegion].
func (e *Editor) RemoveText(query string) {
	e.removeQueries = append(e.removeQueries, query)
}

// RemoveRegion deletes the text inside rect on the given page (0-based). A
// glyph goes when the point where it sits on the baseline falls inside the
// rectangle, so a rectangle covering visible text takes that text; rect is in
// displayed space, like the rectangles [Document.Search] returns.
//
// Removal reaches the text drawn by the page's content streams and by the Form
// XObjects they invoke. It does not touch anything else the file may hold:
//
//	Page content stream glyphs      removed
//	Text inside Form XObjects       removed
//	Image XObjects                  not touched
//	Annotation text and appearance streams
//	                                not touched
//	AcroForm and XFA field values   not touched
//	Document information dictionary not touched
//	XMP metadata                    not touched
//	Embedded files                  not touched
//
// A Form XObject shared by several pages is a single object: text removed from
// it is removed everywhere it is drawn.
func (e *Editor) RemoveRegion(page int, rect Rect) {
	e.removeRegions = append(e.removeRegions, removeRegion{page: page, rect: rect})
}

// stripText deletes the requested text from every page that has any, and
// returns the rewritten content: page indices for the pages' own streams,
// object numbers for the Form XObjects they draw.
//
// Every page marks its own glyphs before anything is rewritten. A form drawn
// by two pages is one object with one set of bytes, and rewriting it as each
// page is visited would leave only the last page's removals in it.
func (e *Editor) stripText(reader *Reader, pages []Dict) (map[int][]byte, map[int][]byte, error) {
	if len(e.removeQueries) == 0 && len(e.removeRegions) == 0 {
		return nil, nil, nil
	}

	regions := make(map[int][]Rect)
	for _, rm := range e.removeRegions {
		regions[rm.page] = append(regions[rm.page], rm.rect)
	}

	strippedPages := make(map[int][]byte)
	formOps := make(map[int][]showOp)
	formData := make(map[int][]byte)

	for i, page := range pages {
		if len(e.removeQueries) == 0 && len(regions[i]) == 0 {
			continue
		}
		rec, err := markPage(reader, page, e.removeQueries, regions[i])
		if err != nil {
			return nil, nil, fmt.Errorf("removing text from page %d: %w", i, err)
		}
		if rec == nil {
			continue
		}
		for stream, ops := range rec.streams {
			if stream == 0 {
				if content, changed := rewriteShowOps(rec.data[0], ops); changed {
					strippedPages[i] = content
				}
				continue
			}
			formOps[stream] = append(formOps[stream], ops...)
			formData[stream] = rec.data[stream]
		}
	}

	strippedForms := make(map[int][]byte)
	for stream, ops := range formOps {
		if content, changed := rewriteShowOps(formData[stream], ops); changed {
			strippedForms[stream] = content
		}
	}
	return strippedPages, strippedForms, nil
}

// markPage records everything page draws and marks the glyphs that neither the
// queries nor rects leave standing.
func markPage(r *Reader, page Dict, queries []string, rects []Rect) (*showRecorder, error) {
	content, err := r.PageContent(page)
	if err != nil || len(content) == 0 {
		return nil, err
	}

	rec := newShowRecorder(content)
	extractTextWithResources(content, r.PageFonts(page), r, r.PageResources(page), 0, rec)

	rec.matchQueries(queries)
	if len(rects) == 0 {
		return rec, nil
	}

	// Glyph positions are in unrotated user space, where content is drawn;
	// the rectangles are in displayed space, where the reader saw the text.
	rotM, rotated := pageRotationMatrix(page)
	rec.mark(func(g glyph) bool {
		x, y := (g.x0+g.x1)/2, (g.y0+g.y1)/2
		if rotated {
			x, y = applyMatrix6(rotM, x, y)
		}
		for _, rect := range rects {
			if x >= rect.X && x <= rect.X+rect.Width && y >= rect.Y && y <= rect.Y+rect.Height {
				return true
			}
		}
		return false
	})
	return rec, nil
}
