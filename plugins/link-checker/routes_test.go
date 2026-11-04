package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// routesHost serves the store queries from canned documents. rows holds the
// links the list query returns (honouring the limit it was given).
func routesHost(t *testing.T, rows []string, dbErr error) *fakeHost {
	return newFakeHost(t, func(c call, out any) error {
		switch c.Fn {
		case "kv.get":
			return json.Unmarshal([]byte(`{"found":true,"value":"2026-03-01T11:56:00Z"}`), out)
		case "db.query":
			if dbErr != nil {
				return dbErr
			}
			sql := c.sql()
			switch {
			case strings.Contains(sql, "'broken', count"):
				return cell(out, `{"broken":12,"redirected":3,"ok":840,"ignored":2,"unknown":1,"pending":4,"pages":96}`)
			case strings.Contains(sql, "'n', count"):
				return cell(out, `{"n":37}`)
			case strings.Contains(sql, "WHERE l.id = $1"):
				id := int(c.params()[0].(float64))
				if id > len(rows) || id < 1 {
					return cell(out, `[]`)
				}
				return cell(out, `[`+rows[id-1]+`]`)
			case strings.Contains(sql, "l.status = $1"):
				limit := int(c.params()[2].(float64))
				after := int(c.params()[1].(float64))
				var got []string
				for i := after; i < len(rows) && len(got) < limit; i++ {
					got = append(got, rows[i])
				}
				return cell(out, `[`+strings.Join(got, ",")+`]`)
			}
		case "db.exec":
			return dbErr
		}
		return nil
	})
}

func linkJSON(id int, status string) string {
	return fmt.Sprintf(`{"id":%d,"url":"https://example.org/%d","kind":"outbound","status":%q,"http_status":404,"error":null,"last_checked":"2026-03-01T10:00:00Z","first_broken":null,"pages":[{"entity_id":"e1","entity_name":"Home","entity_path":"/"}]}`, id, id, status)
}

func request(t *testing.T, method, pattern string, query map[string][]string, id string) (int, map[string]any) {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: method, Pattern: pattern, Query: query, PathValues: map[string]string{"id": id}})
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatalf("route returned an error: %v", err)
	}
	var res sdk.HTTPResponse
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(res.Body), &body)
	return res.Status, body
}

func TestLinksListsWithCountsAndScanTime(t *testing.T) {
	routesHost(t, []string{linkJSON(1, "broken"), linkJSON(2, "broken")}, nil)
	code, body := request(t, "GET", "/links", map[string][]string{"x": {"1"}}, "")
	if code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	counts := body["counts"].(map[string]any)
	if counts["broken"] != float64(12) || counts["ok"] != float64(840) || body["queue"] != float64(37) ||
		body["pages_total"] != float64(96) || body["last_scan"] != "2026-03-01T11:56:00Z" || body["next_cursor"] != "" {
		t.Fatalf("%v", body)
	}
	links := body["links"].([]any)
	page := links[0].(map[string]any)["pages"].([]any)[0].(map[string]any)
	if len(links) != 2 || page["entity_name"] != "Home" || page["entity_path"] != "/" {
		t.Fatalf("%v", links)
	}
}

func TestLinksStatusFilter(t *testing.T) {
	for query, want := range map[string]string{
		"":           "broken",
		"redirected": "redirected",
		"ok":         "ok",
		"ignored":    "ignored",
		"unknown":    "unknown",
		"pending":    "broken", // not a tab
		"nonsense":   "broken",
		"broken'--":  "broken",
	} {
		f := routesHost(t, nil, nil)
		q := map[string][]string{}
		if query != "" {
			q["status"] = []string{query}
		}
		if code, _ := request(t, "GET", "/links", q, ""); code != 200 {
			t.Fatalf("%q: %d", query, code)
		}
		for _, c := range f.calls {
			if strings.Contains(c.sql(), "l.status = $1") && c.params()[0] != want {
				t.Errorf("%q: filtered by %v, want %s", query, c.params()[0], want)
			}
		}
	}
}

func TestLinksPaging(t *testing.T) {
	var rows []string
	for i := 1; i <= 120; i++ {
		rows = append(rows, linkJSON(i, "broken"))
	}
	routesHost(t, rows, nil)
	var seen []int
	cursor := ""
	for range 5 {
		q := map[string][]string{}
		if cursor != "" {
			q["cursor"] = []string{cursor}
		}
		_, body := request(t, "GET", "/links", q, "")
		for _, l := range body["links"].([]any) {
			seen = append(seen, int(l.(map[string]any)["id"].(float64)))
		}
		cursor, _ = body["next_cursor"].(string)
		if cursor == "" {
			break
		}
	}
	if len(seen) != 120 || seen[0] != 1 || seen[119] != 120 {
		t.Fatalf("saw %d links", len(seen))
	}
	// A page of exactly pageSize has no next cursor.
	routesHost(t, rows[:50], nil)
	if _, body := request(t, "GET", "/links", nil, ""); body["next_cursor"] != "" || len(body["links"].([]any)) != 50 {
		t.Fatalf("%v", body["next_cursor"])
	}
	// A bad cursor starts at the beginning.
	routesHost(t, rows[:3], nil)
	if _, body := request(t, "GET", "/links", map[string][]string{"cursor": {"abc"}}, ""); len(body["links"].([]any)) != 3 {
		t.Fatalf("%v", body)
	}
}

func TestEmptyLinksIsAnEmptyArray(t *testing.T) {
	routesHost(t, nil, nil)
	_, body := request(t, "GET", "/links", nil, "")
	if l, ok := body["links"].([]any); !ok || len(l) != 0 {
		t.Fatalf("links must be [], got %v", body["links"])
	}
}

func TestRecheckAndIgnore(t *testing.T) {
	f := routesHost(t, []string{linkJSON(1, "broken")}, nil)
	for _, route := range []string{"/recheck/{id}", "/ignore/{id}"} {
		if code, body := request(t, "POST", route, nil, "1"); code != 200 || body["id"] != float64(1) {
			t.Errorf("%s: %d %v", route, code, body)
		}
		for _, id := range []string{"0", "-3", "abc", "", "1.5", "99999999999999999999"} {
			if code, _ := request(t, "POST", route, nil, id); code != 400 {
				t.Errorf("%s id %q: %d", route, id, code)
			}
		}
		if code, _ := request(t, "POST", route, nil, "2"); code != 404 {
			t.Errorf("%s missing: %d", route, code)
		}
	}
	var recheck, ignore bool
	for _, q := range f.sqls() {
		recheck = recheck || strings.HasPrefix(q, "UPDATE links SET next_check")
		ignore = ignore || strings.Contains(q, "WHEN status = 'ignored' THEN 'pending'")
	}
	if !recheck || !ignore {
		t.Fatal("both changes must reach the database")
	}
}

func TestRoutesAnswer503WithoutDatabase(t *testing.T) {
	routesHost(t, nil, &sdk.HostError{Code: sdk.ErrCapabilityDenied})
	for _, c := range []struct{ method, pattern string }{{"GET", "/links"}, {"POST", "/recheck/{id}"}, {"POST", "/ignore/{id}"}} {
		code, body := request(t, c.method, c.pattern, nil, "1")
		if code != http.StatusServiceUnavailable || body["error"] == nil {
			t.Errorf("%s %s: %d %v", c.method, c.pattern, code, body)
		}
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	routesHost(t, nil, nil)
	if code, _ := request(t, "GET", "/nope", nil, ""); code != 404 {
		t.Fatal(code)
	}
	if code, _ := request(t, "DELETE", "/links", nil, ""); code != 404 {
		t.Fatal(code)
	}
}
