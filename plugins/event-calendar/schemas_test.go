package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

type schemaNode struct {
	Type                 string                `json:"type"`
	Format               string                `json:"format"`
	Enum                 []string              `json:"enum"`
	Items                *schemaNode           `json:"items"`
	Properties           map[string]schemaNode `json:"properties"`
	Required             []string              `json:"required"`
	EntitySchema         string                `json:"x-entity-schema"`
	Multiple             *bool                 `json:"x-multiple"`
	AdditionalProperties json.RawMessage       `json:"additionalProperties"`
	PatternProperties    json.RawMessage       `json:"patternProperties"`
}

// fieldSpec is the expected shape of one property: its type and format, the enum
// on the node, and for arrays the format and enum of the items.
type fieldSpec struct {
	typ, format string
	enum        []string
	itemEnum    []string
	itemFormat  string
	free        bool // array of objects owned by core (page_sections): items are not checked
}

func str(format string) fieldSpec      { return fieldSpec{typ: "string", format: format} }
func en(vals ...string) fieldSpec      { return fieldSpec{typ: "string", enum: vals} }
func arr(itemFormat string) fieldSpec  { return fieldSpec{typ: "array", itemFormat: itemFormat} }
func arrEnum(vals ...string) fieldSpec { return fieldSpec{typ: "array", itemEnum: vals} }
func scalar(typ string) fieldSpec      { return fieldSpec{typ: typ} }

// sections is core's page_sections property: its presence is what opens the page
// builder for the content type.
var sections = fieldSpec{typ: "array", free: true}

var wantSchemas = map[string]map[string]fieldSpec{
	"calendar-event": {
		"page_sections": sections,
		"start_at":      str("date-time"), "end_at": str("date-time"), "all_day": scalar("boolean"),
		"time_zone":       str(""),
		"event_status":    en("scheduled", "cancelled", "postponed"),
		"attendance_mode": en("offline", "online", "hybrid"),
		"online_url":      str("uri"),
		"venue":           arr(""), "venue_name": str(""), "venue_address": str(""), "venue_city": str(""),
		"organizers": arr(""), "category": arr(""), "series": arr(""),
		"ticket_url": str("uri"), "website": str("uri"), "price": str(""), "price_amount": scalar("number"),
		"currency": str(""), "featured": scalar("boolean"), "image": str(""), "description": str(""),
		"tags":            arr(""),
		"repeat":          en("none", "daily", "weekly", "monthly", "yearly"),
		"repeat_every":    scalar("integer"),
		"repeat_weekdays": arrEnum("mo", "tu", "we", "th", "fr", "sa", "su"),
		"repeat_by":       en("day_of_month", "nth_weekday"),
		"repeat_nth":      scalar("integer"), "repeat_until": str("date"), "repeat_count": scalar("integer"),
		"repeat_extra_dates": arr("date"), "repeat_skip_dates": arr("date"), "repeat_overrides": arr(""),
		"source_kind": en("manual", "google", "outlook", "eventbrite", "meetup", "ical", "submission"),
		"source_uid":  str(""), "source_url": str("uri"),
	},
	"calendar-venue": {
		"page_sections": sections,
		"address":       str(""), "city": str(""), "region": str(""), "postcode": str(""), "country": str(""),
		"phone": str(""), "website": str("uri"), "latitude": scalar("number"), "longitude": scalar("number"),
		"image": str(""), "description": str(""),
	},
	"calendar-organizer": {
		"page_sections": sections,
		"email":         str("email"), "phone": str(""), "website": str("uri"), "image": str(""), "description": str(""),
	},
	"calendar-series":   {"page_sections": sections, "description": str(""), "image": str("")},
	"calendar-category": {"description": str(""), "color": str("")},
}

