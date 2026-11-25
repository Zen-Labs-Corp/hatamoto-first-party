package main

import (
	"bytes"
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// Campaigns are mails the owner writes and sends to a segment of people, now or
// later. The fan-out writes one email_sends row per person; the emails job
// sends them within the quota. Campaign mails carry an open pixel and tracked
// links; the target of a link is always looked up in campaign_links.

const (
	maxCampaignSubject = 200
	campaignsPerRun    = 5
	conversionWindow   = "7 days"
)

func init() {
	fanouts = append(fanouts, fanoutCampaigns)
	finishers = append(finishers, finishCampaigns)
	composers["campaign"] = composeCampaign
}

// siteOrigin is the scheme and host campaign links and pixels point at.
func siteOrigin() string { return originOf("", loadSettings().SiteURL) }

type countBody struct {
	Segment segment `json:"segment"`
}

// checkSegment validates a segment, including that its ticket type belongs to
// its event. It returns a message for the owner, empty when it is fine, and
// ok=false when the database failed.
func checkSegment(g segment) (msg string, ok bool) {
	if msg := g.validate(); msg != "" {
		return msg, true
	}
	if g.TypeID != 0 {
		var yes bool
		if err := dbJSON(`SELECT to_jsonb(EXISTS(SELECT 1 FROM ticket_types WHERE id = $1 AND event_id = $2))`, &yes, g.TypeID, g.EventID); err != nil {
			return "", false
		}
		if !yes {
			return "bad ticket type", true
		}
	}
	return "", true
}

// serveCampaignCount answers POST /admin/campaigns/count: how many people a
// segment reaches.
func serveCampaignCount(r sdk.HTTPRequest) sdk.HTTPResponse {
	if r.User == nil || r.User.Role != "owner" {
		return forbidden()
	}
	var b countBody
	if msg := decodeBody(r, &b); msg != "" {
		return failReply(http.StatusBadRequest, msg)
	}
	msg, ok := checkSegment(b.Segment)
	if !ok {
		return unavailable()
	}
	if msg != "" {
		return failReply(http.StatusBadRequest, msg)
	}
	n, err := countSegment(b.Segment)
	if err != nil {
		return unavailable()
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "count": n})
}

type campaignBody struct {
	Subject string  `json:"subject"`
	Format  string  `json:"format"`
	Body    string  `json:"body"`
	Segment segment `json:"segment"`
	SendAt  string  `json:"send_at"` // RFC 3339; "" = now
}

// linkRow is a stored campaign link. Path is set only for a link to this site.
type linkRow struct {
	Idx  int     `json:"idx"`
	URL  string  `json:"url"`
	Path *string `json:"path"`
}

