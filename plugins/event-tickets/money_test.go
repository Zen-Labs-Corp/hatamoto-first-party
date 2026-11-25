package main

import "testing"

func TestCurrencyExp(t *testing.T) {
	for cur, want := range map[string]int{"USD": 2, "EUR": 2, "jpy": 0, "KRW": 0, "XPF": 0, "KWD": 3, "TND": 3} {
		if got := currencyExp(cur); got != want {
			t.Errorf("currencyExp(%s) = %d, want %d", cur, got, want)
		}
	}
	if !paymentsCurrencyOK("USD") || !paymentsCurrencyOK("JPY") || paymentsCurrencyOK("KWD") || paymentsCurrencyOK("BHD") {
		t.Error("three-decimal currencies are refused, the others accepted")
	}
}

func TestDecimalAndFormatMoney(t *testing.T) {
	for _, c := range []struct {
		cents    int64
		cur      string
		dec, txt string
	}{
		{1250, "USD", "12.50", "12.50 USD"},
		{5, "EUR", "0.05", "0.05 EUR"},
		{0, "USD", "0.00", "Free"},
		{100, "USD", "1.00", "1.00 USD"},
		{1200, "JPY", "1200", "1200 JPY"},
		{0, "JPY", "0", "Free"},
		{99999999, "usd", "999999.99", "999999.99 USD"},
	} {
		if got := decimalAmount(c.cents, c.cur); got != c.dec {
			t.Errorf("decimalAmount(%d,%s) = %q, want %q", c.cents, c.cur, got, c.dec)
		}
		if got := formatMoney(c.cents, c.cur); got != c.txt {
			t.Errorf("formatMoney(%d,%s) = %q, want %q", c.cents, c.cur, got, c.txt)
		}
	}
}
