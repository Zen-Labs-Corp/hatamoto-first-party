package main

import (
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// couponAdminHost fakes the host for the coupon and bundle routes.
type couponAdminHost struct {
	save    string // answer of the coupon insert or update
	del     string
	list    string
	bundles string
	calls   []call
}

func newCouponAdminHost(t *testing.T) *couponAdminHost {
	t.Helper()
	h := &couponAdminHost{save: `{"id":5,"exists":false}`, del: `{"deleted":true,"deactivated":false}`, list: `[]`, bundles: `[]`}
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		if fn == sdk.FnDBQuery || fn == sdk.FnDBExec {
			h.calls = append(h.calls, call{fn, sql, ar})
		}
		cell := func(v string) error { return jsonInto(`{"rows":[[`+v+`]]}`, out) }
		switch {
		case fn == sdk.FnSettingsGet:
			return jsonInto(`{}`, out)
		case fn == sdk.FnEntitiesGet:
			return jsonInto(gigEntity, out)
		case strings.HasPrefix(sql, "WITH i AS (INSERT INTO coupons"), strings.HasPrefix(sql, "WITH u AS (UPDATE coupons SET code"):
			return cell(h.save)
		case strings.HasPrefix(sql, "WITH d AS (DELETE FROM coupons"):
			return cell(h.del)
		case strings.Contains(sql, "FROM coupons ORDER BY id DESC"):
			return cell(h.list)
		case strings.HasPrefix(sql, "WITH d AS (DELETE FROM bundle_rules"):
			return nil
		case strings.Contains(sql, "FROM bundle_rules"):
			return cell(h.bundles)
		}
		t.Errorf("unexpected host call %q %q", fn, sql)
		return nil
	}
	return h
}

func (h *couponAdminHost) stmt(prefix string) *call {
	for i, c := range h.calls {
		if strings.HasPrefix(c.sql, prefix) {
			return &h.calls[i]
		}
	}
	return nil
}

func TestSaveCouponInsert(t *testing.T) {
	h := newCouponAdminHost(t)
	code, body := post(t, "/admin/coupons", "", editor, `{"code":" summer10 ","kind":"percent","value":10,"max_uses":100,"expires_at":"2027-06-30"}`)
	if code != 200 || body["id"] != float64(5) {
		t.Fatalf("%d %v", code, body)
	}
	st := h.stmt("WITH i AS (INSERT INTO coupons")
	if st == nil || st.args[0] != "SUMMER10" || st.args[1] != "percent" || !same(st.args[2], 10) || st.args[3] != nil || st.args[4] != nil ||
		!same(st.args[5], 100) || st.args[6] != "2027-06-30T23:59:59Z" || st.args[7] != true {
		t.Fatalf("args %+v", st)
	}
}

func TestSaveCouponUpdateAndConflicts(t *testing.T) {
	h := newCouponAdminHost(t)
	body := `{"id":5,"code":"SUMMER10","kind":"fixed","value":500,"event_id":"e1","type_id":3,"active":false,"expires_at":"2027-06-30T10:00:00+02:00"}`
	if code, _ := post(t, "/admin/coupons", "", editor, body); code != 200 {
		t.Fatalf("update: %d", code)
	}
	st := h.stmt("WITH u AS (UPDATE coupons SET code")
	if st == nil || st.args[3] != "e1" || !same(st.args[4], 3) || st.args[6] != "2027-06-30T08:00:00Z" || st.args[7] != false || !same(st.args[8], 5) {
		t.Fatalf("args %+v", st)
	}
	h.save = `{"id":null,"exists":true}`
	if code, _ := post(t, "/admin/coupons", "", editor, body); code != 409 {
		t.Errorf("taken code on update: %d", code)
	}
	h.save = `{"id":null,"exists":false}`
	if code, _ := post(t, "/admin/coupons", "", editor, body); code != 404 {
		t.Errorf("unknown coupon: %d", code)
	}
	h.save = `{"id":null,"exists":true}`
	if code, _ := post(t, "/admin/coupons", "", editor, `{"code":"SUMMER10","kind":"percent","value":10}`); code != 409 {
		t.Errorf("taken code on insert: %d", code)
	}
	h.save = `{"id":null,"exists":false}`
	if code, _ := post(t, "/admin/coupons", "", editor, `{"code":"SUMMER10","kind":"percent","value":10,"type_id":99}`); code != 400 {
		t.Errorf("type of another event: %d", code)
	}
}

