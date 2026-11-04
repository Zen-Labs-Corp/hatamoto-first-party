package main

import (
	"encoding/json"
	"mime"
	"net/http"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// Stripe posts the whole event, which can pass 4 KiB; only the id is read.
const maxWebhookBody = 256 << 10

// The trust model: core does not pass the Stripe-Signature header to plugin
// routes, so the posted body is not believed. Only its event id is read, and
// the event itself is fetched from Stripe with the secret key. Anyone can post
// an id; only an event Stripe returns is recorded, and recording is idempotent.
// When core passes the signature header, verify it here as well.

// handleWebhook never returns a Go error: bad input is a 400, a problem on our
// side or at Stripe is a 503 (Stripe retries), everything else is a 200.
func handleWebhook(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	id, ok := eventID(r)
	if !ok {
		return reply(http.StatusBadRequest, map[string]string{"error": "bad request"})
	}
	s, err := loadSettings()
	if err != nil || s.SecretKey == "" {
		return unavailable()
	}
	seen, err := eventSeen(id)
	if err != nil {
		return unavailable()
	}
	if seen {
		return webhookDone("duplicate")
	}
	ev, code, err := fetchEvent(s.SecretKey, id)
	switch {
	case err != nil:
		logf("warn", "webhook: could not confirm the event: "+err.Error())
		return unavailable()
	case code == http.StatusNotFound:
		return webhookDone("ignored")
	case code != http.StatusOK:
		logf("warn", "webhook: stripe answered an unexpected status")
		return unavailable()
	}
	p, ok := recordable(ev, id)
	if !ok {
		return webhookDone("ignored")
	}
	p.Label, p.Kind = resolveLink(s.SecretKey, p.LinkID, s.Links)
	inserted, err := insertPayment(p)
	if err != nil {
		logf("warn", "webhook: could not store the payment")
		return unavailable()
	}
	if !inserted {
		return webhookDone("duplicate")
	}
	if s.notifyOn() && s.NotifyEmail != "" {
		notifyOwner(s, p)
	}
	return webhookDone("recorded")
}

// eventID reads the id of the posted event and nothing else.
func eventID(r sdk.HTTPRequest) (string, bool) {
	if r.BodyB64 != "" || len(r.Body) == 0 || len(r.Body) > maxWebhookBody {
		return "", false
	}
	if mt, _, err := mime.ParseMediaType(headerValue(r.Headers, "content-type")); err != nil || mt != "application/json" {
		return "", false
	}
	var in struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(r.Body), &in) != nil || !reEvent.MatchString(in.ID) {
		return "", false
	}
	return in.ID, true
}

// headerValue looks a header up whatever the case of its name.
func headerValue(h map[string]string, name string) string {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// recordable turns a confirmed event into a payment, or reports that the event
// is not one the plugin records: a paid checkout (or a later async success)
// that came from a payment link.
func recordable(ev stripeEvent, id string) (payment, bool) {
	s := ev.Data.Object
	paid := ev.Type == "checkout.session.async_payment_succeeded" ||
		(ev.Type == "checkout.session.completed" && s.PaymentStatus == "paid")
	if ev.ID != id || !paid || s.ID == "" || !rePlink.MatchString(s.PaymentLink) || len(s.Currency) != 3 {
		return payment{}, false
	}
	created := time.Now()
	if s.Created > 0 {
		created = time.Unix(s.Created, 0)
	}
	return payment{
		EventID: id, SessionID: s.ID, Amount: s.AmountTotal, Currency: strings.ToUpper(s.Currency),
		Email: s.CustomerDetails.Email, LinkID: s.PaymentLink, Live: s.Livemode, Created: created.UTC(),
	}, true
}

// notifyOwner emails the owner about a stored payment. A failure is logged and
// never fails the webhook: the payment is already recorded.
func notifyOwner(s settings, p payment) {
	amount := formatAmount(p.Amount, p.Currency)
	body := strings.Join([]string{
		"Link: " + p.Label,
		"Kind: " + p.Kind,
		"Amount: " + amount,
		"Customer email: " + p.Email,
		"Session: " + p.SessionID,
		"Time (UTC): " + p.Created.Format(time.RFC3339),
	}, "\n") + "\n"
	err := callHost("mail.send", map[string]any{
		"to":      []string{s.NotifyEmail},
		"subject": "New payment: " + oneLine(p.Label) + " " + amount,
		"text":    body,
	}, nil)
	if err != nil {
		logf("warn", "owner email failed: "+hostCode(err))
	}
}

// oneLine keeps a value from adding header lines to the subject.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func webhookDone(state string) (sdk.HTTPResponse, error) {
	return reply(http.StatusOK, map[string]string{"status": state})
}

func unavailable() (sdk.HTTPResponse, error) {
	return reply(http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
}
