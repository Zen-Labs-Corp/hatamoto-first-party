package main

import (
	"fmt"
	"net/url"
	"strings"
)

// dayCell is one day of the 90-day bar. Has is false for a day without checks.
type dayCell struct {
	Has bool
	Pct float64
}

const (
	barDays   = 90
	barCellW  = 4
	barStep   = 5
	barHeight = 24

	colorGreen = "#2e7d32" // uptime >= 99.9 %
	colorAmber = "#f9a825" // >= 99 %
	colorRed   = "#c62828" // below
	colorGrey  = "#bdbdbd" // no data
)

func cellColor(c dayCell) string {
	switch {
	case !c.Has:
		return colorGrey
	case c.Pct >= 99.9:
		return colorGreen
	case c.Pct >= 99:
		return colorAmber
	}
	return colorRed
}

// uptimeBar draws the days, oldest first, as an SVG image and returns it as a
// data URI for an <img src>. Colours are attributes: an SVG shown through an
// img is not subject to the page's style rules.
func uptimeBar(days [barDays]dayCell) string {
	var b strings.Builder
	w := barDays*barStep - (barStep - barCellW)
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" preserveAspectRatio="none">`, w, barHeight)
	for i, c := range days {
		fmt.Fprintf(&b, `<rect x="%d" y="0" width="%d" height="%d" rx="1" fill="%s"/>`, i*barStep, barCellW, barHeight, cellColor(c))
	}
	b.WriteString(`</svg>`)
	return "data:image/svg+xml;utf8," + url.PathEscape(b.String())
}
