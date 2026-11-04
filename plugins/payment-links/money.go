package main

import (
	"strconv"
	"strings"
)

// Stripe amounts are integers in the currency's smallest unit. Most currencies
// have two decimals; these have none or three.
var (
	zeroDecimal  = map[string]bool{"BIF": true, "CLP": true, "DJF": true, "GNF": true, "JPY": true, "KMF": true, "KRW": true, "MGA": true, "PYG": true, "RWF": true, "UGX": true, "VND": true, "VUV": true, "XAF": true, "XOF": true, "XPF": true}
	threeDecimal = map[string]bool{"BHD": true, "JOD": true, "KWD": true, "OMR": true, "TND": true}
)

// decimalAmount renders minor units as a plain decimal string ("50.00").
func decimalAmount(minor int64, cur string) string {
	places := 2
	switch c := strings.ToUpper(cur); {
	case zeroDecimal[c]:
		places = 0
	case threeDecimal[c]:
		places = 3
	}
	neg := minor < 0
	u := uint64(minor)
	if neg {
		u = -u
	}
	digits := strconv.FormatUint(u, 10)
	if places > 0 {
		for len(digits) <= places {
			digits = "0" + digits
		}
		digits = digits[:len(digits)-places] + "." + digits[len(digits)-places:]
	}
	if neg {
		digits = "-" + digits
	}
	return digits
}

// formatAmount renders minor units for people: "AUD 1,250.00".
func formatAmount(minor int64, cur string) string {
	d := decimalAmount(minor, cur)
	sign := ""
	if strings.HasPrefix(d, "-") {
		sign, d = "-", d[1:]
	}
	whole, frac, hasFrac := strings.Cut(d, ".")
	var grouped []byte
	for i := range len(whole) {
		if i > 0 && (len(whole)-i)%3 == 0 {
			grouped = append(grouped, ',')
		}
		grouped = append(grouped, whole[i])
	}
	out := sign + string(grouped)
	if hasFrac {
		out += "." + frac
	}
	return strings.ToUpper(cur) + " " + out
}
