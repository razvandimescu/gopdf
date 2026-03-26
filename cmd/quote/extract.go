package main

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/razvandimescu/gopdf/pdf"
)

// QuoteData holds structured data extracted from a Neville Lumb quotation PDF.
type QuoteData struct {
	Company       string
	QuoteName     string
	QuotationRef  string
	IssueDate     string
	QuoteExpiry   string
	Estimator     string
	ContactName   string
	TableHeaders    TableHeader
	LineItems       []LineItem
	SupplierCodes   []string // unique, in document order
	lastCategory    string   // carries across pages
	tableComplete   bool     // set when footer detected
}

// TableHeader holds the detected column names in document order.
type TableHeader struct {
	Columns []string // header names in left-to-right order
}

// LineItem is a single row from the quotation table.
type LineItem struct {
	Quantity     string
	ProductCode  string
	SupplierCode string
	Description  string
	Prices       map[string]string // price column name → value
	Category     string // WC, BASIN, SINK, etc.
}

// ExtractQuote extracts structured quotation data from a PDF document.
func ExtractQuote(doc *pdf.Document) QuoteData {
	var q QuoteData

	// Extract text from all pages.
	for i := 0; i < doc.NumPages(); i++ {
		lines, _ := doc.Page(i).TextLines()
		extractHeaderFields(lines, &q)
	}

	// Table extraction uses spans directly for column precision.
	// Tables can span multiple pages — detect columns on each page that has a header row.
	// Continuation pages may repeat the header or just continue with data rows.
	var cols *tableColumns
	for i := 0; i < doc.NumPages(); i++ {
		spans, _ := doc.Page(i).TextSpans()
		pageCols := findTableColumns(spans)
		if pageCols != nil {
			cols = pageCols
			if q.TableHeaders.Columns == nil {
				q.TableHeaders = cols.headers
			}
		}
		if cols != nil {
			extractTablePage(spans, cols, &q)
		}
	}

	// Collect supplier codes from final line items (after continuation merges).
	for _, item := range q.LineItems {
		code := item.SupplierCode
		if code != "" && code != "-" && !isNoteRef(code) {
			q.SupplierCodes = append(q.SupplierCodes, code)
		}
	}
	q.SupplierCodes = uniqueStrings(q.SupplierCodes)

	return q
}

func extractHeaderFields(lines []pdf.TextLine, q *QuoteData) {
	for i, line := range lines {
		text := line.Text

		if v := extractLabelValue(text, "Quote Name:"); v != "" && q.QuoteName == "" {
			q.QuoteName = v
		}
		if v := extractCompany(text); v != "" && q.Company == "" {
			q.Company = v
		}
		if strings.Contains(text, "Quotation Ref:") && q.QuotationRef == "" {
			// Value might be on the same line or next line.
			v := extractLabelValue(text, "Quotation Ref:")
			if v == "" && i+1 < len(lines) {
				v = strings.TrimSpace(lines[i+1].Text)
			}
			q.QuotationRef = v
		}
		if strings.Contains(text, "Issue Date:") && q.IssueDate == "" {
			v := extractLabelValue(text, "Issue Date:")
			if v == "" && i+1 < len(lines) {
				v = strings.TrimSpace(lines[i+1].Text)
			}
			q.IssueDate = v
		}
		if v := extractLabelValue(text, "Quote Expiry:"); v != "" && q.QuoteExpiry == "" {
			// Often "Quote is valid until DD/MM/YYYY"
			v = strings.TrimPrefix(v, "Quote is valid until ")
			q.QuoteExpiry = v
		}
		if v := extractEstimator(text); v != "" && q.Estimator == "" {
			q.Estimator = v
		}
		if strings.Contains(text, "Contact Name:") && q.ContactName == "" {
			v := extractBetweenLabels(text, "Contact Name:", "Estimator")
			if v != "" {
				q.ContactName = v
			}
		}
	}
}

func extractLabelValue(text, label string) string {
	idx := strings.Index(text, label)
	if idx < 0 {
		return ""
	}
	v := strings.TrimSpace(text[idx+len(label):])
	return v
}

func extractCompany(text string) string {
	idx := strings.Index(text, "Company:")
	if idx < 0 {
		return ""
	}
	v := text[idx+len("Company:"):]
	// Company value ends at "Estimator:" if present on same line.
	if eidx := strings.Index(v, "Estimator:"); eidx > 0 {
		v = v[:eidx]
	}
	return strings.TrimSpace(v)
}

