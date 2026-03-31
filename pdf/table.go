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
	maxWrapXDistance      = 30.0 // max horizontal distance for header continuation merging
	gapClusterTolerance  = 5.0  // X-distance for merging gap midpoints into clusters
	anchorClusterTol     = 5.0  // X-distance for clustering span start positions
	anchorMinRowFrac     = 0.15 // fraction of rows an anchor must appear in
	anchorMaxHeaderLen   = 30   // max text length for header-like spans
	anchorMinColSpacing  = 20.0 // min X-distance between anchor columns (filters char-level spans)
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

	// MaxRowGap is the maximum Y-distance (pt) between consecutive
	// data rows. Rows beyond this gap are excluded; the table is
	// considered to have ended. Applied after MergeGap.
	// Default: 0 (no limit).
	MaxRowGap float64

	// MergeGap is the maximum Y-distance (pt) between consecutive
	// rows to merge them into a single logical row. Text in the same
	// column is concatenated. Useful for tables with multi-line cells
	// (e.g., bank statements). Applied before MaxRowGap.
	// Default: 0 (no merging).
	MergeGap float64

	// AutoTune tries multiple MergeGap/MaxRowGap combinations and
	// picks the one producing the best table (most rows, best fill
	// rate). Only used by FindTableAcrossPages when MergeGap and
	// MaxRowGap are both zero. Default: false.
	AutoTune bool

	// AnchorColumn names a column that signals the start of a new
	// logical row. Consecutive rows where this column is empty are
	// merged into the previous row that had a non-empty anchor.
	// Applied after MergeGap. Case-insensitive.
	AnchorColumn string

	// columnOverrides, when set, provides column X positions for data
	// classification instead of deriving them from header span positions.
	// Headers are still used to locate the header row. Internal only.
	columnOverrides []Column
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

func (o *TableOpts) maxRowGap() float64 {
	if o != nil {
		return o.MaxRowGap
	}
	return 0
}

func (o *TableOpts) mergeGap() float64 {
	if o != nil {
		return o.MergeGap
	}
	return 0
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
	return findTableByAnchors(spans, opts)
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
	tables := findTablesByGaps(spans, opts)
	if len(tables) > 0 {
		return tables
	}
	t := findTableByAnchors(spans, opts)
	if t != nil {
		return []Table{*t}
	}
	return nil
}

