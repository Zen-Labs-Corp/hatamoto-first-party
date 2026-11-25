package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

var editor = &sdk.HTTPUser{ID: "u1", Role: "editor"}

// adminHost fakes the host for the admin routes and records every call.
type adminHost struct {
	entity     string
	settings   string            // settings.get answer; "" = the default below
	listPages  map[string]string // entities.list answer by schema_slug
	listErr    error
	types      string // jsonb cell of the types query
	fields     string
	stats      string
	exists     bool
	insertRows int
	updateRows int
	deleteRows int
	dbErr      error
	calls      []call
}

func newAdminHost(t *testing.T) *adminHost {
	orig := onSeatsFreed
	onSeatsFreed = func(int64) {} // the waitlist has its own tests
	t.Cleanup(func() { onSeatsFreed = orig })
	h := &adminHost{entity: gigEntity, types: `[]`, fields: `[]`, stats: `[]`, insertRows: 1, updateRows: 1}
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		h.calls = append(h.calls, call{fn, sql, ar})
		rows := func(n int) error { return jsonInto(`{"rows_affected":`+jsonText(n)+`}`, out) }
		cell := func(v string) error { return jsonInto(`{"rows":[[`+v+`]]}`, out) }
		switch {
		case fn == sdk.FnSettingsGet:
			if h.settings != "" {
				return jsonInto(h.settings, out)
			}
			return jsonInto(`{"default_capacity":30,"confirmation_text":"secret door code"}`, out)
		case fn == sdk.FnEntitiesGet:
			return jsonInto(h.entity, out)
		case fn == sdk.FnEntitiesList:
			if h.listErr != nil {
				return h.listErr
			}
			page, ok := h.listPages[a["schema_slug"].(string)]
			if !ok {
				return &sdk.HostError{Code: sdk.ErrNotFound}
			}
			return jsonInto(page, out)
		case h.dbErr != nil:
			return h.dbErr
		case strings.HasPrefix(sql, "INSERT INTO ticket_types"):
			return rows(h.insertRows)
		case strings.HasPrefix(sql, "UPDATE ticket_types SET name"):
			return rows(h.updateRows)
		case strings.HasPrefix(sql, "DELETE FROM ticket_types"):
			return rows(h.deleteRows)
		case strings.HasPrefix(sql, "UPDATE ticket_types SET on_sale"):
			return rows(1)
		case strings.HasPrefix(sql, "INSERT INTO events"), strings.HasPrefix(sql, "WITH d AS"):
			return nil
		case strings.Contains(sql, "EXISTS"):
			return cell(jsonText(h.exists))
		case strings.Contains(sql, "FROM ticket_types WHERE event_id"):
			return cell(jsonText(h.types))
		case strings.Contains(sql, "FROM bundle_rules"):
			return cell(`[]`)
		case strings.Contains(sql, "FROM fields"):
			return cell(jsonText(h.fields))
		case strings.Contains(sql, "FROM ticket_types tt"):
			return cell(jsonText(h.stats))
		}
		t.Errorf("unexpected host call %q %q", fn, sql)
		return nil
	}
	return h
}

func (h *adminHost) sawSQL(prefix string) *call {
	for i, c := range h.calls {
		if strings.HasPrefix(c.sql, prefix) {
			return &h.calls[i]
		}
	}
	return nil
}

func request(t *testing.T, r sdk.HTTPRequest) (int, map[string]any) {
	t.Helper()
	in, _ := json.Marshal(r)
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}
	var resp sdk.HTTPResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(resp.Body), &body)
	return resp.Status, body
}

func post(t *testing.T, pattern, id string, user *sdk.HTTPUser, body string) (int, map[string]any) {
	return request(t, sdk.HTTPRequest{Method: "POST", Pattern: pattern, PathValues: map[string]string{"id": id}, User: user, Body: body})
}

func get(t *testing.T, pattern, id string, user *sdk.HTTPUser, query map[string][]string) (int, map[string]any) {
	return request(t, sdk.HTTPRequest{Method: "GET", Pattern: pattern, PathValues: map[string]string{"id": id}, User: user, Query: query})
}

