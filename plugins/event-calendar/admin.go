package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// The data behind the "Repeating events" admin page: the repeating events of
// the index, each with its next dates and their state. Read only; changes go
// through the edit_dates run.

const (
	maxAdminEvents = 50
	maxAdminReads  = 30
	adminDates     = 10
	adminBudget    = 2 * time.Second
)

// adminDate is one date of a repeating event. Day is the generated day the
// run addresses; Date is where the date falls now (a changed date may have
// moved). Start and End are times of day, empty for an all-day date, and End
// is empty when the date ends on another day.
type adminDate struct {
	Day   string `json:"day"`
	Date  string `json:"date"`
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
	State string `json:"state"`
}

type adminEvent struct {
	ID    string      `json:"id"`
	Slug  string      `json:"slug"`
	Name  string      `json:"name"`
	Rule  string      `json:"rule"`
	Owned bool        `json:"owned"`
	Dates []adminDate `json:"dates"`
}

type adminList struct {
	Ready  bool         `json:"ready"`
	Role   string       `json:"role"`
	Events []adminEvent `json:"events"`
}

const repeatingSQL = `SELECT CASE WHEN EXISTS (SELECT 1 FROM index_state)
 THEN COALESCE(jsonb_agg(to_jsonb(r) ORDER BY r.name, r.id), '[]'::jsonb) END FROM (
SELECT e.entity_id AS id, e.slug, e.name, e.source_uid FROM events e
WHERE e.repeating ORDER BY e.name, e.entity_id LIMIT $1::int) r`

// serveRepeating answers GET /admin/repeating. It is an admin route: without a
// signed-in user it is refused. A failure reading one event leaves that event
// listed without dates.
func serveRepeating(u *sdk.HTTPUser) sdk.HTTPResponse {
	if u == nil {
		return plain(http.StatusForbidden, "forbidden")
	}
	out := adminList{Role: u.Role, Events: []adminEvent{}}
	var rows *[]map[string]any
	if err := dbJSON(repeatingSQL, &rows, maxAdminEvents); err != nil {
		return plain(http.StatusServiceUnavailable, "unavailable")
	}
	if rows != nil {
		out.Ready = true
		s := loadSettings()
		at := now()
		_, to := windowFor(at)
		started := clock()
		reads := 0
		for _, m := range *rows {
			ae := adminEvent{ID: attrString(m, "id"), Slug: attrString(m, "slug"), Name: attrString(m, "name"), Dates: []adminDate{}}
			_, ae.Owned = ownedKey(attrString(m, "source_uid"))
			if reads < maxAdminReads && clock().Sub(started) < adminBudget {
				reads++
				var raw entityDTO
				if callHost(sdk.FnEntitiesGet, map[string]any{"id": ae.ID}, &raw) == nil {
					if e, ok := eventFromEntity(raw, s); ok {
						loc := eventLocation(e, s)
						ae.Rule = ruleText(e, loc)
						for _, o := range expandAll(e, loc, at, to, adminDates) {
							ae.Dates = append(ae.Dates, adminDateOf(o, e.AllDay, loc))
						}
					}
				}
			}
			out.Events = append(out.Events, ae)
		}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return plain(http.StatusInternalServerError, "error")
	}
	return sdk.HTTPResponse{
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json; charset=utf-8"},
		Body:    string(body),
	}
}

func adminDateOf(o occ, allDay bool, loc *time.Location) adminDate {
	start := o.Start.In(loc)
	d := adminDate{Day: o.Day, Date: start.Format("2006-01-02"), State: o.State}
	if allDay {
		return d
	}
	d.Start = start.Format("15:04")
	if end := o.End.In(loc); end.After(start) && end.Format("2006-01-02") == d.Date {
		d.End = end.Format("15:04")
	}
	return d
}

var weekdayShort = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// ruleText summarises the rule in a few words: "Weekly, Tue, ends never",
// "Monthly, 2nd Thu, 12x".
func ruleText(e Event, loc *time.Location) string {
	r := e.Repeat
	n := max(r.Every, 1)
	base := e.Start.In(loc)
	var parts []string
	unit := map[string][2]string{
		"daily": {"Daily", "days"}, "weekly": {"Weekly", "weeks"},
		"monthly": {"Monthly", "months"}, "yearly": {"Yearly", "years"},
	}[r.Kind]
	if unit[0] == "" {
		return "Does not repeat"
	}
	if n == 1 {
		parts = append(parts, unit[0])
	} else {
		parts = append(parts, fmt.Sprintf("Every %d %s", n, unit[1]))
	}
	switch r.Kind {
	case "weekly":
		days := r.Weekdays
		if len(days) == 0 {
			days = []time.Weekday{base.Weekday()}
		}
		var names []string
		for _, d := range days {
			names = append(names, weekdayShort[d])
		}
		parts = append(parts, strings.Join(names, " "))
	case "monthly":
		if r.By == "nth_weekday" {
			nth := r.Nth
			if nth == 0 {
				nth = (base.Day()-1)/7 + 1
			}
			parts = append(parts, ordinal(nth)+" "+weekdayShort[base.Weekday()])
		} else {
			parts = append(parts, fmt.Sprintf("day %d", base.Day()))
		}
	}
	switch {
	case r.Count > 0:
		parts = append(parts, fmt.Sprintf("%dx", r.Count))
	case !r.Until.IsZero():
		parts = append(parts, "until "+r.Until.In(loc).Format("2006-01-02"))
	default:
		parts = append(parts, "ends never")
	}
	return strings.Join(parts, ", ")
}

func ordinal(n int) string {
	switch n {
	case -1:
		return "last"
	case 1:
		return "1st"
	case 2:
		return "2nd"
	case 3:
		return "3rd"
	}
	return fmt.Sprintf("%dth", n)
}