// FindTableAcrossPages detects a table spanning multiple pages.
// Each page's spans are searched for a header; data rows accumulate
// into a single table. When auto-detecting (no headers), anchor-based
// detection pools spans across all pages for robust column discovery.
func FindTableAcrossPages(pages [][]TextSpan, opts *TableOpts) *Table {
	// With explicit headers, do per-page detection and merge.
	if opts != nil && len(opts.Headers) > 0 {
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

	// Auto-detect: discover columns via anchor analysis, then extract.
	cols := discoverAnchorsAcrossPages(pages, opts)
	if cols == nil {
		return nil
	}
	hdrNames := make([]string, len(cols))
	for i, c := range cols {
		hdrNames[i] = c.Name
	}

	// When AutoTune is set and no MergeGap/MaxRowGap provided, try
	// multiple combinations and pick the best result.
	if opts != nil && opts.AutoTune && opts.MergeGap == 0 && opts.MaxRowGap == 0 {
		return autoTuneExtract(pages, hdrNames, cols, opts)
	}

	withHeaders := copyOpts(opts)
	withHeaders.Headers = hdrNames
	withHeaders.columnOverrides = cols
	return FindTableAcrossPages(pages, withHeaders)
}

func copyOpts(opts *TableOpts) *TableOpts {
	out := &TableOpts{}
	if opts != nil {
		*out = *opts
	}
	out.Headers = nil
	out.AutoTune = false
	return out
}

var (
	tuneMergeGaps  = []float64{0, 12, 16, 20}
	tuneMaxRowGaps = []float64{0, 25, 35, 50}
)

func autoTuneExtract(pages [][]TextSpan, headers []string, colOverrides []Column, opts *TableOpts) *Table {
	var best *Table
	bestScore := -1.0

	for _, mg := range tuneMergeGaps {
		for _, mrg := range tuneMaxRowGaps {
			candidate := copyOpts(opts)
			candidate.Headers = headers
			candidate.columnOverrides = colOverrides
			candidate.MergeGap = mg
			candidate.MaxRowGap = mrg
			t := FindTableAcrossPages(pages, candidate)
			if t == nil {
				continue
			}
			s := scoreTable(t)
			if s > bestScore {
				bestScore = s
				best = t
			}
		}
	}
	return best
}

// scoreTable ranks a table extraction result. Higher = better.
// Rewards: row count, column fill rate, numeric column consistency.
func scoreTable(t *Table) float64 {
	if len(t.Rows) == 0 || len(t.Columns) == 0 {
		return 0
	}
	ncols := len(t.Columns)
	nrows := len(t.Rows)

	// Fill rate: fraction of cells that are non-empty.
	filled := 0
	for _, row := range t.Rows {
		for _, cell := range row.Cells {
			if cell.Text != "" {
				filled++
			}
		}
	}
	fillRate := float64(filled) / float64(nrows*ncols)

	// Row count (log-scaled so 200 rows isn't 200x better than 1).
	rowScore := math.Log1p(float64(nrows))

	// fillRate^2 penalizes noisy extractions heavily.
	return rowScore * fillRate * fillRate
}

// discoverAnchorsAcrossPages scans pages for the most header-like row,
// then computes data X clusters across all pages and maps header names
// onto data clusters by positional zone. This handles PDFs where header
// X positions don't align with data X positions (e.g. BCR statements).
func discoverAnchorsAcrossPages(pages [][]TextSpan, opts *TableOpts) []Column {
	yTol := opts.yTol()
	minCols := opts.minCols()

	// Step 1: find the best header row (by text characteristics).
	var bestHeaderCols []Column
	bestScore := 0
	bestLen := math.MaxInt

	var allRows []tableRow
	for _, spans := range pages {
		rows := groupRows(spans, yTol)
		allRows = append(allRows, rows...)
		for _, row := range rows {
			cols := scoreHeaderRow(row, anchorMinColSpacing)
			if len(cols) < minCols {
				continue
			}
			totalLen := 0
			for _, c := range cols {
				totalLen += len(c.Name)
			}
			if len(cols) > bestScore || (len(cols) == bestScore && totalLen < bestLen) {
				bestScore = len(cols)
				bestLen = totalLen
				bestHeaderCols = cols
			}
		}
	}
	if bestHeaderCols == nil {
		return nil
	}

	// Step 2: compute data X clusters across all pages.
	anchors := clusterXPositions(allRows, anchorClusterTol)
	threshold := max(3, min(20, int(float64(len(allRows))*anchorMinRowFrac)))

	var dataAnchors []xCluster
	for _, a := range anchors {
		if len(a.rows) >= threshold {
			if len(dataAnchors) > 0 && a.x-dataAnchors[len(dataAnchors)-1].x < anchorMinColSpacing {
				continue
			}
			dataAnchors = append(dataAnchors, a)
		}
	}

	// Keep only the top N data anchors by row count (N = header count).
	// This filters out sub-header noise that creates false anchors.
	if len(dataAnchors) > len(bestHeaderCols) {
		sort.Slice(dataAnchors, func(i, j int) bool {
			return len(dataAnchors[i].rows) > len(dataAnchors[j].rows)
		})
		dataAnchors = dataAnchors[:len(bestHeaderCols)]
		sort.Slice(dataAnchors, func(i, j int) bool {
			return dataAnchors[i].x < dataAnchors[j].x
		})
	}

	// Step 3: map header names to data anchors by positional zone.
	if len(dataAnchors) >= minCols {
		return mapHeadersToAnchors(bestHeaderCols, dataAnchors)
	}

	// Fallback: use header X positions directly.
	return bestHeaderCols
}

// mapHeadersToAnchors assigns header names to data X clusters using
// zone-based mapping: each header falls into the zone of the nearest
// data anchor, where zones are defined by midpoints between anchors.
func mapHeadersToAnchors(headers []Column, anchors []xCluster) []Column {
	// Build zone boundaries (midpoints between adjacent anchors).
	n := len(anchors)
	boundaries := make([]float64, n+1)
	boundaries[0] = -math.MaxFloat64
	boundaries[n] = math.MaxFloat64
	for i := 1; i < n; i++ {
		boundaries[i] = (anchors[i-1].x + anchors[i].x) / 2
	}

	// Assign each header to the zone it falls in.
	cols := make([]Column, n)
	for i := range cols {
		cols[i] = Column{X: anchors[i].x}
	}
	for _, h := range headers {
		for i := 0; i < n; i++ {
			if h.X >= boundaries[i] && h.X < boundaries[i+1] {
				if cols[i].Name == "" {
					cols[i].Name = h.Name
				}
				break
			}
		}
	}

	// Drop unnamed columns (data clusters with no matching header).
	var result []Column
	for _, c := range cols {
		if c.Name != "" {
			result = append(result, c)
		}
	}
	return result
}

// scoreHeaderRow returns columns if the row looks like a table header:
// multiple short non-numeric spans spaced at least minSpacing apart.
func scoreHeaderRow(row tableRow, minSpacing float64) []Column {
	var cols []Column
	for _, sp := range row.spans {
		text := strings.TrimSpace(sp.Text)
		if !isHeaderText(text) {
			continue
		}
		if len(cols) > 0 && sp.X-cols[len(cols)-1].X < minSpacing {
			continue
		}
		cols = append(cols, Column{Name: text, X: sp.X})
	}
	return cols
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

	// Use column overrides (data cluster X positions) when available.
	var columns []Column
	if opts != nil && len(opts.columnOverrides) > 0 {
		columns = opts.columnOverrides
	} else {
		columns = make([]Column, len(hSpans))
		for i, h := range hSpans {
			columns[i] = Column{Name: h.text, X: h.x}
		}
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
		if text == "" {
			continue
		}
		// Deduplicate spans at the same X (some PDFs render headers twice).
		dup := false
		for _, h := range hs {
			if math.Abs(sp.X-h.x) < 1.0 && h.text == text {
				dup = true
				break
			}
		}
		if !dup {
			hs = append(hs, colHeader{text, sp.X})
		}
	}
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
// Approach 3: Auto-detection via anchor X positions
// =====================================================================

func findTableByAnchors(spans []TextSpan, opts *TableOpts) *Table {
	yTol := opts.yTol()
	minCols := opts.minCols()
	rows := groupRows(spans, yTol)
	if len(rows) < 2 {
		return nil
	}

	anchors := clusterXPositions(rows, anchorClusterTol)
	threshold := max(3, int(float64(len(rows))*anchorMinRowFrac))

	var sigAnchors []xCluster
	for _, a := range anchors {
		if len(a.rows) >= threshold {
			// Skip anchors too close to the previous (character-level spans).
			if len(sigAnchors) > 0 && a.x-sigAnchors[len(sigAnchors)-1].x < anchorMinColSpacing {
				continue
			}
			sigAnchors = append(sigAnchors, a)
		}
	}
	if len(sigAnchors) > 10 || len(sigAnchors) < minCols {
		return nil
	}

	hi, ok := findAnchorHeaderRow(rows, sigAnchors, minCols)
	if !ok {
		return nil
	}

	// Build columns from the header row's spans aligned to anchors.
	columns := buildAnchorColumns(rows[hi], sigAnchors)
	if len(columns) < minCols {
		return nil
	}

	// Handle wrapped headers.
	hSpans := make([]colHeader, len(columns))
	for i, c := range columns {
		hSpans[i] = colHeader{text: c.Name, x: c.X}
	}
	lowestY := rows[hi].y
	wt := opts.wrapTol()
	if wt > 0 {
		lowestY = mergeWrapped(rows, hi, hSpans, yTol, wt)
		for i := range columns {
			columns[i].Name = hSpans[i].text
		}
	}

	dataStart := hi + 1
	for dataStart < len(rows) && rows[dataStart].y >= lowestY-yTol {
		dataStart++
	}

	dataRows := collectDataRows(rows[dataStart:], columns, opts)
	if len(dataRows) == 0 {
		return nil
	}
	return &Table{Columns: columns, Rows: dataRows}
}

type xCluster struct {
	x    float64
	rows map[int]bool
}

func clusterXPositions(rows []tableRow, tolerance float64) []xCluster {
	var clusters []xCluster
	for ri, row := range rows {
		for _, sp := range row.spans {
			if strings.TrimSpace(sp.Text) == "" {
				continue
			}
			found := false
			for ci := range clusters {
				if math.Abs(sp.X-clusters[ci].x) < tolerance {
					clusters[ci].rows[ri] = true
					found = true
					break
				}
			}
			if !found {
				clusters = append(clusters, xCluster{x: sp.X, rows: map[int]bool{ri: true}})
			}
		}
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].x < clusters[j].x })
	return clusters
}