func extractBetweenLabels(text, startLabel, stopPrefix string) string {
	idx := strings.Index(text, startLabel)
	if idx < 0 {
		return ""
	}
	v := text[idx+len(startLabel):]
	if sidx := strings.Index(v, stopPrefix); sidx > 0 {
		v = v[:sidx]
	}
	return strings.TrimSpace(v)
}

func extractEstimator(text string) string {
	idx := strings.Index(text, "Estimator:")
	if idx < 0 {
		return ""
	}
	v := text[idx+len("Estimator:"):]
	// Trim at next label if present.
	for _, stop := range []string{"Estimator Tel:", "Contact"} {
		if sidx := strings.Index(v, stop); sidx > 0 {
			v = v[:sidx]
		}
	}
	return strings.TrimSpace(v)
}

// colDef is a named column with an X position.
type colDef struct {
	name string
	x    float64
}

// tableColumns holds detected column positions.
type tableColumns struct {
	qtyX, prodX, suppX, descX float64
	priceCols                 []colDef // variable price columns (right of description)
	headerY                   float64
	headers                   TableHeader
}

func extractTablePage(spans []pdf.TextSpan, cols *tableColumns, q *QuoteData) {
	// If table was already completed (footer found), only continue if this page has its own header.
	pageCols := findTableColumns(spans)
	if q.tableComplete && pageCols == nil {
		return // no new table on this page
	}
	if pageCols != nil {
		q.tableComplete = false // new table section
	}

	// Determine header Y for this page.
	headerY := -math.MaxFloat64 // include all spans by default
	if pageCols != nil {
		headerY = pageCols.headerY
		// Use this page's column positions (may differ slightly).
		cols = pageCols
	}

	type row struct {
		y     float64
		spans []pdf.TextSpan
	}

	const yTol = 2.0
	var rows []row

	for _, sp := range spans {
		if headerY > -math.MaxFloat64 && sp.Y >= headerY-yTol {
			continue // at or above header
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
			rows = append(rows, row{y: sp.Y, spans: []pdf.TextSpan{sp}})
		}
	}

	// Sort rows top-to-bottom.
	sort.Slice(rows, func(i, j int) bool { return rows[i].y > rows[j].y })

	seenFooter := false

	for _, r := range rows {
		if seenFooter {
			break
		}

		sort.Slice(r.spans, func(i, j int) bool { return r.spans[i].X < r.spans[j].X })

		// Classify each span into a column.
		var qty, prod, supp, desc string
		prices := make(map[string]string)
		for _, sp := range r.spans {
			text := strings.TrimSpace(sp.Text)
			if text == "" {
				continue
			}

			// Check for footer markers.
			if strings.HasPrefix(text, "Quote Expiry") ||
				strings.HasPrefix(text, "Pricing:") ||
				strings.HasPrefix(text, "Total:") ||
				strings.HasPrefix(text, "Quote Total:") ||
				(strings.HasPrefix(text, "Logistics Charge:") && sp.X < cols.qtyX+10) ||
				(strings.HasPrefix(text, "Delivery Charge:") && sp.X < cols.qtyX+10) {
				seenFooter = true
				q.tableComplete = true
				break
			}

			col := classifyColumn(sp.X, cols)
			switch col {
			case "qty":
				qty = appendText(qty, text)
			case "prod":
				prod = appendText(prod, text)
			case "supp":
				supp = appendText(supp, text)
			case "desc":
				desc = appendText(desc, text)
			default:
				// Price column.
				prices[col] = appendText(prices[col], text)
			}
		}

		if seenFooter {
			break
		}

		// Skip empty rows.
		if supp == "" && qty == "" && prod == "" && desc == "" {
			continue
		}

		// A data row has a numeric quantity (e.g., "2.00", "1.00").
		isDataRow := isNumeric(qty) && (supp != "" || prod != "")

		if !isDataRow {
			// Check if this is a continuation of the previous data row
			// (text in prod/supp/desc columns but no quantity).
			isContinuation := qty == "" && (prod != "" || supp != "") && len(q.LineItems) > 0
			if isContinuation {
				prev := &q.LineItems[len(q.LineItems)-1]
				if prod != "" {
					prev.ProductCode = smartAppend(prev.ProductCode, prod)
				}
				if supp != "" {
					prev.SupplierCode = smartAppend(prev.SupplierCode, supp)
				}
				if desc != "" {
					prev.Description = appendText(prev.Description, desc)
				}
				continue
			}

			// Category header or note line.
			var parts []string
			for _, sp := range r.spans {
				t := strings.TrimSpace(sp.Text)
				if t != "" {
					parts = append(parts, t)
				}
			}
			label := strings.Join(parts, " ")
			if label != "" {
				q.lastCategory = label
			}
			continue
		}

		q.LineItems = append(q.LineItems, LineItem{
			Quantity:     qty,
			ProductCode:  prod,
			SupplierCode: supp,
			Description:  desc,
			Prices:       prices,
			Category:     q.lastCategory,
		})
	}
}