func TestAdminRoutesRefuseNoUser(t *testing.T) {
	newAdminHost(t)
	for _, c := range []struct{ method, pattern string }{
		{"GET", "/admin/events"}, {"GET", "/admin/events/{id}"}, {"GET", "/admin/settings"},
		{"POST", "/admin/events/{id}/types"}, {"POST", "/admin/types/{id}/delete"}, {"POST", "/admin/events/{id}/fields"},
	} {
		code, _ := request(t, sdk.HTTPRequest{Method: c.method, Pattern: c.pattern, PathValues: map[string]string{"id": "e1"}, Body: `{}`})
		if code != 403 {
			t.Errorf("%s %s without a user: %d", c.method, c.pattern, code)
		}
	}
}

func TestAdminBadIDs(t *testing.T) {
	h := newAdminHost(t)
	for _, id := range []string{"a b", "a/b", strings.Repeat("x", 65), "é"} {
		if code, _ := get(t, "/admin/events/{id}", id, editor, nil); code != 400 {
			t.Errorf("event %q: %d", id, code)
		}
		if code, _ := post(t, "/admin/events/{id}/types", id, editor, `{"name":"x"}`); code != 400 {
			t.Errorf("types %q: %d", id, code)
		}
	}
	for _, id := range []string{"abc", "-1", "1 2", strings.Repeat("1", 19)} {
		if code, _ := post(t, "/admin/types/{id}/delete", id, editor, ``); code != 400 {
			t.Errorf("delete %q: %d", id, code)
		}
	}
	if h.sawSQL("DELETE") != nil || h.sawSQL("INSERT") != nil {
		t.Error("a bad id must not reach the database")
	}
}

func TestEventDetail(t *testing.T) {
	h := newAdminHost(t)
	h.types = `[{"id":7,"event_id":"e1","name":"Standard","capacity":30,"taken":2,"waitlist":true,"on_sale":true,"position":0}]`
	code, body := get(t, "/admin/events/{id}", "e1", editor, nil)
	if code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	if body["default_capacity"] != float64(30) || len(body["types"].([]any)) != 1 || body["event"].(map[string]any)["name"] != "Gig" {
		t.Errorf("%v", body)
	}
	if h.sawSQL("INSERT INTO events") == nil {
		t.Error("reading an event must record its dates")
	}
	if strings.Contains(jsonText(body), "meet.example.org") {
		t.Error("the join link is not part of the admin answer")
	}
	h.entity = strings.Replace(gigEntity, "calendar-event", "page", 1)
	if code, _ := get(t, "/admin/events/{id}", "e1", editor, nil); code != 404 {
		t.Errorf("not an event: %d", code)
	}
	h.dbErr = errors.New("pq: relation missing")
	h.entity = gigEntity
	code, body = get(t, "/admin/events/{id}", "e1", editor, nil)
	if code != 503 || strings.Contains(jsonText(body), "relation") {
		t.Errorf("%d %v", code, body)
	}
}

