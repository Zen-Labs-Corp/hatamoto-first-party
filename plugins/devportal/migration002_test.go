package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestMigration002Shape(t *testing.T) {
	raw, err := os.ReadFile("migrations/002_submissions.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	if bad := regexp.MustCompile(`(?i)\b(function|trigger|grant|extension|policy|schema|public|copy)\b|pg_`).FindString(sql); bad != "" {
		t.Fatalf("migration contains %q", bad)
	}
	if strings.Contains(sql, "-- +goose Down") || !strings.Contains(sql, "-- +goose Up") {
		t.Fatal("migration needs exactly an Up section")
	}
	for _, table := range []string{"names", "submissions", "listings", "revocations", "mail_queue", "published"} {
		if !strings.Contains(sql, "CREATE TABLE "+table+" (") {
			t.Errorf("migration lacks table %s", table)
		}
	}
	first, err := os.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "CREATE TABLE accounts") {
		t.Error("migration 001 changed")
	}
}
