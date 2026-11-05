package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// maxMapBody is the largest mapping body the route reads.
const maxMapBody = 32 << 10

func init() { guest.OnHTTP(serve) }

// serve answers the owner's admin page. A problem is a JSON answer, never a
// Go error (the host would turn that into a bare 502).
func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "GET /log":
		return logRoute()
	case "POST /test":
		return testRoute()
	case "GET /fields":
		return fieldsRoute()
	case "GET /map":
		return mapGetRoute()
	case "PUT /map":
		return mapPutRoute(r.Body)
	}
	return reply(http.StatusNotFound, map[string]any{"error": "not found"}), nil
}

func reply(code int, v any) sdk.HTTPResponse {
	b, _ := json.Marshal(v)
	return sdk.HTTPResponse{
		Status:  code,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    string(b),
	}
}

// routeBudgeted starts the time budget of a route that fetches and returns the
// function that ends it. Instances are reused, so a budget left by a cron must
// never leak into a route.
func routeBudgeted() func() {
	budgetEnd = now().Add(routeBudget)
	return func() { budgetEnd = time.Time{} }
}

// logRoute returns the settings summary (never a credential) and the last 100
// log rows.
func logRoute() (sdk.HTTPResponse, error) {
	c, err := loadCfg()
	if err != nil {
		return reply(http.StatusServiceUnavailable, map[string]any{"error": "settings unreadable"}), nil
	}
	var rows json.RawMessage
	err = dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb)
FROM (SELECT created_at, form_slug, provider, status, record_id, record_url, code, message FROM log ORDER BY id DESC LIMIT 100) t`, &rows)
	if err != nil {
		return reply(http.StatusServiceUnavailable, map[string]any{"error": "delivery log unreadable"}), nil
	}
	forms := c.Forms
	if forms == nil {
		forms = []string{}
	}
	return reply(http.StatusOK, map[string]any{
		"provider":   c.Provider,
		"object":     c.object(),
		"forms":      forms,
		"configured": c.configured(),
		"rows":       rows,
	}), nil
}

// testRoute checks the saved credentials with the provider.
func testRoute() (sdk.HTTPResponse, error) {
	c, err := loadCfg()
	if err != nil {
		return reply(http.StatusOK, map[string]any{"ok": false, "error": "settings unreadable"}), nil
	}
	p := providerFor(c.Provider)
	if p == nil || !c.configured() {
		return reply(http.StatusOK, map[string]any{"ok": false, "error": "not configured"}), nil
	}
	defer routeBudgeted()()
	account, r := p.test(c, doFetch)
	if r.kind == kindCreated || r.kind == kindUpdated {
		return reply(http.StatusOK, map[string]any{"ok": true, "account": account}), nil
	}
	return reply(http.StatusOK, map[string]any{"ok": false, "error": testError(r)}), nil
}

// testError is a fixed short sentence for a failed call, never provider text.
func testError(r result) string {
	switch {
	case r.status == 401 || r.status == 403:
		return "unauthorized"
	case r.status > 0:
		return fmt.Sprintf("the CRM said %d", r.status)
	}
	switch r.reason {
	case "network", "timeout":
		return "network error"
	case "unauthorized", "blocked", "rejected", "bad answer", "bad instance url", "bad my domain", "bad company domain":
		return r.reason
	}
	return "check failed"
}

type formField struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// formFields lists the fields of the watched forms, unique by name and sorted.
func formFields(c cfg) []formField {
	var res struct {
		Items []struct {
			Slug   string      `json:"slug"`
			Fields []formField `json:"fields"`
		} `json:"items"`
	}
	out := []formField{}
	if callHost("forms.list", struct{}{}, &res) != nil {
		return out
	}
	seen := map[string]bool{}
	for _, it := range res.Items {
		if !c.watched(it.Slug) {
			continue
		}
		for _, f := range it.Fields {
			if f.Name == "" || seen[f.Name] {
				continue
			}
			seen[f.Name] = true
			label := f.Label
			if label == "" {
				label = f.Name
			}
			out = append(out, formField{Name: f.Name, Label: label})
		}
	}
	slices.SortFunc(out, func(a, b formField) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// fieldsRoute lists the CRM fields and the watched forms' fields for the
// mapping table. A CRM error keeps the form fields.
func fieldsRoute() (sdk.HTTPResponse, error) {
	c, err := loadCfg()
	if err != nil {
		return reply(http.StatusOK, map[string]any{"crm_fields": []crmField{}, "form_fields": []formField{}, "error": "settings unreadable"}), nil
	}
	out := map[string]any{"crm_fields": []crmField{}, "form_fields": formFields(c)}
	p := providerFor(c.Provider)
	if p == nil || !c.configured() {
		out["error"] = "not configured"
		return reply(http.StatusOK, out), nil
	}
	defer routeBudgeted()()
	fields, r := p.fields(c, doFetch)
	if r.kind != "" && r.kind != kindCreated && r.kind != kindUpdated {
		out["error"] = testError(r)
	} else if fields != nil {
		out["crm_fields"] = slices.DeleteFunc(slices.Clone(fields), func(f crmField) bool { return isPrivileged(f.Name) })
	}
	return reply(http.StatusOK, out), nil
}

// mapGetRoute returns the saved mapping, or the default.
func mapGetRoute() (sdk.HTTPResponse, error) {
	c, err := loadCfg()
	if err != nil {
		return reply(http.StatusServiceUnavailable, map[string]any{"error": "settings unreadable"}), nil
	}
	rows, saved := []mapRow{}, false
	def := []mapRow{}
	if c.Provider != "" {
		rows, saved = loadMap(c.Provider, c.object())
		def = defaultMap(c.Provider, c.object())
	}
	if rows == nil {
		rows = []mapRow{}
	}
	if def == nil {
		def = []mapRow{}
	}
	return reply(http.StatusOK, map[string]any{"rows": rows, "saved": saved, "default_rows": def}), nil
}

// mapPutRoute validates and saves the mapping; an empty list restores the
// default.
func mapPutRoute(body string) (sdk.HTTPResponse, error) {
	bad := func(msg string) (sdk.HTTPResponse, error) {
		return reply(http.StatusBadRequest, map[string]any{"error": msg}), nil
	}
	if len(body) > maxMapBody {
		return bad("the mapping is too large")
	}
	var in struct {
		Rows []mapRow `json:"rows"`
	}
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || in.Rows == nil {
		return bad("expected {\"rows\": [...]}")
	}
	c, err := loadCfg()
	if err != nil {
		return reply(http.StatusServiceUnavailable, map[string]any{"error": "settings unreadable"}), nil
	}
	if providerFor(c.Provider) == nil {
		return bad("choose a provider first")
	}
	if len(in.Rows) == 0 {
		if callHost("kv.delete", map[string]string{"key": mapKey(c.Provider)}, nil) != nil {
			return reply(http.StatusServiceUnavailable, map[string]any{"error": "mapping not saved"}), nil
		}
		return reply(http.StatusOK, map[string]any{"ok": true, "saved": false}), nil
	}
	if err := validateMap(in.Rows); err != nil {
		return bad(err.Error())
	}
	if saveMap(c.Provider, in.Rows) != nil {
		return reply(http.StatusServiceUnavailable, map[string]any{"error": "mapping not saved"}), nil
	}
	return reply(http.StatusOK, map[string]any{"ok": true, "saved": true}), nil
}