// isIncompleteHeader returns true if a header span looks like it wraps to a second line.
func isIncompleteHeader(text string) bool {
	complete := map[string]bool{
		"quantity": true, "product code": true, "suppliers code": true,
		"product description": true, "unit price": true, "total price": true,
		"list price": true, "cost price": true, "selling price": true,
	}
	return !complete[strings.ToLower(text)]
}

// isContinuationWord returns true if the word is a typical header continuation.
func isContinuationWord(text string) bool {
	words := map[string]bool{
		"code": true, "price": true, "name": true, "description": true,
	}
	return words[strings.ToLower(text)]
}

func findTableColumns(spans []pdf.TextSpan) *tableColumns {
	const yTol = 2.0

	// Strategy 1: look for "Suppliers Code" as a single span.
	// Strategy 2: look for "Suppliers" span on a line with "Quantity" (multi-line header).
	var anchorY float64
	var anchorFound bool

	for _, sp := range spans {
		text := strings.TrimSpace(sp.Text)
		if text == "Suppliers Code" || text == "Suppliers" {
			// Verify this line also has "Quantity".
			for _, s := range spans {
				if math.Abs(s.Y-sp.Y) <= yTol && strings.TrimSpace(s.Text) == "Quantity" {
					anchorY = sp.Y
					anchorFound = true
					break
				}
			}
			if anchorFound {
				break
			}
		}
	}
	if !anchorFound {
		return nil
	}

	// Collect all header spans on this row.
	type hdrSpan struct {
		text string
		x    float64
	}
	var headerSpans []hdrSpan
	for _, s := range spans {
		if math.Abs(s.Y-anchorY) > yTol {
			continue
		}
		text := strings.TrimSpace(s.Text)
		if text != "" {
			headerSpans = append(headerSpans, hdrSpan{text, s.X})
		}
	}
	sort.Slice(headerSpans, func(i, j int) bool { return headerSpans[i].x < headerSpans[j].x })

	// Check if any first-row headers are incomplete (e.g., "Product" without "Code").
	hasIncomplete := false
	for _, h := range headerSpans {
		if isIncompleteHeader(h.text) {
			hasIncomplete = true
			break
		}
	}

	// Only look for a second header row if we detected incomplete headers.
	if hasIncomplete {
		var secondRow []hdrSpan
		for _, s := range spans {
			dy := anchorY - s.Y
			if dy > yTol && dy < 15 {
				text := strings.TrimSpace(s.Text)
				if text != "" && isContinuationWord(text) {
					secondRow = append(secondRow, hdrSpan{text, s.X})
				}
			}
		}
		sort.Slice(secondRow, func(i, j int) bool { return secondRow[i].x < secondRow[j].x })

		// Match each continuation word to the nearest incomplete header to its left.
		for _, sr := range secondRow {
			bestIdx := -1
			bestDist := math.MaxFloat64
			for i, h := range headerSpans {
				if !isIncompleteHeader(h.text) {
					continue
				}
				// Header must be to the left of (or near) the continuation.
				if h.x <= sr.x+20 {
					dist := sr.x - h.x
					if dist >= 0 && dist < bestDist {
						bestDist = dist
						bestIdx = i
					}
				}
			}
			if bestIdx >= 0 {
				headerSpans[bestIdx].text += " " + sr.text
			}
		}
	}

	cols := &tableColumns{headerY: anchorY}

	// Classify known columns and collect price columns.
	var headerNames []string
	for _, h := range headerSpans {
		name := h.text
		headerNames = append(headerNames, name)

		nameLower := strings.ToLower(name)
		switch {
		case nameLower == "quantity":
			cols.qtyX = h.x
		case strings.HasPrefix(nameLower, "product c") || nameLower == "product code":
			cols.prodX = h.x
		case strings.HasPrefix(nameLower, "supplier"):
			cols.suppX = h.x
		case strings.HasPrefix(nameLower, "product d") || nameLower == "product description":
			cols.descX = h.x
		default:
			// Anything right of description that contains "price" or is a remaining column.
			if strings.Contains(nameLower, "price") || strings.Contains(nameLower, "cost") {
				cols.priceCols = append(cols.priceCols, colDef{name: name, x: h.x})
			}
		}
	}

	cols.headers = TableHeader{Columns: headerNames}

	// If we have incomplete headers, shift headerY down to below the continuation row
	// so data rows are correctly identified.
	if hasIncomplete {
		for _, s := range spans {
			dy := anchorY - s.Y
			if dy > yTol && dy < 15 {
				if s.Y < cols.headerY {
					cols.headerY = s.Y
				}
			}
		}
	}

	return cols
}