func TestSaveCouponRefusesBadBodies(t *testing.T) {
	newCouponAdminHost(t)
	for name, body := range map[string]string{
		"short code":    `{"code":"AB","kind":"percent","value":10}`,
		"bad char":      `{"code":"A B C","kind":"percent","value":10}`,
		"kind":          `{"code":"SUMMER","kind":"free","value":10}`,
		"percent 101":   `{"code":"SUMMER","kind":"percent","value":101}`,
		"zero":          `{"code":"SUMMER","kind":"fixed","value":0}`,
		"fixed too big": `{"code":"SUMMER","kind":"fixed","value":100000000}`,
		"max uses":      `{"code":"SUMMER","kind":"fixed","value":5,"max_uses":0}`,
		"date":          `{"code":"SUMMER","kind":"fixed","value":5,"expires_at":"June"}`,
		"event":         `{"code":"SUMMER","kind":"fixed","value":5,"event_id":"a b"}`,
		"unknown key":   `{"code":"SUMMER","kind":"fixed","value":5,"used":0}`,
		"negative id":   `{"id":-1,"code":"SUMMER","kind":"fixed","value":5}`,
	} {
		if code, _ := post(t, "/admin/coupons", "", editor, body); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code, _ := post(t, "/admin/coupons", "", nil, `{}`); code != 403 {
		t.Errorf("no user: %d", code)
	}
}

func TestCouponListAndDelete(t *testing.T) {
	h := newCouponAdminHost(t)
	h.list = `[{"id":5,"code":"SUMMER10","used":14,"max_uses":100}]`
	if code, body := get(t, "/admin/coupons", "", editor, nil); code != 200 || len(body["coupons"].([]any)) != 1 {
		t.Fatalf("list: %d %v", code, body)
	}
	if code, body := post(t, "/admin/coupons/{id}/delete", "5", editor, `{}`); code != 200 || body["deleted"] != true {
		t.Errorf("delete: %d %v", code, body)
	}
	h.del = `{"deleted":false,"deactivated":true}`
	if code, body := post(t, "/admin/coupons/{id}/delete", "5", editor, `{}`); code != 200 || body["deleted"] != false {
		t.Errorf("used coupon is switched off: %d %v", code, body)
	}
	h.del = `{"deleted":false,"deactivated":false}`
	if code, _ := post(t, "/admin/coupons/{id}/delete", "5", editor, `{}`); code != 404 {
		t.Errorf("unknown: %d", code)
	}
	if code, _ := post(t, "/admin/coupons/{id}/delete", "x", editor, `{}`); code != 400 {
		t.Errorf("bad id: %d", code)
	}
}

func TestSaveBundles(t *testing.T) {
	h := newCouponAdminHost(t)
	h.bundles = `[{"id":1,"type_ids":[5],"min_qty":4,"kind":"percent","value":15}]`
	code, body := post(t, "/admin/events/{id}/bundles", "e1", editor,
		`{"rules":[{"type_ids":[5],"min_qty":4,"kind":"percent","value":15},{"type_ids":[5,6],"all_types":true,"min_qty":2,"kind":"fixed","value":300}]}`)
	if code != 200 || len(body["bundles"].([]any)) != 1 {
		t.Fatalf("%d %v", code, body)
	}
	st := h.stmt("WITH d AS (DELETE FROM bundle_rules WHERE event_id = $1)")
	if st == nil || st.args[0] != "e1" || !strings.Contains(st.args[1].(string), `"pos":1`) {
		t.Fatalf("statement %+v", st)
	}
	// an empty list clears the rules
	if code, _ := post(t, "/admin/events/{id}/bundles", "e1", editor, `{"rules":[]}`); code != 200 {
		t.Errorf("clear: %d", code)
	}
}

func TestSaveBundlesRefusesBadRules(t *testing.T) {
	h := newCouponAdminHost(t)
	many := strings.Repeat(`{"min_qty":2,"kind":"percent","value":5},`, 10) + `{"min_qty":2,"kind":"percent","value":5}`
	for name, body := range map[string]string{
		"11 rules":         `{"rules":[` + many + `]}`,
		"min qty 0":        `{"rules":[{"min_qty":0,"kind":"percent","value":5}]}`,
		"min qty 11":       `{"rules":[{"min_qty":11,"kind":"percent","value":5}]}`,
		"percent 150":      `{"rules":[{"min_qty":2,"kind":"percent","value":150}]}`,
		"kind":             `{"rules":[{"min_qty":2,"kind":"free","value":5}]}`,
		"combo of nothing": `{"rules":[{"all_types":true,"min_qty":2,"kind":"percent","value":5}]}`,
		"bad type id":      `{"rules":[{"type_ids":[0],"min_qty":2,"kind":"percent","value":5}]}`,
		"unknown key":      `{"rules":[],"x":1}`,
	} {
		if code, _ := post(t, "/admin/events/{id}/bundles", "e1", editor, body); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code, _ := post(t, "/admin/events/{id}/bundles", "a b", editor, `{"rules":[]}`); code != 400 {
		t.Errorf("bad event: %d", code)
	}
	if h.stmt("WITH d AS (DELETE FROM bundle_rules") != nil {
		t.Error("a bad body wrote")
	}
}
