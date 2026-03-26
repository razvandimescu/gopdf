package main

import (
	"github.com/razvandimescu/gopdf/pdf"
)

func createSampleReport() ([]byte, error) {
	c := pdf.NewCreator()
	p := c.NewPage(595, 842) // A4

	// Warm palette — contrast with the cool navy invoice.
	charcoal := [3]float64{0.149, 0.149, 0.165}  // #262629
	coral := [3]float64{0.918, 0.333, 0.259}      // #EA5542
	sand := [3]float64{0.957, 0.933, 0.898}        // #F4EEE5
	warmGray := [3]float64{0.600, 0.573, 0.545}    // #99928B
	darkText := [3]float64{0.133, 0.133, 0.133}

	pageW := 595.0
	pageH := 842.0

	// ─── BACKGROUND ─────────────────────────────────────────────────
	// Full sand background.
	p.FillRect(0, 0, pageW, pageH, sand[0], sand[1], sand[2])

	// Large charcoal block — left side, top portion.
	p.FillRect(0, pageH-520, 380, 520, charcoal[0], charcoal[1], charcoal[2])

	// Coral accent bar — thin vertical on the right edge of the dark block.
	p.FillRect(380, pageH-520, 8, 520, coral[0], coral[1], coral[2])

	// ─── YEAR ───────────────────────────────────────────────────────
	// Large year on the sand side.
	p.SetColor(warmGray[0], warmGray[1], warmGray[2])
	p.SetFont("Helvetica-Bold", 72)
	p.DrawText(410, pageH-100, "2026")

	// ─── TITLE (on dark block) ──────────────────────────────────────
	p.SetColor(1, 1, 1)
	p.SetFont("Helvetica-Bold", 42)
	p.DrawText(40, pageH-140, "Annual")
	p.DrawText(40, pageH-190, "Growth")
	p.DrawText(40, pageH-240, "Report")

	// Coral underline accent.
	p.FillRect(40, pageH-260, 80, 4, coral[0], coral[1], coral[2])

	// ─── SUBTITLE ───────────────────────────────────────────────────
	p.SetColor(warmGray[0], warmGray[1], warmGray[2])
	p.SetFont("Helvetica", 13)
	p.DrawText(40, pageH-290, "Strategic Performance Review")
	p.DrawText(40, pageH-308, "& Market Analysis")

	// ─── KEY METRICS (on dark block) ────────────────────────────────
	metricsY := pageH - 380

	// Metric 1.
	p.SetColor(coral[0], coral[1], coral[2])
	p.SetFont("Helvetica-Bold", 36)
	p.DrawText(40, metricsY, "+42%")
	p.SetColor(warmGray[0], warmGray[1], warmGray[2])
	p.SetFont("Helvetica", 10)
	p.DrawText(40, metricsY-20, "Revenue Growth")

	// Metric 2.
	p.SetColor(1, 1, 1)
	p.SetFont("Helvetica-Bold", 36)
	p.DrawText(190, metricsY, "1.2M")
	p.SetColor(warmGray[0], warmGray[1], warmGray[2])
	p.SetFont("Helvetica", 10)
	p.DrawText(190, metricsY-20, "Active Users")

	// Divider line.
	p.FillRect(40, metricsY-40, 300, 1, warmGray[0], warmGray[1], warmGray[2])

	// Metric 3.
	p.SetColor(1, 1, 1)
	p.SetFont("Helvetica-Bold", 36)
	p.DrawText(40, metricsY-70, "98.7%")
	p.SetColor(warmGray[0], warmGray[1], warmGray[2])
	p.SetFont("Helvetica", 10)
	p.DrawText(40, metricsY-90, "Uptime SLA")

	// Metric 4.
	p.SetColor(coral[0], coral[1], coral[2])
	p.SetFont("Helvetica-Bold", 36)
	p.DrawText(190, metricsY-70, "24")
	p.SetColor(warmGray[0], warmGray[1], warmGray[2])
	p.SetFont("Helvetica", 10)
	p.DrawText(190, metricsY-90, "Markets Served")

	// ─── BAR CHART (on sand side) ───────────────────────────────────
	chartX := 410.0
	chartBaseY := 400.0
	barW := 28.0
	gap := 12.0
	bars := []struct {
		label  string
		height float64
		accent bool
	}{
		{"Q1", 80, false},
		{"Q2", 120, false},
		{"Q3", 100, false},
		{"Q4", 160, true},
	}

	// Chart label.
	p.SetColor(darkText[0], darkText[1], darkText[2])
	p.SetFont("Helvetica-Bold", 11)
	p.DrawText(chartX, chartBaseY+180, "Quarterly Revenue")

	p.SetColor(warmGray[0], warmGray[1], warmGray[2])
	p.SetFont("Helvetica", 9)
	p.DrawText(chartX, chartBaseY+165, "In millions USD")

	for i, bar := range bars {
		x := chartX + float64(i)*(barW+gap)
		if bar.accent {
			p.FillRect(x, chartBaseY, barW, bar.height, coral[0], coral[1], coral[2])
		} else {
			p.FillRect(x, chartBaseY, barW, bar.height, charcoal[0], charcoal[1], charcoal[2])
		}
		// Label below bar.
		p.SetColor(warmGray[0], warmGray[1], warmGray[2])
		p.SetFont("Helvetica", 9)
		p.DrawText(x+6, chartBaseY-14, bar.label)
	}

	// ─── ORGANIZATION (on sand side, bottom) ────────────────────────
	p.SetColor(darkText[0], darkText[1], darkText[2])
	p.SetFont("Helvetica-Bold", 14)
	p.DrawText(410, 200, "Meridian Ventures")

	p.SetColor(warmGray[0], warmGray[1], warmGray[2])
	p.SetFont("Helvetica", 10)
	p.DrawText(410, 183, "Confidential")

	// Small coral square — brand mark.
	p.FillRect(410, 140, 12, 12, coral[0], coral[1], coral[2])
	p.FillRect(426, 140, 12, 12, charcoal[0], charcoal[1], charcoal[2])

	// ─── FOOTER (on dark block) ─────────────────────────────────────
	p.SetColor(warmGray[0], warmGray[1], warmGray[2])
	p.SetFont("Helvetica", 7)
	p.DrawText(40, 30, "Generated with gopdf — github.com/razvandimescu/gopdf")

	return c.Build()
}
