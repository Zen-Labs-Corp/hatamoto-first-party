package main

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxMapRows   = 50
	maxNameLen   = 100
	maxValueLen  = 1000
	maxJobFields = 100
	maxNoteBytes = 10240
)

// defaultMap is the mapping in use until the owner saves one.
func defaultMap(provider, object string) []mapRow {
	switch provider {
	case "hubspot":
		return []mapRow{{"first_name", "firstname"}, {"last_name", "lastname"}, {"phone", "phone"}, {"company", "company"}}
	case "pipedrive":
		return []mapRow{{"first_name", "first_name"}, {"last_name", "last_name"}, {"phone", "phones"}, {"company", "org_name"}}
	case "salesforce":
		rows := []mapRow{{"first_name", "FirstName"}, {"last_name", "LastName"}, {"phone", "Phone"}}
		if object != "contact" {
			rows = append(rows, mapRow{"company", "Company"})
		}
		return rows
	}
	return nil
}

func mapKey(provider string) string { return "map_" + provider }

// loadMap returns the saved mapping, or the default (saved false) when none is
// saved or kv cannot be read.
func loadMap(provider, object string) (rows []mapRow, saved bool) {
	var got []mapRow
	if ok, err := kvGet(mapKey(provider), &got); err != nil || !ok {
		return defaultMap(provider, object), false
	}
	return got, true
}

// saveMap stores the mapping with trimmed names.
func saveMap(provider string, rows []mapRow) error {
	clean := make([]mapRow, len(rows))
	for i, r := range rows {
		clean[i] = mapRow{strings.TrimSpace(r.FormField), strings.TrimSpace(r.CRMField)}
	}
	return kvSet(mapKey(provider), clean, 0)
}

// validateMap checks a mapping before it is saved. Errors are fixed sentences.
func validateMap(rows []mapRow) error {
	if len(rows) > maxMapRows {
		return fmt.Errorf("at most %d rows", maxMapRows)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		form, crm := strings.TrimSpace(r.FormField), strings.TrimSpace(r.CRMField)
		if form == "" || crm == "" {
			return errors.New("every row needs a form field and a CRM field")
		}
		if utf8.RuneCountInString(form) > maxNameLen || utf8.RuneCountInString(crm) > maxNameLen {
			return fmt.Errorf("names are at most %d characters", maxNameLen)
		}
		if seen[crm] {
			return errors.New("a CRM field can be mapped once")
		}
		seen[crm] = true
	}
	return nil
}

// cut shortens s to n runes.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// renderValue is the text sent for a submitted value, at most 1000 characters.
func renderValue(v any) string {
	var s string
	switch x := v.(type) {
	case string:
		s = strings.TrimSpace(x)
	case bool:
		s = strconv.FormatBool(x)
	case float64:
		s = strconv.FormatFloat(x, 'f', -1, 64)
	case []any:
		var parts []string
		for _, e := range x {
			if p := renderValue(e); p != "" {
				parts = append(parts, p)
			}
		}
		s = strings.Join(parts, ";")
	}
	return cut(s, maxValueLen)
}

// flattenFields renders every submitted value, drops empty ones and keeps the
// first 100 names in sorted order.
func flattenFields(m map[string]any) map[string]string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]string{}
	for _, k := range keys {
		if len(out) >= maxJobFields {
			break
		}
		if v := renderValue(m[k]); v != "" {
			out[k] = v
		}
	}
	return out
}

// applyMapping turns submitted fields into CRM fields. Empty values are
// omitted; the first row wins when a CRM field is mapped twice.
func applyMapping(fields map[string]string, rows []mapRow) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		if _, dup := out[r.CRMField]; dup {
			continue
		}
		if v := fields[r.FormField]; v != "" {
			out[r.CRMField] = v
		}
	}
	return out
}

// noteText is the plain-text note: a header, then every submitted field as
// "name: value" in name order, at most 10 KB.
func noteText(j job, leadSource string) string {
	var b strings.Builder
	b.WriteString("Form: " + j.FormSlug + "\n")
	b.WriteString("Page: " + j.SourcePath + "\n")
	b.WriteString("Submitted: " + j.SubmittedAt.UTC().Format(time.RFC3339) + "\n")
	b.WriteString("Lead source: " + leadSource + "\n\n")
	names := make([]string, 0, len(j.Fields))
	for k := range j.Fields {
		names = append(names, k)
	}
	slices.Sort(names)
	for _, k := range names {
		b.WriteString(k + ": " + j.Fields[k] + "\n")
	}
	s := b.String()
	if len(s) > maxNoteBytes {
		s = s[:maxNoteBytes]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}
