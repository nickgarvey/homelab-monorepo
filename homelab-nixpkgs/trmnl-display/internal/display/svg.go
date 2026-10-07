package display

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/ngarvey/trmnl-display/internal/weather"
)

// svgW is the shared coordinate width for all inline SVGs.
// Matched to the full wp-left width (400px col - 2×8px padding - 100px right col - 8px gap = 276px).
const svgW = 276.0

// slotCX returns the x centre of slot i when there are n equal slots across svgW.
func slotCX(i, n int) float64 {
	slot := svgW / float64(n)
	return float64(i)*slot + slot/2
}

// conditionIconID maps a weather condition string to an SVG symbol id.
func conditionIconID(condition string) string {
	switch condition {
	case "Clear":
		return "wi-clear"
	case "Partly Cloudy":
		return "wi-partly"
	case "Overcast":
		return "wi-cloud"
	case "Foggy":
		return "wi-fog"
	case "Drizzle", "Showers":
		return "wi-showers"
	case "Rain":
		return "wi-rain"
	case "Snow":
		return "wi-snow"
	case "Thunderstorm":
		return "wi-thunder"
	default:
		return "wi-cloud"
	}
}

// precipIconID maps a WMO hourly weather code to a symbol id for the icon row.
// Every code maps to an icon — the row always shows one icon per slot.
func precipIconID(code int) string {
	switch {
	case code <= 1:
		return "wi-clear"
	case code == 2:
		return "wi-partly"
	case code == 3:
		return "wi-cloud" // overcast
	case code <= 48:
		return "wi-fog" // fog
	case code <= 57:
		return "wi-showers" // drizzle
	case code <= 63:
		return "wi-showers" // light/moderate rain
	case code <= 67:
		return "wi-rain" // heavy rain or freezing rain
	case code <= 77:
		return "wi-snow"
	case code <= 81:
		return "wi-showers" // light/moderate showers
	case code == 82:
		return "wi-rain" // violent showers
	case code <= 86:
		return "wi-snow" // snow showers
	case code >= 95:
		return "wi-thunder"
	default:
		return "wi-cloud"
	}
}

// filterEvery3Hours returns every 3rd hourly point (up to 8) — the shared x-axis set.
func filterEvery3Hours(hourly []weather.HourlyPoint) []weather.HourlyPoint {
	var out []weather.HourlyPoint
	for i, h := range hourly {
		if i%3 == 0 {
			out = append(out, h)
		}
		if len(out) >= 8 {
			break
		}
	}
	return out
}

