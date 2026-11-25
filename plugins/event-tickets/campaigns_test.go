package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// campHost fakes the campaign statements on top of mailHost.
type campHost struct {
	*mailHost
	typeOK   bool
	due      string // ids of due campaigns, a jsonb array
	segment  string // segment of the due campaign
	campaign string // subject/format/body of the sending campaign, "null" = none
	stmts    []call // every campaign write, in order
	stats    string
	statsErr bool   // the campaign list fails
	buys     string // buys per campaign id, a jsonb object
	buysErr  bool   // the buys count fails (a statement timeout on a large site)
}

func newCampHost(t *testing.T) *campHost {
	h := &campHost{mailHost: newMailHost(t), typeOK: true, due: `[]`, segment: `{"event_id":"e1"}`, stats: `[]`, buys: `{}`,
		campaign: `{"subject":"News","format":"text","body":"Hello https://example.org/events/gig and https://other.example/x"}`}
	h.settings = `{"site_url":"https://example.org"}`
	inner := callHost
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		switch {
		case fn == "permissions":
			return errors.New("no answer")
		case strings.Contains(sql, "FROM ticket_types WHERE id = $1 AND event_id = $2"):
			return jsonInto(`{"rows":[[`+map[bool]string{true: "true", false: "false"}[h.typeOK]+`]]}`, out)
		case strings.Contains(sql, "count(DISTINCT lower(o.email))"):
			h.stmts = append(h.stmts, call{fn, sql, ar})
			return jsonInto(`{"rows":[[42]]}`, out)
		case strings.HasPrefix(sql, "WITH c AS (INSERT INTO campaigns"):
			h.stmts = append(h.stmts, call{fn, sql, ar})
			return jsonInto(`{"rows":[[9]]}`, out)
		case strings.HasPrefix(sql, "WITH c AS (UPDATE campaigns SET status = 'cancelled'"):
			h.stmts = append(h.stmts, call{fn, sql, ar})
			return jsonInto(`{"rows":[[true]]}`, out)
		case strings.Contains(sql, "FROM campaigns WHERE status = 'scheduled' AND send_at <= now()"):
			return jsonInto(`{"rows":[[`+h.due+`]]}`, out)
		case strings.Contains(sql, "SELECT segment FROM campaigns"):
			return jsonInto(`{"rows":[[`+h.segment+`]]}`, out)
		case strings.HasPrefix(sql, "WITH c AS (UPDATE campaigns SET status = 'sending'"), strings.HasPrefix(sql, "UPDATE campaigns"):
			h.stmts = append(h.stmts, call{fn, sql, ar})
			return nil
		case strings.Contains(sql, "FROM campaigns WHERE id = $1 AND status = 'sending'"):
			return jsonInto(`{"rows":[[`+h.campaign+`]]}`, out)
		case strings.Contains(sql, "count(DISTINCT m.id)"):
			if h.buysErr {
				return errors.New("statement timeout")
			}
			return jsonInto(`{"rows":[[`+h.buys+`]]}`, out)
		case strings.Contains(sql, "FROM (SELECT c.id, c.subject"):
			if h.statsErr {
				return errors.New("statement timeout")
			}
			return jsonInto(`{"rows":[[`+h.stats+`]]}`, out)
		case strings.Contains(sql, "'email.%'"):
			return jsonInto(`{"rows":[[{}]]}`, out)
		case strings.Contains(sql, "FROM event_changes"):
			return jsonInto(`{"rows":[[[]]]}`, out)
		}
		return inner(fn, args, out)
	}
	return h
}

func ownerPost(user string, body any) sdk.HTTPRequest {
	b, _ := json.Marshal(body)
	r := sdk.HTTPRequest{Method: "POST", Body: string(b)}
	if user != "" {
		r.User = &sdk.HTTPUser{Role: user}
	}
	return r
}

func errOf(t *testing.T, r sdk.HTTPResponse, status int) string {
	t.Helper()
	if r.Status != status {
		t.Fatalf("status %d, want %d: %s", r.Status, status, r.Body)
	}
	var v struct{ Error string }
	_ = json.Unmarshal([]byte(r.Body), &v)
	return v.Error
}

