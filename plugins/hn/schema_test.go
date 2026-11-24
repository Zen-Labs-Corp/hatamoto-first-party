package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSchemaDocMatchesMigrations keeps SCHEMA.md and migrations/ in step: every
// table of the marked index has exactly one CREATE TABLE in the file it names and
// no other table exists. Each migration is goose Up only and avoids the words the
// host refuses in plugin migrations.
func TestSchemaDocMatchesMigrations(t *testing.T) {
	doc, err := os.ReadFile("SCHEMA.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	begin := strings.Index(text, "<!-- tables:begin -->")
	end := strings.Index(text, "<!-- tables:end -->")
	if begin < 0 || end < begin {
		t.Fatal("SCHEMA.md: tables:begin / tables:end markers missing")
	}
	row := regexp.MustCompile("^\\| `([a-z_]+)` \\| `(0[1-5][0-9]_[a-z_]+\\.sql)` \\| ([a-z]+) \\|$")
	want := map[string]string{}
	for _, line := range strings.Split(text[begin:end], "\n") {
		m := row.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		if _, dup := want[m[1]]; dup {
			t.Errorf("SCHEMA.md lists table %s twice", m[1])
		}
		want[m[1]] = m[2]
	}
	if len(want) != 19 {
		t.Errorf("SCHEMA.md lists %d tables, want 19", len(want))
	}

	files, err := filepath.Glob("migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations: %v", err)
	}
	name := regexp.MustCompile(`^0[1-5][0-9]_[a-z_]+\.sql$`)
	create := regexp.MustCompile(`(?i)CREATE TABLE\s+([a-z_]+)\s*\(`)
	banned := regexp.MustCompile(`(?i)\b(function|trigger|grant|extension|policy|schema|public|copy)\b|pg_`)
	got := map[string]string{}
	for _, f := range files {
		base := filepath.Base(f)
		if !name.MatchString(base) {
			t.Errorf("%s: name must match NNN_phase.sql in blocks 010-059", base)
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		sql := string(b)
		if !strings.Contains(sql, "-- +goose Up") {
			t.Errorf("%s: missing -- +goose Up", base)
		}
		if strings.Contains(sql, "-- +goose Down") {
			t.Errorf("%s: must not have -- +goose Down", base)
		}
		if w := banned.FindString(sql); w != "" {
			t.Errorf("%s: contains refused word %q", base, w)
		}
		for _, m := range create.FindAllStringSubmatch(sql, -1) {
			if prev, dup := got[m[1]]; dup {
				t.Errorf("table %s created twice (%s, %s)", m[1], prev, base)
			}
			got[m[1]] = base
		}
	}
	for table, file := range want {
		if got[table] != file {
			t.Errorf("table %s: SCHEMA.md says %s, migrations say %q", table, file, got[table])
		}
	}
	for table, file := range got {
		if _, ok := want[table]; !ok {
			t.Errorf("table %s in %s is not listed in SCHEMA.md", table, file)
		}
	}
}
