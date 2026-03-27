package pdf

import (
	"math"
	"sort"
	"strings"
)

const (
	defaultYTolerance    = 2.0
	defaultWrapTolerance = 15.0
	defaultMinColumns    = 3
	defaultMinGap        = 10.0
	maxWrapXDistance     = 30.0 // max horizontal distance for header continuation merging
	gapClusterTolerance  = 5.0  // X-distance for merging gap midpoints into clusters
)

// Table is a detected table with named columns and data rows.
type Table struct {
	Columns []Column
	Rows    []Row
}

// Column is a named table column with its detected X position on the page.
type Column struct {
	Name string
	X    float64
}

// Row is a single data row within a detected table.
type Row struct {
	Y     float64
	Cells []Cell
}

// Cell holds the text content assigned to one column in one row.
type Cell struct {
	Column int // index into Table.Columns
	Text   string
	Spans  []TextSpan // original spans for position/font access
}

// TableOpts configures table detection.
type TableOpts struct {
	// Headers identifies the header row: all strings must appear as
	// case-insensitive substrings of span text on the same Y-line.
	// When nil, auto-detection via gap analysis is used.
	Headers []string

	// YTolerance is the max vertical distance (pt) for spans to be
	// on the same row. Default: 2.0.
	YTolerance float64

	// MinColumns is the minimum column count for auto-detection.
	// Default: 3. Ignored when Headers is set.
	MinColumns int

	// RowFilter is called for each candidate data row. Return false
	// to skip the row (processing continues with next row).
	// The slice is positionally matched to columns.
	RowFilter func(cells []string) bool

	// WrapTolerance is the max distance (pt) below the header to
	// search for wrapped header continuations (e.g. "Product" / "Code"
	// on two lines). Default: 15. Set negative to disable.
	WrapTolerance float64

	// MinGap is the minimum horizontal gap (pt) between spans for
	// auto-detection to treat as a column separator. Default: 10.
	MinGap float64
}

func (o *TableOpts) yTol() float64 {
	if o != nil && o.YTolerance > 0 {
		return o.YTolerance
	}
	return defaultYTolerance
}

func (o *TableOpts) wrapTol() float64 {
	if o != nil && o.WrapTolerance > 0 {
		return o.WrapTolerance
	}
	if o != nil && o.WrapTolerance < 0 {
		return 0
	}
	return defaultWrapTolerance
}

func (o *TableOpts) minCols() int {
	if o != nil && o.MinColumns > 0 {
		return o.MinColumns
	}
	return defaultMinColumns
}

func (o *TableOpts) minGap() float64 {
	if o != nil && o.MinGap > 0 {
		return o.MinGap
	}
	return defaultMinGap
}

// CellText returns the text at (row, col). Empty string if out of bounds.
func (t *Table) CellText(row, col int) string {
	if row < 0 || row >= len(t.Rows) {
		return ""
	}
	if col < 0 || col >= len(t.Rows[row].Cells) {
		return ""
	}
	return t.Rows[row].Cells[col].Text
}

// ColumnByName returns the column index for name (case-insensitive), or -1.
func (t *Table) ColumnByName(name string) int {
	lower := strings.ToLower(name)
	for i, c := range t.Columns {
		if strings.ToLower(c.Name) == lower {
			return i
		}
	}
	return -1
}

// CellByName returns the text at (row, column-by-name). Empty if not found.
func (t *Table) CellByName(row int, colName string) string {
	ci := t.ColumnByName(colName)
	if ci < 0 {
		return ""
	}
	return t.CellText(row, ci)
}

// FindTable detects a single table from spans.
// Uses explicit headers if opts.Headers is set, otherwise auto-detects.
func FindTable(spans []TextSpan, opts *TableOpts) *Table {
	if opts != nil && len(opts.Headers) > 0 {
		return findTableByHeaders(spans, opts)
	}
	tables := findTablesByGaps(spans, opts)
	if len(tables) > 0 {
		return &tables[0]
	}
	return nil
}

// FindTables detects all tables in spans via gap-based auto-detection.
// When opts.Headers is set, returns at most one table.
func FindTables(spans []TextSpan, opts *TableOpts) []Table {
	if opts != nil && len(opts.Headers) > 0 {
		t := findTableByHeaders(spans, opts)
		if t != nil {
			return []Table{*t}
		}
		return nil
	}
	return findTablesByGaps(spans, opts)
}

// FindTableAcrossPages detects a table spanning multiple pages.
// Each page's spans are searched for a header; data rows accumulate
// into a single table.
func FindTableAcrossPages(pages [][]TextSpan, opts *TableOpts) *Table {
	var result *Table
	for _, spans := range pages {
		t := FindTable(spans, opts)
		if t == nil {
			continue
		}
		if result == nil {
			result = t
		} else {
			result.Rows = append(result.Rows, t.Rows...)
		}
	}
	return result
}

// =====================================================================
// Approach 1: Explicit header anchors
// =====================================================================