// storedLinks turns the links of a body into rows. A link whose scheme and
// host are the site's gets the path (and query) to redirect to; every other
// link gets none and is shown on an interstitial page.
func storedLinks(links []string, origin string) []linkRow {
	rows := make([]linkRow, 0, len(links))
	for i, l := range links {
		row := linkRow{Idx: i, URL: l}
		if u, err := url.Parse(l); err == nil && strings.EqualFold(u.Scheme+"://"+u.Host, origin) {
			p := u.EscapedPath()
			if p == "" {
				p = "/"
			}
			if u.RawQuery != "" {
				p += "?" + u.RawQuery
			}
			if !strings.HasPrefix(p, "//") && !strings.HasPrefix(p, `/\`) {
				row.Path = &p
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// serveCreateCampaign answers POST /admin/campaigns.
func serveCreateCampaign(r sdk.HTTPRequest) sdk.HTTPResponse {
	if r.User == nil || r.User.Role != "owner" {
		return forbidden()
	}
	var b campaignBody
	if msg := decodeBody(r, &b); msg != "" {
		return failReply(http.StatusBadRequest, msg)
	}
	subject := plainText(b.Subject, false)
	if subject == "" || len([]rune(subject)) > maxCampaignSubject {
		return failReply(http.StatusBadRequest, "bad subject")
	}
	parsed, msg := parseBody(b.Format, b.Body)
	if msg != "" {
		return failReply(http.StatusBadRequest, "bad body: "+msg)
	}
	at := now().UTC()
	if b.SendAt != "" {
		t, err := time.Parse(time.RFC3339, b.SendAt)
		if err != nil || t.Before(at.Add(-5*time.Minute)) || t.After(at.AddDate(0, 0, 366)) {
			return failReply(http.StatusBadRequest, "bad send time")
		}
		at = t.UTC()
	}
	origin := siteOrigin()
	if origin == "" {
		return failReply(http.StatusBadRequest, "Set Site URL in the plugin settings")
	}
	segMsg, ok := checkSegment(b.Segment)
	if !ok {
		return unavailable()
	}
	if segMsg != "" {
		return failReply(http.StatusBadRequest, segMsg)
	}
	segJSON, _ := json.Marshal(b.Segment)
	linksJSON, _ := json.Marshal(storedLinks(parsed.Links, origin))
	var id int64
	err := dbJSON(`WITH c AS (INSERT INTO campaigns (subject, format, body, segment, event_id, send_at)
  VALUES ($1, $2, $3, $4::jsonb, $5, $6::timestamptz) RETURNING id),
 l AS (INSERT INTO campaign_links (campaign_id, idx, url, path)
  SELECT c.id, r.idx, r.url, r.path FROM c, jsonb_to_recordset($7::jsonb) AS r(idx int, url text, path text) RETURNING 1)
SELECT to_jsonb(c.id) FROM c`, &id, subject, b.Format, b.Body, string(segJSON), b.Segment.EventID, at.Format(time.RFC3339), string(linksJSON))
	if err != nil {
		return unavailable()
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "id": id})
}

// serveCancelCampaign answers POST /admin/campaigns/{id}/cancel: the campaign
// stops and what is still queued is skipped, in one statement.
func serveCancelCampaign(u *sdk.HTTPUser, rawID string) sdk.HTTPResponse {
	if u == nil || u.Role != "owner" {
		return forbidden()
	}
	id, ok := parseID(rawID)
	if !ok {
		return failReply(http.StatusBadRequest, "bad id")
	}
	var found bool
	err := dbJSON(`WITH c AS (UPDATE campaigns SET status = 'cancelled' WHERE id = $1 AND status IN ('scheduled', 'sending') RETURNING id),
 s AS (UPDATE email_sends SET status = 'skipped' WHERE kind = 'campaign' AND status = 'queued' AND ref_id IN (SELECT id FROM c) RETURNING 1)
SELECT to_jsonb(EXISTS (SELECT 1 FROM c))`, &found, id)
	switch {
	case err != nil:
		return unavailable()
	case !found:
		return failReply(http.StatusNotFound, "not found")
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true})
}

// campaignStatsSQL lists the newest campaigns with their counts, from the
// indexed email_sends rows only, so it stays cheap.
const campaignStatsSQL = `SELECT coalesce(jsonb_agg(jsonb_build_object('id', x.id, 'subject', x.subject, 'segment', x.segment,
    'status', x.status, 'send_at', x.send_at, 'total', x.total, 'sent', x.sent, 'opens', x.opens, 'clicks', x.clicks)
  ORDER BY x.id DESC), '[]'::jsonb)
FROM (SELECT c.id, c.subject, c.segment, c.status, c.send_at,
  (SELECT count(*) FROM email_sends s WHERE s.kind = 'campaign' AND s.ref_id = c.id) AS total,
  (SELECT count(*) FROM email_sends s WHERE s.kind = 'campaign' AND s.ref_id = c.id AND s.status = 'sent') AS sent,
  (SELECT count(*) FROM email_sends s WHERE s.kind = 'campaign' AND s.ref_id = c.id AND s.opened_at IS NOT NULL) AS opens,
  (SELECT count(*) FROM email_sends s WHERE s.kind = 'campaign' AND s.ref_id = c.id AND s.clicked_at IS NOT NULL) AS clicks
  FROM campaigns c ORDER BY c.id DESC LIMIT 50) x`

// campaignBuysSQL counts, per campaign of that list, the buys: orders of the
// campaign's event (any event when it has none) by a person who clicked,
// within seven days of the click. It goes click by click (LATERAL), so the
// orders' status/date index limits the addresses hashed to the orders of that
// week, not every order. It is a separate statement so a slow or failed count
// never hides the campaigns.
const campaignBuysSQL = `SELECT coalesce(jsonb_object_agg(b.id::text, b.n), '{}'::jsonb)
FROM (SELECT c.id, count(DISTINCT m.id) AS n
  FROM (SELECT id, event_id FROM campaigns ORDER BY id DESC LIMIT 50) c
  JOIN email_sends s ON s.kind = 'campaign' AND s.ref_id = c.id AND s.clicked_at IS NOT NULL
  CROSS JOIN LATERAL (SELECT o.id FROM orders o
    WHERE o.status IN ('confirmed', 'paid')
      AND o.created_at >= s.clicked_at AND o.created_at < s.clicked_at + interval '` + conversionWindow + `'
      AND (c.event_id = '' OR o.event_id = c.event_id)
      AND ` + hashSQL + ` = s.email_hash) m
  GROUP BY c.id) b`

// readCampaigns is the list the Emails tab shows. An error is only the list
// failing; when just the buys cannot be counted the campaigns come back with
// "buys": null, which the tab shows as unavailable.
func readCampaigns() ([]map[string]any, error) {
	var list []map[string]any
	if err := dbJSON(campaignStatsSQL, &list); err != nil {
		return nil, err
	}
	var buys map[string]int
	buysErr := dbJSON(campaignBuysSQL, &buys)
	for _, c := range list {
		if buysErr != nil {
			c["buys"] = nil
			continue
		}
		id, _ := c["id"].(float64)
		c["buys"] = buys[strconv.FormatInt(int64(id), 10)]
	}
	return list, nil
}

// campaignFanoutSQL flips a due campaign to sending and writes one send per
// person in the same statement, so it runs once however often the job does.
func campaignFanoutSQL(cond string) string {
	return `WITH c AS (UPDATE campaigns SET status = 'sending' WHERE id = $1 AND status = 'scheduled' RETURNING id, event_id)
INSERT INTO email_sends (kind, ref_id, event_id, order_id, email_hash, code)
SELECT DISTINCT ON (lower(o.email)) 'campaign', c.id, c.event_id, o.id, ` + hashSQL + `, ` + codeSQL + `
FROM c, orders o WHERE ` + cond + `
ORDER BY lower(o.email), o.id
ON CONFLICT DO NOTHING`
}

// fanoutCampaigns starts the campaigns whose time has come.
func fanoutCampaigns(_ automations, over func() bool) {
	var ids []int64
	if err := dbJSON(`SELECT coalesce(jsonb_agg(x.id ORDER BY x.send_at, x.id), '[]'::jsonb)
FROM (SELECT id, send_at FROM campaigns WHERE status = 'scheduled' AND send_at <= now() ORDER BY send_at, id LIMIT $1) x`, &ids, campaignsPerRun); err != nil {
		return
	}
	for _, id := range ids {
		if over() {
			return
		}
		var g *segment
		if err := dbJSON(`SELECT coalesce((SELECT segment FROM campaigns WHERE id = $1 AND status = 'scheduled'), 'null'::jsonb)`, &g, id); err != nil || g == nil {
			continue
		}
		if g.validate() != "" {
			_ = dbExec(`UPDATE campaigns SET status = 'cancelled' WHERE id = $1 AND status = 'scheduled'`, id)
			continue
		}
		cond, args := g.where(2)
		_ = dbExec(campaignFanoutSQL(cond), append([]any{id}, args...)...)
	}
}

// finishCampaigns closes the campaigns whose sends are all done.
func finishCampaigns() {
	_ = dbExec(`UPDATE campaigns c SET status = 'sent' WHERE status = 'sending'
  AND NOT EXISTS (SELECT 1 FROM email_sends s WHERE s.kind = 'campaign' AND s.ref_id = c.id AND s.status = 'queued')`)
}

// composeCampaign builds one campaign mail. ok is false when the campaign was
// cancelled, the person is gone or there is no site address for the links.
func composeCampaign(s sendRow) (m outMail, ok bool, err error) {
	var c *struct {
		Subject string `json:"subject"`
		Format  string `json:"format"`
		Body    string `json:"body"`
	}
	if err := dbJSON(`SELECT coalesce((SELECT jsonb_build_object('subject', subject, 'format', format, 'body', body)
  FROM campaigns WHERE id = $1 AND status = 'sending'), 'null'::jsonb)`, &c, s.RefID); err != nil {
		return outMail{}, false, err
	}
	if c == nil {
		return outMail{}, false, nil
	}
	parsed, msg := parseBody(c.Format, c.Body)
	if msg != "" {
		return outMail{}, false, nil
	}
	o, err := readOrderMail(s.OrderID)
	if err != nil || o == nil || o.Anonymised || o.Email == "" || len(o.Tickets) == 0 {
		return outMail{}, false, err
	}
	origin := siteOrigin()
	if origin == "" {
		return outMail{}, false, nil
	}
	key, err := hmacSecret()
	if err != nil {
		return outMail{}, false, err
	}
	href := func(i int) string {
		n := strconv.Itoa(i)
		return origin + "/p/event-tickets/c/" + signCode(key, kindClick(i), s.Code) + "?l=" + n
	}
	unsub := unsubscribeURL(origin, key, s.Code)
	pixel := origin + "/p/event-tickets/o/" + signCode(key, kindOpen, s.Code) + ".gif"
	var h bytes.Buffer
	h.WriteString(parsed.html(href))
	h.WriteString(`<img src="` + html.EscapeString(pixel) + `" width="1" height="1" alt="">`)
	h.WriteString(`<hr><p>` + html.EscapeString(unsubscribeNote) + ` Unsubscribe: <a href="` + html.EscapeString(unsub) + `">` + html.EscapeString(unsub) + `</a></p>`)
	text := parsed.text(href) + "\n\n--\n" + unsubscribeNote + " Unsubscribe: " + unsub + "\n"
	return outMail{To: o.Email, Subject: c.Subject, Text: text, HTML: h.String()}, true, nil
}
