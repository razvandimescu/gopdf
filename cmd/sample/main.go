package main

import (
	"fmt"
	"os"

	"github.com/razvandimescu/gopdf/pdf"
)

func main() {
	samples := []struct {
		name string
		fn   func() ([]byte, error)
	}{
		{"sample-invoice.pdf", createSampleInvoice},
		{"sample-report.pdf", createSampleReport},
	}

	for _, s := range samples {
		output := s.name
		data, err := s.fn()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating %s: %v\n", s.name, err)
			os.Exit(1)
		}
		if err := os.WriteFile(output, data, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", output, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Created %s (%d bytes)\n", output, len(data))
	}
}

func createSampleInvoice() ([]byte, error) {
	c := pdf.NewCreator()
	p := c.NewPage(595, 842) // A4

	// Color palette — deep teal + gold accent.
	teal := [3]float64{0.067, 0.180, 0.224}        // #112E39
	gold := [3]float64{0.878, 0.702, 0.353}         // #E0B35A
	darkText := [3]float64{0.133, 0.133, 0.133}
	medText := [3]float64{0.467, 0.467, 0.467}
	lightBg := [3]float64{0.969, 0.973, 0.976}      // #F7F8F9
	tableBorder := [3]float64{0.878, 0.886, 0.898}  // #E0E2E5

	pageW := 595.0
	pageH := 842.0
	marginL := 50.0
	contentW := pageW - marginL*2

	// ─── SIDE STRIPE ────────────────────────────────────────────────
	p.FillRect(0, 0, 40, pageH, teal[0], teal[1], teal[2])
	// Gold accent line inside stripe.
	p.FillRect(34, pageH-200, 3, 120, gold[0], gold[1], gold[2])

	// ─── HEADER ─────────────────────────────────────────────────────
	headerY := pageH - 60

	p.SetColor(teal[0], teal[1], teal[2])
	p.SetFont("Helvetica-Bold", 11)
	p.DrawText(marginL+10, headerY, "ACME CORPORATION")

	p.SetColor(medText[0], medText[1], medText[2])
	p.SetFont("Helvetica", 8)
	p.DrawText(marginL+10, headerY-14, "1234 Innovation Drive, San Francisco, CA 94107")
	p.DrawText(marginL+10, headerY-25, "hello@acmecorp.com  |  +1 (415) 555-0199")

	// ─── INVOICE TITLE ──────────────────────────────────────────────
	titleY := headerY - 70

	p.SetColor(teal[0], teal[1], teal[2])
	p.SetFont("Helvetica-Bold", 48)
	p.DrawText(marginL+10, titleY, "INVOICE")

	// Gold bar under title.
	p.FillRect(marginL+10, titleY-12, 100, 4, gold[0], gold[1], gold[2])

	// Invoice number — right side, large.
	p.SetColor(medText[0], medText[1], medText[2])
	p.SetFont("Helvetica", 9)
	p.DrawText(420, titleY+36, "INVOICE NO.")
	p.SetColor(teal[0], teal[1], teal[2])
	p.SetFont("Helvetica-Bold", 20)
	p.DrawText(420, titleY+12, "#0042")

	p.SetColor(medText[0], medText[1], medText[2])
	p.SetFont("Helvetica", 9)
	p.DrawText(420, titleY-8, "March 26, 2026")

	// ─── META CARDS ─────────────────────────────────────────────────
	metaY := titleY - 55

	// Bill To card — light background.
	p.FillRect(marginL+10, metaY-52, 220, 65, lightBg[0], lightBg[1], lightBg[2])
	p.SetColor(medText[0], medText[1], medText[2])
	p.SetFont("Helvetica", 8)
	p.DrawText(marginL+18, metaY, "BILL TO")
	p.SetColor(darkText[0], darkText[1], darkText[2])
	p.SetFont("Helvetica-Bold", 11)
	p.DrawText(marginL+18, metaY-16, "Riverside Studios Ltd")
	p.SetFont("Helvetica", 9)
	p.DrawText(marginL+18, metaY-30, "42 Thames Wharf, London SE1 7PB")
	p.DrawText(marginL+18, metaY-42, "United Kingdom")

	// Due date card.
	p.FillRect(marginL+250, metaY-52, 150, 65, lightBg[0], lightBg[1], lightBg[2])
	p.SetColor(medText[0], medText[1], medText[2])
	p.SetFont("Helvetica", 8)
	p.DrawText(marginL+258, metaY, "DUE DATE")
	p.SetColor(darkText[0], darkText[1], darkText[2])
	p.SetFont("Helvetica-Bold", 14)
	p.DrawText(marginL+258, metaY-20, "Apr 25, 2026")

	// Status badge.
	p.FillRect(marginL+420, metaY-52, 70, 65, gold[0], gold[1], gold[2])
	p.SetColor(teal[0], teal[1], teal[2])
	p.SetFont("Helvetica-Bold", 10)
	p.DrawText(marginL+432, metaY-14, "UNPAID")

	// ─── TABLE ──────────────────────────────────────────────────────
	tableTop := metaY - 80
	rowH := 32.0
	colDesc := marginL + 10.0
	colQty := marginL + 290.0
	colRate := marginL + 360.0
	colAmt := marginL + 440.0

	// Table header.
	p.FillRect(marginL+10, tableTop-rowH+6, contentW-10, rowH, teal[0], teal[1], teal[2])
	p.SetColor(1, 1, 1)
	p.SetFont("Helvetica-Bold", 9)
	p.DrawText(colDesc+10, tableTop-rowH+18, "DESCRIPTION")
	p.DrawText(colQty, tableTop-rowH+18, "QTY")
	p.DrawText(colRate, tableTop-rowH+18, "RATE")
	p.DrawText(colAmt, tableTop-rowH+18, "AMOUNT")

	type row struct {
		desc, qty, rate, amount string
	}
	rows := []row{
		{"Brand Identity & Logo Design", "1", "$4,500", "$4,500.00"},
		{"Website Design (8 pages)", "1", "$12,000", "$12,000.00"},
		{"UI/UX Consultation (per hour)", "24", "$175", "$4,200.00"},
		{"Photography Direction", "1", "$2,800", "$2,800.00"},
		{"Print Collateral Package", "1", "$3,200", "$3,200.00"},
	}

	y := tableTop - rowH
	for i, r := range rows {
		y -= rowH
		// Alternating backgrounds.
		if i%2 == 0 {
			p.FillRect(marginL+10, y+6, contentW-10, rowH, lightBg[0], lightBg[1], lightBg[2])
		}
		// Bottom border.
		p.FillRect(marginL+10, y+5, contentW-10, 1, tableBorder[0], tableBorder[1], tableBorder[2])

		p.SetColor(darkText[0], darkText[1], darkText[2])
		p.SetFont("Helvetica", 10)
		p.DrawText(colDesc+10, y+18, r.desc)
		p.SetColor(medText[0], medText[1], medText[2])
		p.DrawText(colQty, y+18, r.qty)
		p.DrawText(colRate, y+18, r.rate)
		p.SetColor(darkText[0], darkText[1], darkText[2])
		p.SetFont("Helvetica-Bold", 10)
		p.DrawText(colAmt, y+18, r.amount)
	}

	// ─── TOTALS ─────────────────────────────────────────────────────
	totalsY := y - 10
	totalsX := colRate - 10.0
	totalsW := contentW - (totalsX - marginL) - 10

	p.SetFont("Helvetica", 10)
	p.SetColor(medText[0], medText[1], medText[2])
	p.DrawText(totalsX, totalsY-5, "Subtotal")
	p.SetColor(darkText[0], darkText[1], darkText[2])
	p.DrawText(colAmt, totalsY-5, "$26,700.00")

	p.SetColor(medText[0], medText[1], medText[2])
	p.DrawText(totalsX, totalsY-22, "Tax (10%)")
	p.SetColor(darkText[0], darkText[1], darkText[2])
	p.DrawText(colAmt, totalsY-22, "$2,670.00")

	// Separator.
	p.FillRect(totalsX, totalsY-35, totalsW, 1, tableBorder[0], tableBorder[1], tableBorder[2])

	// Total bar.
	totalBarY := totalsY - 60
	p.FillRect(totalsX-5, totalBarY, totalsW+25, 30, teal[0], teal[1], teal[2])
	// Gold accent on left edge of total bar.
	p.FillRect(totalsX-5, totalBarY, 4, 30, gold[0], gold[1], gold[2])
	p.SetColor(1, 1, 1)
	p.SetFont("Helvetica-Bold", 11)
	p.DrawText(totalsX+8, totalBarY+10, "TOTAL DUE")
	p.SetFont("Helvetica-Bold", 14)
	p.DrawText(colAmt, totalBarY+10, "$29,370.00")

	// ─── PAYMENT + NOTES ────────────────────────────────────────────
	payY := totalBarY - 50

	// Payment box.
	p.FillRect(marginL+10, payY-70, 230, 85, lightBg[0], lightBg[1], lightBg[2])
	p.FillRect(marginL+10, payY-70, 3, 85, gold[0], gold[1], gold[2]) // gold left accent

	p.SetColor(medText[0], medText[1], medText[2])
	p.SetFont("Helvetica", 8)
	p.DrawText(marginL+22, payY, "PAYMENT DETAILS")

	p.SetColor(darkText[0], darkText[1], darkText[2])
	p.SetFont("Helvetica", 9)
	p.DrawText(marginL+22, payY-16, "Bank: First National Bank")
	p.DrawText(marginL+22, payY-30, "Account: 1234 5678 9012")
	p.DrawText(marginL+22, payY-44, "Sort Code: 12-34-56")
	p.DrawText(marginL+22, payY-58, "Ref: INV-2026-0042")

	// Notes box.
	p.FillRect(marginL+260, payY-70, 230, 85, lightBg[0], lightBg[1], lightBg[2])
	p.FillRect(marginL+260, payY-70, 3, 85, teal[0], teal[1], teal[2]) // teal left accent

	p.SetColor(medText[0], medText[1], medText[2])
	p.SetFont("Helvetica", 8)
	p.DrawText(marginL+272, payY, "NOTES")

	p.SetColor(darkText[0], darkText[1], darkText[2])
	p.SetFont("Helvetica", 9)
	p.DrawText(marginL+272, payY-16, "Payment due within 30 days.")
	p.DrawText(marginL+272, payY-30, "Late payments subject to")
	p.DrawText(marginL+272, payY-44, "1.5% monthly interest.")
	p.SetFont("Helvetica-Bold", 9)
	p.DrawText(marginL+272, payY-58, "Thank you for your business!")

	// ─── FOOTER ─────────────────────────────────────────────────────
	footerY := 35.0
	p.FillRect(marginL+10, footerY+12, contentW-10, 1, tableBorder[0], tableBorder[1], tableBorder[2])

	p.SetColor(medText[0], medText[1], medText[2])
	p.SetFont("Helvetica", 7)
	p.DrawText(marginL+10, footerY, "Generated with gopdf")
	p.DrawText(400, footerY, "github.com/razvandimescu/gopdf")

	return c.Build()
}
