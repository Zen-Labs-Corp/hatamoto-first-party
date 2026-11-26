package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

type schemaNode struct {
	Type                 string                `json:"type"`
	Format               string                `json:"format"`
	Pattern              string                `json:"pattern"`
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

// reservedPropertyNames is core's denylist of names that would pollute the
// JavaScript object prototype (entity.reservedPropertyNames).
var reservedPropertyNames = map[string]bool{
	"__proto__": true, "constructor": true, "prototype": true, "hasOwnProperty": true,
	"isPrototypeOf": true, "propertyIsEnumerable": true, "toString": true, "valueOf": true,
	"__defineGetter__": true, "__defineSetter__": true, "__lookupGetter__": true, "__lookupSetter__": true,
}

// shapeErrors mirrors core's entity.ValidateSchemaShape, which refuses a content
// type at install time (the plugin is then enabled without it): every node has a
// supported type, objects have properties and no additionalProperties or
// patternProperties, property names match nameRe, required names exist, every
// array has items, no array holds an array and no property name is reserved.
func shapeErrors(node map[string]any, path string, nameRe *regexp.Regexp) []string {
	var errs []string
	typ, _ := node["type"].(string)
	switch typ {
	case "":
		return []string{path + " missing 'type'"}
	case "string", "number", "integer", "boolean":
	case "object":
		for _, k := range []string{"additionalProperties", "patternProperties"} {
			if _, ok := node[k]; ok {
				errs = append(errs, path+" has "+k)
			}
		}
		props, ok := node["properties"].(map[string]any)
		if !ok {
			return append(errs, path+" of type 'object' missing 'properties'")
		}
		for name, c := range props {
			if reservedPropertyNames[name] {
				errs = append(errs, "property name "+name+" is reserved")
			}
			if !nameRe.MatchString(name) {
				errs = append(errs, "property name "+name+" is invalid")
			}
			cm, ok := c.(map[string]any)
			if !ok {
				errs = append(errs, path+".properties."+name+" is not an object")
				continue
			}
			errs = append(errs, shapeErrors(cm, path+".properties."+name, nameRe)...)
		}
		if req, ok := node["required"].([]any); ok {
			for _, r := range req {
				if name, _ := r.(string); props[name] == nil {
					errs = append(errs, path+" requires unknown property "+name)
				}
			}
		}
	case "array":
		items, ok := node["items"].(map[string]any)
		if !ok {
			return []string{path + " of type 'array' missing 'items'"}
		}
		if items["type"] == "array" {
			errs = append(errs, path+".items is a nested array")
		}
		errs = append(errs, shapeErrors(items, path+".items", nameRe)...)
	default:
		errs = append(errs, path+" has unsupported type "+typ)
	}
	return errs
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
		var generic map[string]any
		if err := json.Unmarshal(raw, &generic); err != nil {
			t.Fatalf("%s: %v", slug, err)
		}
		for _, e := range shapeErrors(generic, "root", nameRe) {
			t.Errorf("%s: core would refuse this schema: %s", slug, e)
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

func TestShapeMirrorRejectsReservedNames(t *testing.T) {
	nameRe := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	node := map[string]any{"type": "object", "properties": map[string]any{"constructor": map[string]any{"type": "string"}}}
	if errs := shapeErrors(node, "$", nameRe); len(errs) != 1 || !strings.Contains(errs[0], "reserved") {
		t.Errorf("errs = %v", errs)
	}
}

// schemaAcceptsTime reports whether core's entity validator takes v for
// calendar-event start_at/end_at. Core asserts a date-time format as strict
// RFC 3339 on save and on publish.
func schemaAcceptsTime(t *testing.T, name, v string) bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("schemas", "calendar-event.json"))
	if err != nil {
		t.Fatal(err)
	}
	var n schemaNode
	if err := json.Unmarshal(raw, &n); err != nil {
		t.Fatal(err)
	}
	p := n.Properties[name]
	if strings.Contains(p.Pattern, "[+-]") {
		t.Errorf("%s: pattern has an unescaped '-' in a class; browsers compile it with the v flag and drop it", name)
	}
	if p.Format != "" {
		_, err := time.Parse(time.RFC3339, v)
		return err == nil
	}
	return p.Pattern == "" || regexp.MustCompile(p.Pattern).MatchString(v)
}

func TestTimeFieldsTakeWhatTheEditorPosts(t *testing.T) {
	for _, name := range []string{"start_at", "end_at"} {
		for _, v := range []string{"2026-12-20T20:00:00Z", "2026-12-20T20:00:00+01:00", "2026-12-20T20:00:30.5Z"} {
			if !schemaAcceptsTime(t, name, v) {
				t.Errorf("%s refuses %q", name, v)
			}
		}
		for _, v := range []string{"", "2026-12-20", "20:00", "tomorrow", "2026-12-20 20:00", "2026-12-20T20:00", "2026-12-20T20:00:30"} {
			if v != "" && schemaAcceptsTime(t, name, v) {
				t.Errorf("%s takes %q", name, v)
			}
		}
	}
}

// Sites installed before date-time keep the pattern the fields had in 0.5.0
// (evolution never retypes), so what the plugin writes must match it too.
func TestOldPatternTakesWhatThePluginWrites(t *testing.T) {
	old := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?(Z|[+\-]\d{2}:\d{2})?$`)
	berlin, _ := time.LoadLocation("Europe/Berlin")
	allDay := importEvent{Start: time.Date(2027, 7, 1, 0, 0, 0, 0, berlin), AllDay: true}
	attrs := buildCreateAttrs(allDay, berlin, "google", "k", "", "")
	vals := []string{
		editorTime(time.Date(2026, 12, 20, 20, 0, 0, 0, time.UTC), berlin),
		editorTime(time.Date(2027, 6, 12, 18, 0, 0, 0, time.UTC), time.UTC),
		attrText(t, attrs, "start_at"),
	}
	for _, v := range vals {
		if !old.MatchString(v) {
			t.Errorf("the 0.5.0 pattern refuses %q", v)
		}
	}
}

// Core keeps a link_fields property to these keys (its LinkFieldDef): any
// other, such as x-hm-group, makes core refuse the standing approval.
func TestLinkFieldsArePlain(t *testing.T) {
	m, _ := loadManifest(t)
	var fields []string
	for _, r := range m.Runs {
		if r.StandingApproval != nil {
			fields = r.StandingApproval.Create.LinkFields
		}
	}
	if len(fields) == 0 {
		t.Fatal("no link_fields declared")
	}
	raw, err := os.ReadFile(filepath.Join("schemas", "calendar-event.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	allowed := []string{"type", "format", "title", "description"}
	for _, f := range fields {
		prop, ok := schema.Properties[f]
		if !ok {
			t.Errorf("link field %s is not a calendar-event property", f)
			continue
		}
		for k := range prop {
			if !slices.Contains(allowed, k) {
				t.Errorf("link field %s carries %q, core allows only %v", f, k, allowed)
			}
		}
		if prop["type"] != "string" || prop["format"] != "uri" {
			t.Errorf("link field %s = %v, want a string with format uri", f, prop)
		}
	}
}