func findAnchorHeaderRow(rows []tableRow, anchors []xCluster, minCols int) (int, bool) {
	nAnchors := len(anchors)
	// Require the header to match most anchor positions.
	minScore := max(minCols, nAnchors*2/3)

	bestIdx, bestScore := -1, 0
	for ri, row := range rows {
		score := 0
		for _, a := range anchors {
			for _, sp := range row.spans {
				text := strings.TrimSpace(sp.Text)
				if math.Abs(sp.X-a.x) < anchorClusterTol && isHeaderText(text) {
					score++
					break
				}
			}
		}
		if score > bestScore && score >= minScore {
			bestScore = score
			bestIdx = ri
		}
	}
	if bestIdx < 0 {
		return 0, false
	}
	return bestIdx, true
}

func buildAnchorColumns(row tableRow, anchors []xCluster) []Column {
	var cols []Column
	for _, a := range anchors {
		var best *TextSpan
		bestDist := math.MaxFloat64
		for i := range row.spans {
			d := math.Abs(row.spans[i].X - a.x)
			if d < anchorClusterTol && d < bestDist {
				bestDist = d
				best = &row.spans[i]
			}
		}
		if best != nil {
			text := strings.TrimSpace(best.Text)
			if text != "" {
				cols = append(cols, Column{Name: text, X: a.x})
			}
		}
	}
	return cols
}

