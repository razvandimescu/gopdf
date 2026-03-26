package main

import (
	"fmt"
	"os"

	"github.com/razvandimescu/gopdf/pdf"
)

func main() {
	output := "sample.pdf"
	if len(os.Args) > 1 {
		output = os.Args[1]
	}

	data, err := createSampleInvoice()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(output, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Created %s (%d bytes)\n", output, len(data))
}

func createSampleInvoice() ([]byte, error) {
	c := pdf.NewCreator()
	p := c.NewPage(595, 842) // A4

	// Color palette.
	navy := [3]float64{0.102, 0.137, 0.208}       // #1A2335
	accent := [3]float64{0.259, 0.522, 0.957}      // #4285F4
	darkText := [3]float64{0.133, 0.133, 0.133}    // #222
	medText := [3]float64{0.400, 0.400, 0.400}     // #666
	lightBg := [3]float64{0.965, 0.969, 0.976}     // #F6F7F9
	white := [3]float64{1, 1, 1}
	_ = white

	pageW := 595.0
	marginL := 50.0
	marginR := 50.0
	contentW := pageW - marginL - marginR

	// ─── HEADER BLOCK ───────────────────────────────────────────────
	headerH := 120.0
	headerY := 842 - headerH
	p.FillRect(0, headerY, pageW, headerH, navy[0], navy[1], navy[2])

	// Accent stripe on left.
	p.FillRect(0, headerY, 6, headerH, accent[0], accent[1], accent[2])

	// Company name.
	p.SetColor(1, 1, 1)
	p.SetFont("Helvetica-Bold", 28)
	p.DrawText(marginL+10, headerY+70, "ACME CORPORATION")

	// Company details.
	p.SetFont("Helvetica", 9)
	p.DrawText(marginL+10, headerY+48, "1234 Innovation Drive, San Francisco, CA 94107")
	p.DrawText(marginL+10, headerY+36, "hello@acmecorp.com  |  +1 (415) 555-0199  |  acmecorp.com")

	// INVOICE label (right side).
	p.SetFont("Helvetica-Bold", 36)
	p.DrawText(395, headerY+68, "INVOICE")
	p.SetFont("Helvetica", 10)
	p.DrawText(430, headerY+48, "#INV-2026-0042")

	// ─── INVOICE META ───────────────────────────────────────────────
	metaY := headerY - 40

	p.SetColor(medText[0], medText[1], medText[2])
	p.SetFont("Helvetica", 9)
	p.DrawText(marginL, metaY, "BILL TO")
	p.DrawText(250, metaY, "INVOICE DATE")
	p.DrawText(380, metaY, "DUE DATE")

	p.SetColor(darkText[0], darkText[1], darkText[2])
	p.SetFont("Helvetica-Bold", 11)
	p.DrawText(marginL, metaY-16, "Riverside Studios Ltd")
	p.DrawText(250, metaY-16, "March 26, 2026")
	p.DrawText(380, metaY-16, "April 25, 2026")

	p.SetFont("Helvetica", 10)
	p.DrawText(marginL, metaY-30, "42 Thames Wharf, London SE1 7PB")
	p.DrawText(marginL, metaY-43, "United Kingdom")

	// ─── TABLE ──────────────────────────────────────────────────────
	tableTop := metaY - 75
	rowH := 30.0
	colX := []float64{marginL, marginL + 260, marginL + 340, marginL + 420}
	headers := []string{"Description", "Qty", "Rate", "Amount"}

	// Table header.
	p.FillRect(marginL, tableTop-rowH+4, contentW, rowH, navy[0], navy[1], navy[2])
	p.SetColor(1, 1, 1)
	p.SetFont("Helvetica-Bold", 9)
	for i, h := range headers {
		p.DrawText(colX[i]+8, tableTop-rowH+16, h)
	}

	// Table rows.
	type row struct {
		desc, qty, rate, amount string
	}
	rows := []row{
		{"Brand Identity & Logo Design", "1", "$4,500.00", "$4,500.00"},
		{"Website Design (8 pages)", "1", "$12,000.00", "$12,000.00"},
		{"UI/UX Consultation (per hour)", "24", "$175.00", "$4,200.00"},
		{"Photography Direction", "1", "$2,800.00", "$2,800.00"},
		{"Print Collateral Package", "1", "$3,200.00", "$3,200.00"},
	}

	y := tableTop - rowH
	for i, r := range rows {
		y -= rowH
		// Alternating row background.
		if i%2 == 0 {
			p.FillRect(marginL, y+4, contentW, rowH, lightBg[0], lightBg[1], lightBg[2])
		}
		p.SetColor(darkText[0], darkText[1], darkText[2])
		p.SetFont("Helvetica", 10)
		p.DrawText(colX[0]+8, y+14, r.desc)
		p.DrawText(colX[1]+8, y+14, r.qty)
		p.DrawText(colX[2]+8, y+14, r.rate)

		p.SetFont("Helvetica-Bold", 10)
		p.DrawText(colX[3]+8, y+14, r.amount)
	}

	// ─── TOTALS ─────────────────────────────────────────────────────
	totalsY := y - 20
	totalsX := colX[2]

	// Separator line.
	p.FillRect(totalsX, totalsY+12, contentW-(totalsX-marginL), 1, medText[0], medText[1], medText[2])

	p.SetFont("Helvetica", 10)
	p.SetColor(medText[0], medText[1], medText[2])
	p.DrawText(totalsX+8, totalsY-5, "Subtotal")
	p.SetColor(darkText[0], darkText[1], darkText[2])
	p.DrawText(colX[3]+8, totalsY-5, "$26,700.00")

	p.SetColor(medText[0], medText[1], medText[2])
	p.DrawText(totalsX+8, totalsY-22, "Tax (10%)")
	p.SetColor(darkText[0], darkText[1], darkText[2])
	p.DrawText(colX[3]+8, totalsY-22, "$2,670.00")

	// Total highlight.
	totalBarY := totalsY - 50
	p.FillRect(totalsX, totalBarY, contentW-(totalsX-marginL), 30, accent[0], accent[1], accent[2])
	p.SetColor(1, 1, 1)
	p.SetFont("Helvetica-Bold", 12)
	p.DrawText(totalsX+8, totalBarY+10, "TOTAL DUE")
	p.SetFont("Helvetica-Bold", 14)
	p.DrawText(colX[3]+8, totalBarY+10, "$29,370.00")

	// ─── PAYMENT INFO ───────────────────────────────────────────────
	payY := totalBarY - 50

	p.SetColor(medText[0], medText[1], medText[2])
	p.SetFont("Helvetica", 9)
	p.DrawText(marginL, payY, "PAYMENT DETAILS")

	p.SetColor(darkText[0], darkText[1], darkText[2])
	p.SetFont("Helvetica", 10)
	p.DrawText(marginL, payY-16, "Bank: First National Bank")
	p.DrawText(marginL, payY-30, "Account: 1234 5678 9012")
	p.DrawText(marginL, payY-44, "Sort Code: 12-34-56")

	p.SetColor(medText[0], medText[1], medText[2])
	p.SetFont("Helvetica", 9)
	p.DrawText(300, payY, "NOTES")

	p.SetColor(darkText[0], darkText[1], darkText[2])
	p.SetFont("Helvetica", 10)
	p.DrawText(300, payY-16, "Payment due within 30 days.")
	p.DrawText(300, payY-30, "Late payments subject to 1.5% monthly interest.")

	// ─── FOOTER ─────────────────────────────────────────────────────
	footerY := 40.0

	// Footer line.
	p.FillRect(marginL, footerY+15, contentW, 1, lightBg[0], lightBg[1], lightBg[2])

	p.SetColor(medText[0], medText[1], medText[2])
	p.SetFont("Helvetica", 8)
	p.DrawText(marginL, footerY, "Thank you for your business. This invoice was generated with gopdf — github.com/razvandimescu/gopdf")

	// Accent dot.
	p.FillRect(pageW-marginR-8, footerY-2, 8, 8, accent[0], accent[1], accent[2])

	return c.Build()
}
