package main

// Category names, as web.dev labels them.
const (
	catGood  = "good"
	catNeeds = "needs-improvement"
	catPoor  = "poor"
)

// thresholds are web.dev's published limits: a value up to and including the
// first is good, above the second is poor. LCP and INP are in milliseconds.
var thresholds = map[string][2]float64{
	"lcp": {2500, 4000},
	"inp": {200, 500},
	"cls": {0.1, 0.25},
}

// category maps a p75 value of metric ("lcp", "inp", "cls") to good,
// needs-improvement or poor. An unknown metric has no category.
func category(metric string, v float64) string {
	t, ok := thresholds[metric]
	if !ok {
		return ""
	}
	switch {
	case v <= t[0]:
		return catGood
	case v > t[1]:
		return catPoor
	}
	return catNeeds
}
