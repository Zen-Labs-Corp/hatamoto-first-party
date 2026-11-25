package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() { guest.OnHTTP(serve) }

// routeBudget is how much of core's 10 s route limit the plugin plans to use;
// routeEnd is that point for the request being served (the zero value, as in
// tests that skip serve, means no overall limit).
const routeBudget = 9 * time.Second

var routeEnd deadline

// serve answers the routes. It never returns a Go error and never puts host
// or database error text in a body.
func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	routeEnd = newDeadline(routeBudget)
	// Core mounts GET routes, which also match HEAD; it drops the body of a
	// HEAD answer itself.
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		switch r.Pattern {
		case "/admin/events":
			return serveEvents(r.User, r.Query), nil
		case "/admin/events/{id}":
			return serveEventDetail(r.User, r.PathValues["id"]), nil
		case "/admin/events/{id}/attendees":
			return serveAttendees(r.User, r.PathValues["id"], r.Query), nil
		case "/admin/events/{id}/attendees.csv":
			return serveAttendeesCSV(r.User, r.PathValues["id"]), nil
		case "/admin/events/{id}/checkin":
			return serveCheckinState(r.User, r.PathValues["id"], r.Query), nil
		case "/admin/settings":
			return serveSettings(r.User), nil
		case "/admin/payouts":
			return servePayouts(r.User, r.Query), nil
		case "/admin/payouts.csv":
			return servePayoutsCSV(r.User, r.Query), nil
		case "/admin/orders":
			return serveOrders(r.User, r.Query), nil
		case "/admin/coupons":
			return serveCoupons(r.User), nil
		case "/admin/emails":
			return serveEmails(r.User), nil
		case "/admin/maps":
			return serveMaps(r.User), nil
		case "/admin/maps/{id}":
			return serveMap(r.User, r.PathValues["id"]), nil
		case "/token":
			return serveToken(r.Query), nil
		case "/waitlist/{token}/claim":
			return serveClaim(r.PathValues["token"], r.Query), nil
		case "/waitlist/{token}/leave":
			return serveLeave(r.PathValues["token"], r.Query), nil
		case "/return/{token}":
			return servePayReturn(r.PathValues["token"], r.Query), nil
		case "/cancel/{token}":
			return servePayCancel(r.PathValues["token"]), nil
		case "/o/{token}":
			return serveOpen(r.PathValues["token"]), nil
		case "/c/{token}":
			return serveClick(r.PathValues["token"], r.Query), nil
		case "/u/{token}":
			return serveUnsub(r.PathValues["token"], r.Query), nil
		case "/ticket/{token}":
			return serveTicket(r.PathValues["token"]), nil
		case "/ticket/{token}/qr.png":
			return serveTicketQR(r.PathValues["token"]), nil
		}
	}
	if r.Method == http.MethodPost {
		switch r.Pattern {
		case "/rsvp":
			return serveRSVP(r), nil
		case "/quote":
			return serveQuote(r), nil
		case "/checkout":
			return serveCheckout(r), nil
		case "/stripe/webhook":
			return serveStripeWebhook(r), nil
		case "/paypal/webhook":
			return servePayPalWebhook(r), nil
		case "/admin/events/{id}/types":
			return serveSaveType(r, r.PathValues["id"]), nil
		case "/admin/types/{id}/delete":
			return serveDeleteType(r.User, r.PathValues["id"]), nil
		case "/admin/attendees/{id}/resend":
			return serveResend(r.User, r.PathValues["id"]), nil
		case "/admin/attendees/{id}/cancel":
			return serveCancel(r.User, r.PathValues["id"]), nil
		case "/admin/events/{id}/checkin":
			return serveCheckinScan(r, r.PathValues["id"]), nil
		case "/admin/attendees/{id}/uncheck":
			return serveUncheck(r.User, r.PathValues["id"]), nil
		case "/admin/events/{id}/fields":
			return serveSaveFields(r, r.PathValues["id"]), nil
		case "/admin/events/{id}/bundles":
			return serveSaveBundles(r, r.PathValues["id"]), nil
		case "/admin/events/{id}/splits":
			return serveSaveSplits(r, r.PathValues["id"]), nil
		case "/admin/payouts/mark":
			return serveMarkPaid(r), nil
		case "/admin/payouts/unmark":
			return serveUnmarkPaid(r), nil
		case "/admin/orders/{id}/refund":
			return serveRefund(r, r.PathValues["id"]), nil
		case "/admin/orders/{id}/cancel":
			return serveCancelOrder(r.User, r.PathValues["id"]), nil
		case "/admin/coupons":
			return serveSaveCoupon(r), nil
		case "/admin/coupons/{id}/delete":
			return serveDeleteCoupon(r.User, r.PathValues["id"]), nil
		case "/admin/emails/automations":
			return serveSaveAutomations(r), nil
		case "/admin/campaigns/count":
			return serveCampaignCount(r), nil
		case "/admin/campaigns":
			return serveCreateCampaign(r), nil
		case "/admin/campaigns/{id}/cancel":
			return serveCancelCampaign(r.User, r.PathValues["id"]), nil
		case "/admin/maps":
			return serveSaveMap(r), nil
		case "/admin/maps/{id}/delete":
			return serveDeleteMap(r.User, r.PathValues["id"]), nil
		case "/admin/maps/{id}/sections":
			return serveSaveSection(r, r.PathValues["id"]), nil
		case "/admin/sections/{id}/delete":
			return serveDeleteSection(r.User, r.PathValues["id"]), nil
		case "/admin/sections/{id}/seats":
			return serveSetBlocked(r, r.PathValues["id"]), nil
		}
	}
	return plain(http.StatusNotFound, "not found"), nil
}

func plain(code int, body string) sdk.HTTPResponse {
	return sdk.HTTPResponse{
		Status:  code,
		Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
		Body:    body,
	}
}

func jsonReply(code int, v any) sdk.HTTPResponse {
	b, _ := json.Marshal(v)
	return sdk.HTTPResponse{
		Status:  code,
		Headers: map[string]string{"Content-Type": "application/json; charset=utf-8", "Cache-Control": "no-store"},
		Body:    string(b),
	}
}

// failReply is the body of every JSON error.
func failReply(code int, msg string) sdk.HTTPResponse {
	return jsonReply(code, map[string]any{"ok": false, "error": msg})
}

func forbidden() sdk.HTTPResponse   { return failReply(http.StatusForbidden, "not allowed") }
func unavailable() sdk.HTTPResponse { return failReply(http.StatusServiceUnavailable, "unavailable") }

const maxBodyBytes = 16 << 10

// decodeBody reads a JSON body of at most 16 KiB into v and refuses unknown
// keys and trailing data. It returns an error message, empty when the body is fine.
func decodeBody(r sdk.HTTPRequest, v any) string {
	if r.BodyB64 != "" || len(r.Body) > maxBodyBytes {
		return "bad request"
	}
	dec := json.NewDecoder(strings.NewReader(r.Body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return "bad request"
	}
	if _, err := dec.Token(); err != io.EOF {
		return "bad request"
	}
	return ""
}

// queryValue is the first value of a query parameter. Core's bridge turns a
// non-string value the page sends into its JSON text, so an array arrives as
// `["x"]`; its first element is taken.
func queryValue(v []string) string {
	s := first(v)
	if strings.HasPrefix(s, "[") {
		var list []string
		if json.Unmarshal([]byte(s), &list) == nil {
			return first(list)
		}
	}
	return s
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return strings.TrimSpace(v[0])
}