func TestShippedSchemas(t *testing.T) {
	slugRe := regexp.MustCompile(`^[a-z]([a-z0-9]|-[a-z0-9]){0,63}$`)
	nameRe := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

	entries, err := os.ReadDir("schemas")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]schemaNode{}
	for _, e := range entries {
		slug, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			t.Fatalf("unexpected file schemas/%s", e.Name())
		}
		if !slugRe.MatchString(slug) {
			t.Errorf("slug %q is not a valid content type slug", slug)
		}
		raw, err := os.ReadFile(filepath.Join("schemas", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) >= 64<<10 {
			t.Errorf("%s is %d bytes, limit is 64 KiB", slug, len(raw))
		}
		var n schemaNode
		if err := json.Unmarshal(raw, &n); err != nil {
			t.Fatalf("%s: %v", slug, err)
		}
		got[slug] = n
	}
	if len(got) != len(wantSchemas) {
		t.Fatalf("schemas/ holds %d files, want %d", len(got), len(wantSchemas))
	}

	uriFields := map[string]bool{"ticket_url": true, "online_url": true, "website": true, "source_url": true}
	for slug, want := range wantSchemas {
		n, ok := got[slug]
		if !ok {
			t.Errorf("schemas/%s.json missing", slug)
			continue
		}
		if n.Type != "object" {
			t.Errorf("%s: root type %q", slug, n.Type)
		}
		if len(n.AdditionalProperties) > 0 || len(n.PatternProperties) > 0 {
			t.Errorf("%s: additionalProperties and patternProperties are not allowed", slug)
		}
		for name, p := range n.Properties {
			if !nameRe.MatchString(name) {
				t.Errorf("%s.%s: invalid property name", slug, name)
			}
			if p.Type == "" {
				t.Errorf("%s.%s: node without type", slug, name)
			}
			if p.Items != nil && p.Items.Type == "" {
				t.Errorf("%s.%s: items without type", slug, name)
			}
			if _, listed := want[name]; !listed {
				t.Errorf("%s.%s: not in the schema contract", slug, name)
			}
		}
		for name, w := range want {
			p, ok := n.Properties[name]
			if !ok {
				t.Errorf("%s.%s: missing", slug, name)
				continue
			}
			if p.Type != w.typ {
				t.Errorf("%s.%s: type %q, want %q", slug, name, p.Type, w.typ)
			}
			if p.Format != w.format {
				t.Errorf("%s.%s: format %q, want %q", slug, name, p.Format, w.format)
			}
			if !reflect.DeepEqual(p.Enum, w.enum) {
				t.Errorf("%s.%s: enum %v, want %v", slug, name, p.Enum, w.enum)
			}
			if w.typ == "array" && !w.free {
				if p.Items == nil || p.Items.Type != "string" {
					t.Errorf("%s.%s: items must be strings", slug, name)
				} else {
					if p.Items.Format != w.itemFormat {
						t.Errorf("%s.%s: items format %q, want %q", slug, name, p.Items.Format, w.itemFormat)
					}
					if !reflect.DeepEqual(p.Items.Enum, w.itemEnum) {
						t.Errorf("%s.%s: items enum %v, want %v", slug, name, p.Items.Enum, w.itemEnum)
					}
				}
			}
			if uriFields[name] && p.Format != "uri" {
				t.Errorf("%s.%s: link field must have format uri", slug, name)
			}
		}
	}

	ev := got["calendar-event"]
	if !reflect.DeepEqual(ev.Required, []string{"start_at"}) {
		t.Errorf("calendar-event required = %v, want [start_at]", ev.Required)
	}
	for name := range map[string]bool{"submitter_email": true, "notes": true} {
		if _, ok := ev.Properties[name]; ok {
			t.Errorf("calendar-event must not carry the private field %q", name)
		}
	}
	refs := map[string]struct {
		target string
		single bool
	}{
		"venue": {"calendar-venue", true}, "series": {"calendar-series", true},
		"organizers": {"calendar-organizer", false}, "category": {"calendar-category", false},
	}
	for name, r := range refs {
		p := ev.Properties[name]
		if p.EntitySchema != r.target {
			t.Errorf("calendar-event.%s: x-entity-schema %q, want %q", name, p.EntitySchema, r.target)
		}
		if _, shipped := got[p.EntitySchema]; !shipped {
			t.Errorf("calendar-event.%s: %q is not a shipped schema", name, p.EntitySchema)
		}
		single := p.Multiple != nil && !*p.Multiple
		if single != r.single {
			t.Errorf("calendar-event.%s: single = %v, want %v", name, single, r.single)
		}
	}
}
