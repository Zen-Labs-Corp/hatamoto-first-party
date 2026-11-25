package main

import (
	"strconv"
	"strings"
)

// zeroDecimal are the currencies whose smallest unit is the whole unit.
var zeroDecimal = map[string]bool{
	"BIF": true, "CLP": true, "DJF": true, "GNF": true, "JPY": true, "KMF": true, "KRW": true, "MGA": true,
	"PYG": true, "RWF": true, "UGX": true, "VND": true, "VUV": true, "XAF": true, "XOF": true, "XPF": true,
}

// threeDecimal currencies are not supported for payments.
var threeDecimal = map[string]bool{"BHD": true, "JOD": true, "KWD": true, "OMR": true, "TND": true}

// currencyExp is the number of decimals of a currency's minor unit.
func currencyExp(cur string) int {
	cur = strings.ToUpper(cur)
	switch {
	case zeroDecimal[cur]:
		return 0
	case threeDecimal[cur]:
		return 3
	}
	return 2
}

// paymentsCurrencyOK reports whether paid tickets can be sold in cur.
func paymentsCurrencyOK(cur string) bool { return currencyExp(cur) != 3 }

// decimalAmount writes minor units as a decimal string: "12.50", "1200" for JPY.
func decimalAmount(cents int64, cur string) string {
	exp := currencyExp(cur)
	neg := cents < 0
	if neg {
		cents = -cents
	}
	s := strconv.FormatInt(cents, 10)
	if exp > 0 {
		if len(s) <= exp {
			s = strings.Repeat("0", exp-len(s)+1) + s
		}
		s = s[:len(s)-exp] + "." + s[len(s)-exp:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// formatMoney is the text a price is shown as: "Free", "12.50 USD", "1200 JPY".
func formatMoney(cents int64, cur string) string {
	if cents <= 0 {
		return "Free"
	}
	return decimalAmount(cents, cur) + " " + strings.ToUpper(cur)
}
