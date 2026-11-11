package main

import "testing"

func TestNameRefusal(t *testing.T) {
	for name, want := range map[string]string{
		"seo": "reserved", "hello": "reserved", "gtm": "reserved",
		"stats": "reserved", "devportal": "reserved", "searchconsole": "reserved", "kaizen": "reserved",
		"Acme": "invalid", "a": "invalid", "1abc": "invalid", "acme_seo": "invalid", "": "invalid",
		"acme-seo": "",
	} {
		if got := nameRefusal(name); got != want {
			t.Errorf("nameRefusal(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestOwnsOrFree(t *testing.T) {
	me, other := int64(1), int64(2)
	if !ownsOrFree(nil, me) || !ownsOrFree(&me, me) || ownsOrFree(&other, me) {
		t.Error("ownsOrFree matrix")
	}
}

func TestPolicyFilesAreNames(t *testing.T) {
	for file, raw := range map[string]string{"reserved": reservedRaw, "first-party": firstPartyRaw} {
		set := nameSet(raw)
		if len(set) == 0 {
			t.Errorf("%s names empty", file)
		}
		for n := range set {
			if !namePattern.MatchString(n) {
				t.Errorf("%s: %q is not a valid name", file, n)
			}
		}
	}
}

func TestCategoryLists(t *testing.T) {
	if len(Categories) != 10 || len(SiteTypes) != 6 {
		t.Error("category lists changed")
	}
}