// classifyColumn returns "qty", "prod", "supp", "desc", or a price column name.
func classifyColumn(x float64, cols *tableColumns) string {
	// Build list of all columns.
	defs := []colDef{
		{"qty", cols.qtyX},
		{"prod", cols.prodX},
		{"supp", cols.suppX},
		{"desc", cols.descX},
	}
	defs = append(defs, cols.priceCols...)

	best := ""
	bestDist := math.MaxFloat64
	for _, d := range defs {
		if d.x == 0 {
			continue
		}
		dist := math.Abs(x - d.x)
		if dist < bestDist {
			bestDist = dist
			best = d.name
		}
	}

	if bestDist > 50 {
		if x < cols.prodX {
			return "qty"
		}
		if x < cols.suppX {
			return "prod"
		}
		if x < cols.descX {
			return "supp"
		}
		return "desc"
	}

	return best
}

// smartAppend joins text, omitting the space if the existing part ends with - or /.
func smartAppend(existing, text string) string {
	if existing == "" {
		return text
	}
	if strings.HasSuffix(existing, "-") || strings.HasSuffix(existing, "/") {
		return existing + text
	}
	return existing + " " + text
}

func appendText(existing, text string) string {
	if existing == "" {
		return text
	}
	return existing + " " + text
}

func isNumeric(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, c := range s {
		if c != '.' && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func isNoteRef(s string) bool {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.HasPrefix(s, "NOTE")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func uniqueStrings(ss []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

func (q QuoteData) String() string {
	var b strings.Builder

	fmt.Fprintf(&b, "Company:        %s\n", q.Company)
	fmt.Fprintf(&b, "Quote Name:     %s\n", q.QuoteName)
	fmt.Fprintf(&b, "Quotation Ref:  %s\n", q.QuotationRef)
	fmt.Fprintf(&b, "Issue Date:     %s\n", q.IssueDate)
	if q.QuoteExpiry != "" {
		fmt.Fprintf(&b, "Quote Expiry:   %s\n", q.QuoteExpiry)
	}
	if q.Estimator != "" {
		fmt.Fprintf(&b, "Estimator:      %s\n", q.Estimator)
	}
	if q.ContactName != "" {
		fmt.Fprintf(&b, "Contact Name:   %s\n", q.ContactName)
	}

	h := q.TableHeaders
	if len(h.Columns) > 0 {
		fmt.Fprintf(&b, "\nTable Headers: %s\n", strings.Join(h.Columns, " | "))
	}

	// Collect price column names from headers (everything after the 4 fixed columns).
	var priceCols []string
	for _, col := range h.Columns {
		lower := strings.ToLower(col)
		if strings.Contains(lower, "price") || strings.Contains(lower, "cost") {
			priceCols = append(priceCols, col)
		}
	}

	if len(q.LineItems) > 0 {
		fmt.Fprintf(&b, "\nLine Items (%d):\n", len(q.LineItems))

		// Header row.
		header := fmt.Sprintf("  %3s  %-5s %-12s  %-14s  %-42s", "#", "Qty", "Product", "Supplier", "Description")
		for _, pc := range priceCols {
			header += fmt.Sprintf("  %12s", truncate(pc, 12))
		}
		fmt.Fprintln(&b, header)
		fmt.Fprintf(&b, "  %s\n", strings.Repeat("-", len(header)))

		for i, item := range q.LineItems {
			cat := ""
			if item.Category != "" {
				cat = fmt.Sprintf(" [%s]", item.Category)
			}
			line := fmt.Sprintf("  %3d  %-5s %-12s  %-14s  %-42s",
				i+1, item.Quantity, item.ProductCode, item.SupplierCode,
				truncate(item.Description, 42))
			for _, pc := range priceCols {
				line += fmt.Sprintf("  %12s", item.Prices[pc])
			}
			fmt.Fprintf(&b, "%s%s\n", line, cat)
		}
	}

	if len(q.SupplierCodes) > 0 {
		fmt.Fprintf(&b, "\nUnique Supplier Codes (%d):\n", len(q.SupplierCodes))
		for _, code := range q.SupplierCodes {
			fmt.Fprintf(&b, "  %s\n", code)
		}
	}

	return b.String()
}
