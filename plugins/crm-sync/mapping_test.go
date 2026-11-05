package main

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestApplyMapping(t *testing.T) {
	fields := map[string]string{"first_name": "Ada", "last_name": "", "phone": "123", "extra": "x"}
	rows := []mapRow{
		{"first_name", "firstname"},
		{"last_name", "lastname"},
		{"phone", "phone"},
		{"first_name", "phone"},
		{"missing", "company"},
	}
	got := applyMapping(fields, rows)
	if len(got) != 2 || got["firstname"] != "Ada" || got["phone"] != "123" {
		t.Fatalf("mapped %v", got)
	}
	if applyMapping(fields, []mapRow{{"first_name", "a"}, {"phone", "a"}})["a"] != "Ada" {
		t.Fatal("the first row must win on a duplicate CRM field")
	}
}

func TestRenderValue(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"  Ada ", "Ada"},
		{true, "true"},
		{false, "false"},
		{1.0, "1"},
		{1.5, "1.5"},
		{1e21, "1000000000000000000000"},
		{[]any{"a", "", "b"}, "a;b"},
		{[]any{" ", 2.0, true}, "2;true"},
		{nil, ""},
		{map[string]any{"a": 1}, ""},
	}
	for _, tc := range cases {
		if got := renderValue(tc.in); got != tc.want {
			t.Errorf("renderValue(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := renderValue(strings.Repeat("é", 1500)); utf8.RuneCountInString(got) != 1000 {
		t.Fatalf("cut to %d runes", utf8.RuneCountInString(got))
	}
}

func TestFlattenFields(t *testing.T) {
	m := map[string]any{"b": "2", "a": " 1 ", "empty": "", "nil": nil}
	got := flattenFields(m)
	if len(got) != 2 || got["a"] != "1" || got["b"] != "2" {
		t.Fatalf("flattened %v", got)
	}
	big := map[string]any{}
	for i := range 150 {
		big["f"+string(rune('a'+i/26))+string(rune('a'+i%26))] = "v"
	}
	if got := flattenFields(big); len(got) != 100 {
		t.Fatalf("%d keys", len(got))
	}
}

func TestDefaultMapPerProvider(t *testing.T) {
	find := func(rows []mapRow, form string) string {
		for _, r := range rows {
			if r.FormField == form {
				return r.CRMField
			}
		}
		return ""
	}
	if got := find(defaultMap("hubspot", ""), "first_name"); got != "firstname" {
		t.Fatalf("hubspot first_name -> %q", got)
	}
	if got := find(defaultMap("pipedrive", ""), "phone"); got != "phones" {
		t.Fatalf("pipedrive phone -> %q", got)
	}
	if got := find(defaultMap("pipedrive", ""), "company"); got != "org_name" {
		t.Fatalf("pipedrive company -> %q", got)
	}
	if got := find(defaultMap("salesforce", "lead"), "company"); got != "Company" {
		t.Fatalf("salesforce lead company -> %q", got)
	}
	if got := find(defaultMap("salesforce", "contact"), "company"); got != "" {
		t.Fatalf("a contact has no company row, got %q", got)
	}
	if defaultMap("nope", "") != nil {
		t.Fatal("unknown provider")
	}
}

func TestMapSaveLoad(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	rows, saved := loadMap("hubspot", "")
	if saved || len(rows) != 4 {
		t.Fatalf("default: saved %v rows %v", saved, rows)
	}
	want := []mapRow{{"nick", "firstname"}, {"mail_phone", "phone"}}
	if err := saveMap("hubspot", []mapRow{{" nick ", "firstname"}, {"mail_phone", " phone"}}); err != nil {
		t.Fatal(err)
	}
	if h.kvTTL["map_hubspot"] != 0 {
		t.Fatal("the mapping must not expire")
	}
	rows, saved = loadMap("hubspot", "")
	if !saved || !slices.Equal(rows, want) {
		t.Fatalf("saved %v rows %v", saved, rows)
	}
	if _, saved := loadMap("pipedrive", ""); saved {
		t.Fatal("mappings are per provider")
	}
	h.kv["map_pipedrive"] = []byte(`"not a list"`)
	if rows, saved := loadMap("pipedrive", ""); saved || len(rows) != 4 {
		t.Fatalf("unreadable value must give the default, got %v %v", rows, saved)
	}
}

func TestValidateMap(t *testing.T) {
	if err := validateMap([]mapRow{{"a", "b"}, {"c", "d"}}); err != nil {
		t.Fatal(err)
	}
	if err := validateMap(nil); err != nil {
		t.Fatal("an empty mapping is valid")
	}
	long := strings.Repeat("x", 101)
	many := make([]mapRow, 51)
	for i := range many {
		many[i] = mapRow{"f", "c" + string(rune('a'+i%26)) + string(rune('a'+i/26))}
	}
	for name, rows := range map[string][]mapRow{
		"empty form": {{"", "b"}},
		"blank crm":  {{"a", "  "}},
		"long form":  {{long, "b"}},
		"long crm":   {{"a", long}},
		"duplicate":  {{"a", "x"}, {"b", "x"}},
		"too many":   many,
	} {
		if err := validateMap(rows); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestValidateMapRefusesPrivilegedFields(t *testing.T) {
	for _, crm := range []string{"OwnerId", "RecordTypeId", "Id", "IsConverted", "IsDeleted", "CreatedById", "LastModifiedById", "hubspot_owner_id", "owner_id", "visible_to", "ownerid"} {
		err := validateMap([]mapRow{{"a", crm}})
		if err == nil || !strings.Contains(err.Error(), crm) {
			t.Errorf("%s: %v", crm, err)
		}
	}
	if err := validateMap([]mapRow{{"a", "lifecyclestage"}, {"b", "LeadSource"}}); err != nil {
		t.Fatal(err)
	}
}

func TestNoteTextBoundedAndSorted(t *testing.T) {
	j := job{
		FormSlug: "contact", SourcePath: "/contact",
		SubmittedAt: time.Date(2026, 11, 5, 9, 30, 0, 0, time.UTC),
		Email:       "ada@example.com",
		Fields:      map[string]string{"message": "Hello", "first_name": "Ada", "a_key": "1"},
	}
	got := noteText(j, "Website")
	want := "Form: contact\nPage: /contact\nSubmitted: 2026-11-05T09:30:00Z\nLead source: Website\n\na_key: 1\nfirst_name: Ada\nmessage: Hello\n"
	if got != want {
		t.Fatalf("note:\n%s", got)
	}
	if strings.Contains(got, "ada@example.com") {
		t.Fatal("the email is not part of the note unless submitted as a field")
	}
	j.Fields = map[string]string{}
	for i := range 40 {
		j.Fields["f"+string(rune('a'+i%26))+string(rune('a'+i/26))] = strings.Repeat("é", 1000)
	}
	got = noteText(j, "Website")
	if len(got) > 10240 || !utf8.ValidString(got) {
		t.Fatalf("note is %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
}