// buildSparklineSVG returns a smooth temperature sparkline using the shared 8-slot x-axis.
// Uses a Catmull-Rom spline converted to cubic bezier for a smooth curve.
// No time labels — those appear on the bottom (condition icon) chart.
func buildSparklineSVG(hourly []weather.HourlyPoint) template.HTML {
	if len(hourly) == 0 {
		return ""
	}
	const h = 44.0
	const padY = 5.0

	minT, maxT := hourly[0].TempF, hourly[0].TempF
	for _, p := range hourly {
		if p.TempF < minT {
			minT = p.TempF
		}
		if p.TempF > maxT {
			maxT = p.TempF
		}
	}
	rangeT := maxT - minT
	if rangeT < 1 {
		rangeT = 1
	}

	type pt struct{ x, y float64 }
	n := len(hourly)
	pts := make([]pt, n)
	for i, p := range hourly {
		pts[i] = pt{
			x: slotCX(i, n),
			y: padY + (1.0-(p.TempF-minT)/rangeT)*(h-2*padY),
		}
	}

	// Build a smooth Catmull-Rom spline as an SVG cubic bezier path.
	var path strings.Builder
	path.WriteString(fmt.Sprintf("M %.1f,%.1f", pts[0].x, pts[0].y))
	for i := 0; i < n-1; i++ {
		i0 := i - 1
		if i0 < 0 {
			i0 = 0
		}
		i3 := i + 2
		if i3 >= n {
			i3 = n - 1
		}
		p0, p1, p2, p3 := pts[i0], pts[i], pts[i+1], pts[i3]
		cp1x := p1.x + (p2.x-p0.x)/6
		cp1y := p1.y + (p2.y-p0.y)/6
		cp2x := p2.x - (p3.x-p1.x)/6
		cp2y := p2.y - (p3.y-p1.y)/6
		path.WriteString(fmt.Sprintf(" C %.1f,%.1f %.1f,%.1f %.1f,%.1f",
			cp1x, cp1y, cp2x, cp2y, p2.x, p2.y))
	}

	// Min/max labels at the left edge with white background rects so the sparkline
	// doesn't visually bleed through the text.
	const (
		labelX    = 1.0
		labelW    = 36.0
		labelH    = 14.0
		fontSize  = 12.0
	)
	maxRectY  := padY - 1
	maxTextY  := maxRectY + fontSize - 1 // baseline inside rect
	minRectY  := h - labelH
	minTextY  := h - 2
	var labelsSVG string
	if minRectY-maxRectY < labelH+2 {
		// Too close — merge into a single mid label.
		midRectY := (maxRectY + minRectY) / 2
		midTextY := midRectY + fontSize - 1
		labelsSVG = fmt.Sprintf(
			`<rect x="%.0f" y="%.0f" width="%.0f" height="%.0f" fill="white"/>`+
				`<text x="%.0f" y="%.0f" font-size="%.0f" font-family="DejaVu Sans,Arial,sans-serif" text-anchor="start" fill="#000">%.0f–%.0f°</text>`,
			labelX, midRectY, labelW, labelH,
			labelX+2, midTextY, fontSize, minT, maxT,
		)
	} else {
		labelsSVG = fmt.Sprintf(
			`<rect x="%.0f" y="%.0f" width="%.0f" height="%.0f" fill="white"/>`+
				`<text x="%.0f" y="%.0f" font-size="%.0f" font-family="DejaVu Sans,Arial,sans-serif" text-anchor="start" fill="#000">%.0f°</text>`+
				`<rect x="%.0f" y="%.0f" width="%.0f" height="%.0f" fill="white"/>`+
				`<text x="%.0f" y="%.0f" font-size="%.0f" font-family="DejaVu Sans,Arial,sans-serif" text-anchor="start" fill="#000">%.0f°</text>`,
			labelX, maxRectY, labelW, labelH,
			labelX+2, maxTextY, fontSize, maxT,
			labelX, minRectY, labelW, labelH,
			labelX+2, minTextY, fontSize, minT,
		)
	}

	return template.HTML(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="100%%" height="%.0f" viewBox="0 0 %.0f %.0f">`+
			`<path d="%s" fill="none" stroke="#000" stroke-width="1.5" stroke-linejoin="round" stroke-linecap="round"/>`+
			`%s`+
			`</svg>`,
		h, svgW, h,
		path.String(),
		labelsSVG,
	))
}

// buildUVTextSVG returns a row of integer UV index values (0–11) aligned to the
// shared 8-slot x-axis. No bars — just numbers, easier to read at small sizes.
func buildUVTextSVG(hourly []weather.HourlyPoint) template.HTML {
	if len(hourly) == 0 {
		return ""
	}
	const h = 16.0

	// Text baseline: vertically center the numbers in the SVG.
	// Use dominant-baseline so the glyph centre sits at h/2.
	const textY = h / 2

	n := len(hourly)
	var sb strings.Builder
	// Rotated "UV" label anchored to the left edge of this row.
	sb.WriteString(fmt.Sprintf(
		`<text x="4" y="%.1f" font-size="8" font-family="DejaVu Sans,Arial,sans-serif" text-anchor="middle" dominant-baseline="middle" transform="rotate(-90,4,%.1f)">UV</text>`,
		textY, textY,
	))
	for i, p := range hourly {
		cx := slotCX(i, n)
		uv := int(p.UVIndex + 0.5)
		if uv < 0 {
			uv = 0
		}
		if uv > 11 {
			uv = 11
		}
		sb.WriteString(fmt.Sprintf(
			`<text x="%.1f" y="%.0f" font-size="11" font-family="DejaVu Sans,Arial,sans-serif" text-anchor="middle" dominant-baseline="middle">%d</text>`,
			cx, textY, uv,
		))
	}

	return template.HTML(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="100%%" height="%.0f" viewBox="0 0 %.0f %.0f">%s</svg>`,
		h, svgW, h, sb.String(),
	))
}

// buildConditionIconsSVG returns a row of small weather condition icons keyed to
// the hourly WMO weather code. Neutral conditions (overcast, fog) show nothing.
// Time labels appear at the bottom — this is the lowest chart in the shared axis stack.
func buildConditionIconsSVG(hourly []weather.HourlyPoint) template.HTML {
	if len(hourly) == 0 {
		return ""
	}
	const iconSize = 20.0
	const labelH = 14.0 // extra space creates a gap between icon bottom and time label
	const h = iconSize + labelH

	n := len(hourly)
	slot := svgW / float64(n)

	var sb strings.Builder
	for i, p := range hourly {
		cx := slotCX(i, n)

		// Time label at the bottom.
		sb.WriteString(fmt.Sprintf(
			`<text x="%.1f" y="%.1f" font-size="11" font-family="DejaVu Sans,Arial,sans-serif" text-anchor="middle">%s</text>`,
			cx, h-1, p.ShortLabel,
		))

		// Condition icon centred in the icon area.
		if id := precipIconID(p.WeatherCode); id != "" {
			iconX := cx - iconSize/2
			// Clamp to slot boundaries so edge icons don't overflow.
			if iconX < 0 {
				iconX = 0
			}
			if iconX+iconSize > slot*float64(n) {
				iconX = slot*float64(n) - iconSize
			}
			sb.WriteString(fmt.Sprintf(
				`<use href="#%s" x="%.1f" y="0" width="%.0f" height="%.0f"/>`,
				id, iconX, iconSize, iconSize,
			))
		}
	}

	return template.HTML(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="100%%" height="%.0f" viewBox="0 0 %.0f %.0f">%s</svg>`,
		h, svgW, h, sb.String(),
	))
}