func isHeaderText(s string) bool {
	if len(s) == 0 || len(s) > anchorMaxHeaderLen {
		return false
	}
	// Strip common numeric characters; if nothing remains, it's a number.
	stripped := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r == '.' || r == ',' || r == '-' || r == ' ' {
			return -1
		}
		return r
	}, s)
	return len(stripped) > 0
}

// =====================================================================
// Shared internals
// =====================================================================

type tableRow struct {
	y       float64
	yBottom float64
	spans   []TextSpan
}

func sortSpansByX(spans []TextSpan) {
	sort.Slice(spans, func(a, b int) bool { return spans[a].X < spans[b].X })
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
			rows = append(rows, tableRow{y: sp.Y, yBottom: sp.Y, spans: []TextSpan{sp}})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].y > rows[j].y })
	for i := range rows {
		sortSpansByX(rows[i].spans)
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

// collectDataRows builds Row values from tableRows, applying MergeGap,
// MaxRowGap, RowFilter, and skipping empty rows. Shared by both approaches.
func collectDataRows(rows []tableRow, columns []Column, opts *TableOpts) []Row {
	if mg := opts.mergeGap(); mg > 0 {
		rows = mergeCloseRows(rows, mg)
	}

	maxGap := opts.maxRowGap()
	var result []Row
	for i, row := range rows {
		if maxGap > 0 && i > 0 && rows[i-1].yBottom-row.y > maxGap {
			break
		}
		cells := buildCells(row.spans, columns)
		if opts != nil && opts.RowFilter != nil && !opts.RowFilter(cellTexts(cells)) {
			continue
		}
		if allEmpty(cells) {
			continue
		}
		result = append(result, Row{Y: row.y, Cells: cells})
	}

	// Anchor-column merge: collapse rows where the anchor column is
	// empty into the previous row that had a non-empty anchor.
	if opts != nil && opts.AnchorColumn != "" {
		result = mergeByAnchorColumn(result, columns, opts.AnchorColumn)
	}
	return result
}

func mergeByAnchorColumn(rows []Row, columns []Column, anchor string) []Row {
	ai := -1
	lower := strings.ToLower(anchor)
	for i, c := range columns {
		if strings.ToLower(c.Name) == lower {
			ai = i
			break
		}
	}
	if ai < 0 {
		return rows
	}

	var merged []Row
	for _, row := range rows {
		anchorText := ""
		if ai < len(row.Cells) {
			anchorText = row.Cells[ai].Text
		}
		if anchorText != "" || len(merged) == 0 {
			merged = append(merged, row)
		} else if isContinuationRow(row, ai) {
			// Append continuation text into the previous row.
			prev := &merged[len(merged)-1]
			for ci := range prev.Cells {
				if ci < len(row.Cells) && row.Cells[ci].Text != "" {
					if prev.Cells[ci].Text != "" {
						prev.Cells[ci].Text += " " + row.Cells[ci].Text
					} else {
						prev.Cells[ci].Text = row.Cells[ci].Text
					}
					prev.Cells[ci].Spans = append(prev.Cells[ci].Spans, row.Cells[ci].Spans...)
				}
			}
		}
	}
	return merged
}

// isContinuationRow returns true if the row looks like a description
// continuation: it must have non-numeric text in the column immediately
// after the anchor (the "description" column). Rows with content only
// in distant columns (summaries, totals) are not continuations.
func isContinuationRow(row Row, anchorIdx int) bool {
	descIdx := anchorIdx + 1
	if descIdx >= len(row.Cells) {
		return false
	}
	return row.Cells[descIdx].Text != "" && !isAllNumeric(row.Cells[descIdx].Text)
}

func isAllNumeric(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' || r == '.' || r == ',' || r == '-' || r == ' ' {
			continue
		}
		return false
	}
	return true
}

func mergeCloseRows(rows []tableRow, mergeGap float64) []tableRow {
	if len(rows) == 0 {
		return rows
	}
	merged := make([]tableRow, 0, len(rows))
	merged = append(merged, rows[0])
	for i := 1; i < len(rows); i++ {
		prev := &merged[len(merged)-1]
		if prev.yBottom-rows[i].y < mergeGap {
			prev.spans = append(prev.spans, rows[i].spans...)
			if rows[i].yBottom < prev.yBottom {
				prev.yBottom = rows[i].yBottom
			}
		} else {
			merged = append(merged, rows[i])
		}
	}
	// Re-sort spans only for rows that absorbed additional spans.
	for i := range merged {
		if len(merged[i].spans) > 1 {
			sortSpansByX(merged[i].spans)
		}
	}
	return merged
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