func findTableByHeaders(spans []TextSpan, opts *TableOpts) *Table {
	yTol := opts.yTol()
	rows := groupRows(spans, yTol)

	hi, ok := findHeaderRow(rows, opts.Headers)
	if !ok {
		return nil
	}

	hSpans := collectHeaders(rows[hi])
	if len(hSpans) == 0 {
		return nil
	}

	lowestY := rows[hi].y
	wt := opts.wrapTol()
	if wt > 0 {
		lowestY = mergeWrapped(rows, hi, hSpans, yTol, wt)
	}

	columns := make([]Column, len(hSpans))
	for i, h := range hSpans {
		columns[i] = Column{Name: h.text, X: h.x}
	}

	// Data rows start after header + any continuation rows.
	dataStart := hi + 1
	for dataStart < len(rows) && rows[dataStart].y >= lowestY-yTol {
		dataStart++
	}

	return &Table{Columns: columns, Rows: collectDataRows(rows[dataStart:], columns, opts)}
}

func findHeaderRow(rows []tableRow, anchors []string) (int, bool) {
	for i, row := range rows {
		if matchAnchors(row.spans, anchors) {
			return i, true
		}
	}
	return 0, false
}

func matchAnchors(spans []TextSpan, anchors []string) bool {
	for _, anchor := range anchors {
		al := strings.ToLower(anchor)
		found := false
		for _, sp := range spans {
			if strings.Contains(strings.ToLower(strings.TrimSpace(sp.Text)), al) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

type colHeader struct {
	text string
	x    float64
}

func collectHeaders(row tableRow) []colHeader {
	var hs []colHeader
	for _, sp := range row.spans {
		text := strings.TrimSpace(sp.Text)
		if text != "" {
			hs = append(hs, colHeader{text, sp.X})
		}
	}
	// Spans pre-sorted by X in groupRows.
	return hs
}

// mergeWrapped merges single-word continuation spans from row(s)
// below the header into single-word header spans. Returns the lowest
// Y of the continuation region (for computing where data rows start).
func mergeWrapped(rows []tableRow, hi int, hSpans []colHeader, yTol, wrapTol float64) float64 {
	headerY := rows[hi].y
	lowestY := headerY

	nIncomplete := 0
	for _, h := range hSpans {
		if !strings.Contains(h.text, " ") {
			nIncomplete++
		}
	}
	if nIncomplete == 0 {
		return headerY
	}

	for ri := hi + 1; ri < len(rows); ri++ {
		dy := headerY - rows[ri].y
		if dy < yTol || dy > wrapTol {
			break
		}

		var contSpans []colHeader
		for _, sp := range rows[ri].spans {
			text := strings.TrimSpace(sp.Text)
			if text != "" && !strings.Contains(text, " ") {
				contSpans = append(contSpans, colHeader{text, sp.X})
			}
		}
		if len(contSpans) == 0 || len(contSpans) > nIncomplete {
			break
		}

		if rows[ri].y < lowestY {
			lowestY = rows[ri].y
		}

		for _, cs := range contSpans {
			bestIdx := -1
			bestDist := math.MaxFloat64
			for i, h := range hSpans {
				if strings.Contains(h.text, " ") {
					continue
				}
				dist := math.Abs(cs.x - h.x)
				if dist < bestDist && dist < maxWrapXDistance {
					bestDist = dist
					bestIdx = i
				}
			}
			if bestIdx >= 0 {
				hSpans[bestIdx].text += " " + cs.text
			}
		}
	}
	return lowestY
}

// =====================================================================
// Approach 2: Auto-detection via recurring vertical gaps
// =====================================================================

func findTablesByGaps(spans []TextSpan, opts *TableOpts) []Table {
	yTol := opts.yTol()
	minCols := opts.minCols()
	minGap := opts.minGap()

	rows := groupRows(spans, yTol)
	if len(rows) < 2 {
		return nil
	}

	// For each row, compute gap midpoints between consecutive spans.
	rowGaps := make([][]float64, len(rows))
	for i, row := range rows {
		for j := 0; j < len(row.spans)-1; j++ {
			endX := spanEndX(row.spans[j])
			startX := row.spans[j+1].X
			if gap := startX - endX; gap > minGap {
				rowGaps[i] = append(rowGaps[i], (endX+startX)/2)
			}
		}
	}

	// Cluster gap midpoints across rows.
	type gapCluster struct {
		x    float64
		rows map[int]bool
	}
	var clusters []gapCluster
	for ri, gaps := range rowGaps {
		for _, gx := range gaps {
			found := false
			for ci := range clusters {
				if math.Abs(gx-clusters[ci].x) < gapClusterTolerance {
					clusters[ci].rows[ri] = true
					found = true
					break
				}
			}
			if !found {
				clusters = append(clusters, gapCluster{x: gx, rows: map[int]bool{ri: true}})
			}
		}
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].x < clusters[j].x })

	// Keep clusters appearing in enough rows and score each row.
	threshold := max(3, len(rows)/4)
	var sigGaps []float64
	rowScore := make([]int, len(rows))
	for _, c := range clusters {
		if len(c.rows) < threshold {
			continue
		}
		sigGaps = append(sigGaps, c.x)
		for ri := range c.rows {
			rowScore[ri]++
		}
	}
	if len(sigGaps) < minCols-1 {
		return nil
	}

	// Find contiguous row regions with score >= minCols-1.
	var tables []Table
	i := 0
	for i < len(rows) {
		if rowScore[i] < minCols-1 {
			i++
			continue
		}
		start := i
		for i < len(rows) && rowScore[i] >= minCols-1 {
			i++
		}
		if i-start < 2 {
			continue
		}
		t := buildTableFromRegion(rows[start:i], sigGaps, opts)
		if t != nil {
			tables = append(tables, *t)
		}
	}

	return tables
}

func buildTableFromRegion(rows []tableRow, gaps []float64, opts *TableOpts) *Table {
	// gaps are already sorted (populated from X-sorted clusters).
	var columns []Column
	var curName []string
	var curX float64
	curSeg := -1

	for _, sp := range rows[0].spans {
		text := strings.TrimSpace(sp.Text)
		if text == "" {
			continue
		}
		seg := sort.SearchFloat64s(gaps, sp.X)
		if curSeg >= 0 && seg != curSeg {
			columns = append(columns, Column{
				Name: strings.Join(curName, " "),
				X:    curX,
			})
			curName = nil
		}
		if len(curName) == 0 {
			curX = sp.X
		}
		curName = append(curName, text)
		curSeg = seg
	}
	if len(curName) > 0 {
		columns = append(columns, Column{
			Name: strings.Join(curName, " "),
			X:    curX,
		})
	}

	if len(columns) < 2 {
		return nil
	}

	dataRows := collectDataRows(rows[1:], columns, opts)
	if len(dataRows) == 0 {
		return nil
	}
	return &Table{Columns: columns, Rows: dataRows}
}

// =====================================================================
// Shared internals
// =====================================================================

type tableRow struct {
	y     float64
	spans []TextSpan
}

// groupRows groups spans by Y coordinate into rows sorted top-to-bottom,
// with spans within each row sorted left-to-right by X.
func groupRows(spans []TextSpan, yTol float64) []tableRow {
	var rows []tableRow
	for _, sp := range spans {
		if strings.TrimSpace(sp.Text) == "" {
			continue
		}
		found := false
		for i := range rows {
			if math.Abs(sp.Y-rows[i].y) < yTol {
				rows[i].spans = append(rows[i].spans, sp)
				found = true
				break
			}
		}
		if !found {
			rows = append(rows, tableRow{y: sp.Y, spans: []TextSpan{sp}})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].y > rows[j].y })
	for i := range rows {
		sort.Slice(rows[i].spans, func(a, b int) bool {
			return rows[i].spans[a].X < rows[i].spans[b].X
		})
	}
	return rows
}