func TestEventsList(t *testing.T) {
	h := newAdminHost(t)
	h.listPages = map[string]string{
		"calendar-event": `{"items":[{"id":"e1","name":"Gig","url":"/events/gig","attributes":{"start_at":"2027-03-01T20:00"}},{"id":"e2","name":"Talk"}],"next_cursor":"c2"}`,
	}
	h.stats = `[{"event_id":"e1","types":2,"capacity":80,"taken":10,"waiting":3}]`
	code, body := get(t, "/admin/events", "", editor, map[string][]string{"search": {"gi"}})
	if code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	evs := body["events"].([]any)
	e1 := evs[0].(map[string]any)
	if len(evs) != 2 || e1["taken"] != float64(10) || e1["waiting"] != float64(3) || e1["starts_at"] != "2027-03-01T20:00" || evs[1].(map[string]any)["types"] != float64(0) {
		t.Errorf("%v", evs)
	}
	if body["next_cursor"] != "0:c2" {
		t.Errorf("next_cursor %v", body["next_cursor"])
	}
	for _, c := range h.calls {
		if c.fn == sdk.FnEntitiesGet {
			t.Error("the list must not read events one by one")
		}
	}
	// the last page of the first type points at the second type
	h.listPages["calendar-event"] = `{"items":[{"id":"e1","name":"Gig"}],"next_cursor":""}`
	if _, body = get(t, "/admin/events", "", editor, nil); body["next_cursor"] != "1:" {
		t.Errorf("next_cursor %v", body["next_cursor"])
	}
	// and the second page of the second type is the end; the missing first type is skipped
	h.listPages = map[string]string{"event": `{"items":[{"id":"e3","name":"Old"}],"next_cursor":""}`}
	if _, body = get(t, "/admin/events", "", editor, map[string][]string{"cursor": {"1:"}}); body["next_cursor"] != "" || len(body["events"].([]any)) != 1 {
		t.Errorf("%v", body)
	}
	if code, _ := get(t, "/admin/events", "", editor, map[string][]string{"cursor": {"9:x"}}); code != 400 {
		t.Errorf("bad cursor: %d", code)
	}
	h.listErr = errors.New("down")
	if code, _ := get(t, "/admin/events", "", editor, nil); code != 503 {
		t.Errorf("host down: %d", code)
	}
}

func TestSettingsRouteHasNoSecrets(t *testing.T) {
	newAdminHost(t)
	code, body := get(t, "/admin/settings", "", editor, nil)
	if code != 200 || body["confirmation_text_set"] != true || body["default_capacity"] != float64(30) {
		t.Fatalf("%d %v", code, body)
	}
	for _, bad := range []string{"secret door code", "signing", "key"} {
		if strings.Contains(jsonText(body), bad) {
			t.Errorf("settings answer contains %q", bad)
		}
	}
}

// The settings answer says whether a provider is configured and never carries a secret.
func TestSettingsRoutePaymentsAnswer(t *testing.T) {
	h := newAdminHost(t)
	h.settings = `{"currency":"JPY","stripe_secret_key":"sk_live_SECRETKEY123","stripe_webhook_secret":"whsec_SECRETHOOK123","paypal_client_id":"CLIENTID99","paypal_secret":"PAYPALSECRET99",
"paypal_webhook_id":"WEBHOOK123","paypal_sandbox":true,"fee_fixed_cents":30,"fee_percent_bp":250,"fee_per":"ticket","fee_payer":"organizer"}`
	owner := &sdk.HTTPUser{ID: "u0", Role: "owner"}
	code, body := get(t, "/admin/settings", "", owner, nil)
	if code != 200 || body["stripe_configured"] != true || body["paypal_configured"] != true || body["paypal_webhook_set"] != true ||
		body["paypal_sandbox"] != true || body["fee_fixed_cents"] != float64(30) || body["fee_percent_bp"] != float64(250) ||
		body["fee_per"] != "ticket" || body["fee_payer"] != "organizer" || body["currency_exponent"] != float64(0) || body["can_refund"] != true {
		t.Fatalf("%d %v", code, body)
	}
	for _, bad := range []string{"SECRETKEY", "SECRETHOOK", "PAYPALSECRET", "CLIENTID99", "WEBHOOK123"} {
		if strings.Contains(jsonText(body), bad) {
			t.Errorf("settings answer contains %q", bad)
		}
	}
	if _, body = get(t, "/admin/settings", "", editor, nil); body["can_refund"] != false {
		t.Errorf("an editor cannot refund: %v", body["can_refund"])
	}
	h.settings = `{}`
	if _, body = get(t, "/admin/settings", "", owner, nil); body["stripe_configured"] != false || body["paypal_configured"] != false ||
		body["fee_per"] != "order" || body["fee_payer"] != "buyer" || body["currency_exponent"] != float64(2) {
		t.Errorf("defaults: %v", body)
	}
}
