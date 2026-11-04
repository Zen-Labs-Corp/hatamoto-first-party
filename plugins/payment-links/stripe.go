package main

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"hatamoto.dev/sdk"
)

const (
	stripeAPI       = "https://api.stripe.com"
	stripeTimeoutMS = 8000
)

var (
	reEvent = regexp.MustCompile(`^evt_[A-Za-z0-9]{1,250}$`)
	rePlink = regexp.MustCompile(`^plink_[A-Za-z0-9]{1,250}$`)
)

// Errors are fixed strings: they never carry the key, a header or a response
// body, so they are safe to log.
var (
	errStripeCall     = errors.New("stripe request failed")
	errStripeTooLarge = errors.New("stripe answer too large")
	errStripeBadJSON  = errors.New("stripe answer is not valid JSON")
)

// stripeEvent is the part of a Stripe event the plugin reads.
type stripeEvent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data struct {
		Object session `json:"object"`
	} `json:"data"`
}

// session is the part of a checkout session the plugin reads.
type session struct {
	ID              string `json:"id"`
	PaymentStatus   string `json:"payment_status"`
	Currency        string `json:"currency"`
	PaymentLink     string `json:"payment_link"`
	AmountTotal     int64  `json:"amount_total"`
	Created         int64  `json:"created"`
	Livemode        bool   `json:"livemode"`
	CustomerDetails struct {
		Email string `json:"email"`
	} `json:"customer_details"`
}

// hostCode names a host error by its code only, for log lines.
func hostCode(err error) string {
	var he *sdk.HostError
	if errors.As(err, &he) {
		return he.Code
	}
	return "error"
}

// stripeGet runs GET path against the Stripe API with the secret key. The body
// is decoded into out only for a 200 answer; any other status is returned as
// is for the caller to judge.
func stripeGet(key, path string, out any) (int, error) {
	var res struct {
		Status    int    `json:"status"`
		Body      string `json:"body"`
		Truncated bool   `json:"truncated"`
	}
	err := callHost("http.fetch", map[string]any{
		"method":     "GET",
		"url":        stripeAPI + path,
		"headers":    map[string]string{"Authorization": "Bearer " + key, "Accept": "application/json"},
		"timeout_ms": stripeTimeoutMS,
	}, &res)
	if err != nil {
		return 0, errStripeCall
	}
	if res.Truncated {
		return res.Status, errStripeTooLarge
	}
	if res.Status == 200 && out != nil {
		if json.Unmarshal([]byte(res.Body), out) != nil {
			return res.Status, errStripeBadJSON
		}
	}
	return res.Status, nil
}

// fetchEvent asks Stripe for the event with this id. The id is validated
// before it reaches the URL.
func fetchEvent(key, id string) (stripeEvent, int, error) {
	var ev stripeEvent
	if !reEvent.MatchString(id) {
		return ev, 0, errors.New("invalid event id")
	}
	status, err := stripeGet(key, "/v1/events/"+id, &ev)
	return ev, status, err
}

// normURL reduces a link URL to what identifies it: no query, fragment or
// trailing slash.
func normURL(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	return strings.TrimRight(strings.TrimSpace(u), "/")
}

// resolveLink names a payment link by matching its Stripe URL against the
// links setting. Any failure, or no match, gives the link id and kind payment.
func resolveLink(key, plinkID string, links []link) (label, kind string) {
	label, kind = plinkID, "payment"
	if !rePlink.MatchString(plinkID) {
		return
	}
	var pl struct {
		URL string `json:"url"`
	}
	if status, err := stripeGet(key, "/v1/payment_links/"+plinkID, &pl); err != nil || status != 200 || pl.URL == "" {
		return
	}
	want := normURL(pl.URL)
	for _, l := range links {
		if normURL(l.URL) == want && strings.TrimSpace(l.Label) != "" {
			k := "payment"
			if l.Kind == "donation" {
				k = "donation"
			}
			return strings.TrimSpace(l.Label), k
		}
	}
	return
}