func TestCampaignCountUsesTheSegmentCondition(t *testing.T) {
	h := newCampHost(t)
	g := segment{EventID: "e1", TypeID: 5}
	r := serveCampaignCount(ownerPost("owner", map[string]any{"segment": g}))
	if r.Status != 200 || !strings.Contains(r.Body, `"count":42`) {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	cond, _ := g.where(1)
	if len(h.stmts) != 1 || !strings.Contains(h.stmts[0].sql, cond) {
		t.Fatal("the count does not use where()")
	}
	// the fan-out uses the same condition (ISC-5), shifted by the campaign id
	cond2, _ := g.where(2)
	if !strings.Contains(campaignFanoutSQL(cond2), cond2) || !strings.Contains(cond2, "NOT EXISTS (SELECT 1 FROM unsubscribes") {
		t.Error("the fan-out lacks the segment condition or the unsubscribe exclusion")
	}
	errOf(t, serveCampaignCount(ownerPost("editor", map[string]any{"segment": g})), 403)
	errOf(t, serveCampaignCount(ownerPost("owner", map[string]any{"segment": segment{TypeID: 5}})), 400)
	h.typeOK = false
	errOf(t, serveCampaignCount(ownerPost("owner", map[string]any{"segment": g})), 400)
}

func TestCreateCampaign(t *testing.T) {
	h := newCampHost(t)
	base := func() map[string]any {
		return map[string]any{"subject": "News", "format": "text", "segment": map[string]any{"event_id": "e1"},
			"body": "See https://example.org/events/gig?x=1 and https://other.example/a"}
	}
	r := serveCreateCampaign(ownerPost("owner", base()))
	if r.Status != 200 || !strings.Contains(r.Body, `"id":9`) {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if len(h.stmts) != 1 {
		t.Fatalf("%d statements", len(h.stmts))
	}
	var links []linkRow
	if err := json.Unmarshal([]byte(h.stmts[0].args[6].(string)), &links); err != nil || len(links) != 2 {
		t.Fatalf("links %v %v", links, err)
	}
	if links[0].Path == nil || *links[0].Path != "/events/gig?x=1" {
		t.Errorf("same-site link path = %v", links[0].Path)
	}
	if links[1].Path != nil || links[1].URL != "https://other.example/a" {
		t.Errorf("external link = %+v", links[1])
	}
	for _, s := range []string{"campaign_links", "jsonb_to_recordset"} {
		if !strings.Contains(h.stmts[0].sql, s) {
			t.Errorf("statement lacks %q", s)
		}
	}
	if strings.Contains(h.stmts[0].sql, "ana@") {
		t.Error("no address belongs in campaigns")
	}

	many := base()
	var b strings.Builder
	for i := 0; i < 21; i++ {
		b.WriteString("https://other.example/" + string(rune('a'+i)) + " ")
	}
	many["body"] = b.String()
	js := base()
	js["format"], js["body"] = "html", `<a href="javascript:alert(1)">x</a>`
	past := base()
	past["send_at"] = now().Add(-time.Hour).UTC().Format(time.RFC3339)
	far := base()
	far["send_at"] = now().AddDate(0, 0, 400).UTC().Format(time.RFC3339)
	nb := base()
	nb["segment"] = map[string]any{"event_id": "e1", "type_id": 5, "not_bought": true}
	empty := base()
	empty["subject"] = "  "
	for name, req := range map[string]map[string]any{"21 links": many, "javascript link": js, "past": past, "far": far, "not bought + type": nb, "no subject": empty} {
		before := len(h.stmts)
		errOf(t, serveCreateCampaign(ownerPost("owner", req)), 400)
		if len(h.stmts) != before {
			t.Errorf("%s: something was written", name)
		}
	}
	soon := base()
	soon["send_at"] = now().Add(time.Hour).UTC().Format(time.RFC3339)
	if r := serveCreateCampaign(ownerPost("owner", soon)); r.Status != 200 {
		t.Errorf("scheduled: %d %s", r.Status, r.Body)
	}
	errOf(t, serveCreateCampaign(ownerPost("editor", base())), 403)
	errOf(t, serveCreateCampaign(ownerPost("", base())), 403)

	h.settings = `{}`
	if msg := errOf(t, serveCreateCampaign(ownerPost("owner", base())), 400); !strings.Contains(msg, "Site URL") {
		t.Errorf("no origin: %q", msg)
	}
}

func TestCancelCampaign(t *testing.T) {
	h := newCampHost(t)
	if r := serveCancelCampaign(&sdk.HTTPUser{Role: "owner"}, "9"); r.Status != http.StatusOK {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	sql := h.stmts[0].sql
	for _, want := range []string{"status IN ('scheduled', 'sending')", "UPDATE email_sends SET status = 'skipped'", "status = 'queued'"} {
		if !strings.Contains(sql, want) {
			t.Errorf("statement lacks %q", want)
		}
	}
	errOf(t, serveCancelCampaign(&sdk.HTTPUser{Role: "editor"}, "9"), 403)
	errOf(t, serveCancelCampaign(&sdk.HTTPUser{Role: "owner"}, "x"), 400)
}

func TestFanoutCampaignsOnce(t *testing.T) {
	h := newCampHost(t)
	h.due = `[9]`
	fanoutCampaigns(defaultAutomations(), never)
	if len(h.stmts) != 1 {
		t.Fatalf("%d statements", len(h.stmts))
	}
	c := h.stmts[0]
	cond, args := segment{EventID: "e1"}.where(2)
	for _, want := range []string{"UPDATE campaigns SET status = 'sending' WHERE id = $1 AND status = 'scheduled'", "DISTINCT ON (lower(o.email))", "ON CONFLICT DO NOTHING", cond} {
		if !strings.Contains(c.sql, want) {
			t.Errorf("statement lacks %q", want)
		}
	}
	if jsonText(c.args) != jsonText(append([]any{int64(9)}, args...)) {
		t.Errorf("args %s", jsonText(c.args))
	}
	// an invalid stored segment is cancelled, never fanned out
	h = newCampHost(t)
	h.due, h.segment = `[9]`, `{"type_id":5}`
	fanoutCampaigns(defaultAutomations(), never)
	if len(h.stmts) != 1 || !strings.Contains(h.stmts[0].sql, "'cancelled'") {
		t.Errorf("%+v", h.stmts)
	}
}

func TestComposeCampaign(t *testing.T) {
	newCampHost(t)
	key, err := hmacSecret()
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Repeat("cd", 16)
	m, ok, err := composeCampaign(sendRow{Kind: "campaign", RefID: 9, OrderID: 7, EventID: "e1", Code: code})
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if m.To != "ana@example.com" || m.Subject != "News" {
		t.Errorf("%q %q", m.To, m.Subject)
	}
	unsub := regexp.MustCompile(`https://example\.org/p/event-tickets/u/([0-9a-f]{32}\.[0-9a-f]{32})`)
	for name, body := range map[string]string{"html": m.HTML, "text": m.Text} {
		g := unsub.FindStringSubmatch(body)
		if g == nil {
			t.Fatalf("%s lacks the unsubscribe link", name)
		}
		if c, ok := verifyToken(key, kindUnsub, g[1]); !ok || c != code {
			t.Errorf("%s: unsubscribe token does not verify", name)
		}
		// the visible text of a link may be its address; no href or text-mail link goes to a target
		if strings.Contains(body, `href="https://other.example`) || strings.Contains(body, `href="https://example.org/events`) ||
			(name == "text" && (strings.Contains(body, "https://other.example") || strings.Contains(body, "events/gig"))) {
			t.Errorf("%s: a target address is in the mail: %s", name, body)
		}
		clicks := regexp.MustCompile(`https://example\.org/p/event-tickets/c/([0-9a-f]{32}\.[0-9a-f]{32})\?l=([0-9]+)`).FindAllStringSubmatch(body, -1)
		if len(clicks) != 2 {
			t.Fatalf("%s: %d tracked links: %s", name, len(clicks), body)
		}
		for i, c := range clicks {
			if got, ok := verifyToken(key, kindClick(i), c[1]); !ok || got != code || c[2] != string(rune('0'+i)) {
				t.Errorf("%s: link %d token does not verify as click.%d", name, i, i)
			}
		}
	}
	px := regexp.MustCompile(`<img src="https://example\.org/p/event-tickets/o/([0-9a-f]{32}\.[0-9a-f]{32})\.gif" width="1" height="1" alt="">`).FindStringSubmatch(m.HTML)
	if px == nil {
		t.Fatalf("html lacks the pixel: %s", m.HTML)
	}
	if c, ok := verifyToken(key, kindOpen, px[1]); !ok || c != code {
		t.Error("pixel token does not verify")
	}
	if strings.Contains(m.Text, "/o/") || strings.Contains(m.Text, "<img") {
		t.Errorf("text has a pixel: %s", m.Text)
	}
}

func TestComposeCampaignSkips(t *testing.T) {
	h := newCampHost(t)
	send := sendRow{Kind: "campaign", RefID: 9, OrderID: 7, EventID: "e1", Code: strings.Repeat("cd", 16)}
	h.campaign = `null` // cancelled or finished
	if _, ok, err := composeCampaign(send); ok || err != nil {
		t.Errorf("cancelled: ok %v err %v", ok, err)
	}
	h.campaign = `{"subject":"News","format":"text","body":"Hi"}`
	h.settings = `{}`
	if _, ok, _ := composeCampaign(send); ok {
		t.Error("no site origin: want skipped")
	}
}

func TestEmailsListsCampaigns(t *testing.T) {
	h := newCampHost(t)
	h.stats = `[{"id":9,"subject":"News","segment":{"event_id":"e1"},"status":"sending","total":10,"sent":4,"opens":2,"clicks":1},
{"id":8,"subject":"Old","segment":{},"status":"sent","total":3,"sent":3,"opens":0,"clicks":0}]`
	h.buys = `{"9":1}`
	r := serveEmails(&sdk.HTTPUser{Role: "owner"})
	var got struct {
		Campaigns []map[string]any `json:"campaigns"`
	}
	if err := json.Unmarshal([]byte(r.Body), &got); err != nil || len(got.Campaigns) != 2 {
		t.Fatalf("%s %v", r.Body, err)
	}
	for _, k := range []string{"subject", "segment", "status", "total", "sent", "opens", "clicks", "buys"} {
		if _, ok := got.Campaigns[0][k]; !ok {
			t.Errorf("campaign lacks %q", k)
		}
	}
	if got.Campaigns[0]["buys"] != float64(1) || got.Campaigns[1]["buys"] != float64(0) {
		t.Errorf("buys %v %v, want 1 and 0", got.Campaigns[0]["buys"], got.Campaigns[1]["buys"])
	}
	for _, want := range []string{"s.clicked_at IS NOT NULL", "o.created_at >= s.clicked_at", "s.clicked_at + interval '7 days'", "o.status IN ('confirmed', 'paid')",
		"(c.event_id = '' OR o.event_id = c.event_id)", "count(DISTINCT m.id)", "LIMIT 50", "CROSS JOIN LATERAL"} {
		if !strings.Contains(campaignBuysSQL, want) {
			t.Errorf("buys SQL lacks %q", want)
		}
	}
	if strings.Contains(campaignStatsSQL, "orders") || !strings.Contains(campaignStatsSQL, "LIMIT 50") {
		t.Error("the list statement must not touch orders")
	}
}

func TestEmailsKeepsTheListWhenTheBuysCountFails(t *testing.T) {
	h := newCampHost(t)
	h.stats = `[{"id":9,"subject":"News","segment":{},"status":"sending","total":10,"sent":4,"opens":2,"clicks":1}]`
	h.buysErr = true
	r := serveEmails(&sdk.HTTPUser{Role: "owner"})
	var got struct {
		Campaigns []map[string]any `json:"campaigns"`
	}
	if err := json.Unmarshal([]byte(r.Body), &got); err != nil || r.Status != http.StatusOK || len(got.Campaigns) != 1 {
		t.Fatalf("%d %s %v: the campaign must still be listed", r.Status, r.Body, err)
	}
	if v, ok := got.Campaigns[0]["buys"]; !ok || v != nil {
		t.Errorf("buys %v %v, want null (unavailable)", v, ok)
	}
	if got.Campaigns[0]["status"] != "sending" {
		t.Errorf("status %v", got.Campaigns[0]["status"])
	}

	// a failing list is an error, never an empty list that reads "No campaigns yet"
	h.statsErr = true
	if r := serveEmails(&sdk.HTTPUser{Role: "owner"}); r.Status != http.StatusServiceUnavailable {
		t.Errorf("list failure: %d %s, want 503", r.Status, r.Body)
	}
}
