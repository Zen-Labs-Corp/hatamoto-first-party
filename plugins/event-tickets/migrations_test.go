package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Core refuses a migration file when any statement is not a plain
// select/insert/update/delete/with/values or a create/alter/drop of a table,
// index, view, sequence or type (and refuses a few words anywhere). Its fake
// host does not run that check, so this test copies the rule.
var (
	sqlLineComment = regexp.MustCompile(`--[^\n]*`)
	sqlLiteral     = regexp.MustCompile(`'(?:[^']|'')*'`)
	sqlWord        = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

func TestMigrationsPassCoreGuard(t *testing.T) {
	files, err := filepath.Glob("migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found: %v", err)
	}
	dml := map[string]bool{"select": true, "insert": true, "update": true, "delete": true, "with": true, "values": true}
	ddl := map[string]bool{"create": true, "alter": true, "drop": true}
	obj := map[string]bool{"table": true, "index": true, "view": true, "sequence": true, "type": true}
	banned := map[string]bool{"public": true, "copy": true, "set_config": true, "current_setting": true, "information_schema": true}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		body := sqlLiteral.ReplaceAllString(sqlLineComment.ReplaceAllString(string(raw), ""), "''")
		for _, stmt := range strings.Split(body, ";") {
			words := sqlWord.FindAllString(strings.ToLower(stmt), -1)
			if len(words) == 0 {
				continue
			}
			first := words[0]
			switch {
			case dml[first]:
			case ddl[first]:
				i := 1
				for i < len(words) && (words[i] == "unique" || words[i] == "materialized" || words[i] == "concurrently") {
					i++
				}
				if i >= len(words) || !obj[words[i]] {
					t.Errorf("%s: %q: only tables, indexes, views, sequences and types may be created, altered or dropped", f, stmt)
				}
			default:
				t.Errorf("%s: %q statements are refused by core's migration guard: %.60q", f, first, strings.TrimSpace(stmt))
			}
			for _, w := range words {
				if banned[w] || strings.HasPrefix(w, "pg_") {
					t.Errorf("%s: word %q is refused by core's migration guard", f, w)
				}
			}
		}
	}
}