func classifySpan(x float64, columns []Column) int {
	best := 0
	bestDist := math.MaxFloat64
	for i, col := range columns {
		dist := math.Abs(x - col.X)
		if dist < bestDist {
			bestDist = dist
			best = i
		}
	}
	return best
}

func buildCells(spans []TextSpan, columns []Column) []Cell {
	cells := make([]Cell, len(columns))
	for i := range cells {
		cells[i].Column = i
	}
	for _, sp := range spans { // spans already sorted by X via groupRows
		text := strings.TrimSpace(sp.Text)
		if text == "" {
			continue
		}
		ci := classifySpan(sp.X, columns)
		if cells[ci].Text != "" {
			cells[ci].Text += " " + text
		} else {
			cells[ci].Text = text
		}
		cells[ci].Spans = append(cells[ci].Spans, sp)
	}
	return cells
}

// collectDataRows builds Row values from tableRows, applying RowFilter
// and skipping empty rows. Shared by both detection approaches.
func collectDataRows(rows []tableRow, columns []Column, opts *TableOpts) []Row {
	var result []Row
	for _, row := range rows {
		cells := buildCells(row.spans, columns)
		if opts != nil && opts.RowFilter != nil && !opts.RowFilter(cellTexts(cells)) {
			continue
		}
		if allEmpty(cells) {
			continue
		}
		result = append(result, Row{Y: row.y, Cells: cells})
	}
	return result
}

func spanEndX(sp TextSpan) float64 {
	if sp.EndX > sp.X {
		return sp.EndX
	}
	return sp.X + float64(len([]rune(sp.Text)))*sp.FontSize*0.5
}

func cellTexts(cells []Cell) []string {
	ct := make([]string, len(cells))
	for i, c := range cells {
		ct[i] = c.Text
	}
	return ct
}

func allEmpty(cells []Cell) bool {
	for _, c := range cells {
		if c.Text != "" {
			return false
		}
	}
	return true
}
