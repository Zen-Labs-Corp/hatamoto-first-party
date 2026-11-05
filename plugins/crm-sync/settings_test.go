package main

import (
	"strings"
	"testing"
)

func TestTruthy(t *testing.T) {
	for _, v := range []any{true, 1.0, "yes", "On", " TRUE ", "y", "checked", "1", []any{"yes"}, []any{"", "newsletter"}} {
		if !truthy(v) {
			t.Errorf("%#v should be truthy", v)
		}
	}
	for _, v := range []any{false, 0.0, 2.0, "no", "", "  ", []any{}, []any{""}, []any{2.0}, nil} {
		if truthy(v) {
			t.Errorf("%#v should not be truthy", v)
		}
	}
}

func TestMyDomainValidation(t *testing.T) {
	ok := map[string]string{
		"acme.my.salesforce.com":              "acme.my.salesforce.com",
		"acme--dev.sandbox.my.salesforce.com": "acme--dev.sandbox.my.salesforce.com",
		"https://ACME.my.salesforce.com/":     "acme.my.salesforce.com",
	}
	for in, want := range ok {
		if got := normMyDomain(in); got != want {
			t.Errorf("normMyDomain(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"evil.com", "acme.my.salesforce.com.evil.com", "my.salesforce.com", ".my.salesforce.com", "acme.my.salesforce.com:8443", "user@acme.my.salesforce.com", "", "acme.salesforce.com", "acme.my.salesforce.com/x", "a b.my.salesforce.com", "a..b.my.salesforce.com"} {
		if got := normMyDomain(in); got != "" {
			t.Errorf("normMyDomain(%q) = %q, want refusal", in, got)
		}
	}
}

func TestCompanyDomainValidation(t *testing.T) {
	for _, in := range []string{"acme", "acme.pipedrive.com", "https://Acme.pipedrive.com/", "  ACME "} {
		if got := normCompany(in); got != "acme" {
			t.Errorf("normCompany(%q) = %q", in, got)
		}
	}
	for _, in := range []string{"a.b.pipedrive.com", "acme/x", "-acme", "acme:80", "api.pipedrive.com.evil.com", "", "acme@x", "a.b"} {
		if got := normCompany(in); got != "" {
			t.Errorf("normCompany(%q) = %q, want refusal", in, got)
		}
	}
}

func TestConfigured(t *testing.T) {
	cases := []struct {
		name string
		c    cfg
		want bool
	}{
		{"none", cfg{}, false},
		{"unknown provider", cfg{Provider: "zoho", AccessToken: "x"}, false},
		{"hubspot", cfg{Provider: "hubspot", AccessToken: "tok"}, true},
		{"hubspot blank token", cfg{Provider: "hubspot", AccessToken: "  \n"}, false},
		{"pipedrive", cfg{Provider: "pipedrive", APIToken: "tok", CompanyDomain: "acme.pipedrive.com"}, true},
		{"pipedrive blank token", cfg{Provider: "pipedrive", APIToken: " ", CompanyDomain: "acme"}, false},
		{"pipedrive bad domain", cfg{Provider: "pipedrive", APIToken: "tok", CompanyDomain: "a/b"}, false},
		{"salesforce", cfg{Provider: "salesforce", ClientID: "id", ClientSecret: "s", MyDomain: "acme.my.salesforce.com"}, true},
		{"salesforce blank secret", cfg{Provider: "salesforce", ClientID: "id", ClientSecret: "  ", MyDomain: "acme.my.salesforce.com"}, false},
		{"salesforce blank id", cfg{Provider: "salesforce", ClientID: " ", ClientSecret: "s", MyDomain: "acme.my.salesforce.com"}, false},
		{"salesforce bad domain", cfg{Provider: "salesforce", ClientID: "id", ClientSecret: "s", MyDomain: "evil.com"}, false},
	}
	for _, tc := range cases {
		if got := tc.c.configured(); got != tc.want {
			t.Errorf("%s: configured = %v", tc.name, got)
		}
	}
}

func TestSettingsDefaults(t *testing.T) {
	var c cfg
	if !c.addNote() || !c.createLead() || c.leadSource() != "Website" || c.object() != "lead" || c.defaultCompany() != "Website visitor" || c.lifecycle() != "lead" || c.emailField() != "email" {
		t.Fatalf("defaults %+v", c)
	}
	no := false
	c = cfg{AddNote: &no, CreateLead: &no, Object: "contact", Forms: []string{"a"}}
	if c.addNote() || c.createLead() || c.object() != "contact" || !c.watched("a") || c.watched("b") || c.watched("") {
		t.Fatalf("explicit %+v", c)
	}
	if (cfg{Object: "Contact"}).object() != "lead" {
		t.Fatal("object must match exactly")
	}
}

func TestOwnerValidation(t *testing.T) {
	cases := []struct {
		provider, id, want string
	}{
		{"hubspot", "12345", "12345"},
		{"pipedrive", " 77 ", "77"},
		{"hubspot", "12a", ""},
		{"pipedrive", "", ""},
		{"hubspot", strings.Repeat("1", 21), ""},
		{"salesforce", "005000000000001AAA", "005000000000001AAA"},
		{"salesforce", "005000000000001", "005000000000001"},
		{"salesforce", "12345", ""},
		{"salesforce", "005000000000001AAA1", ""},
		{"salesforce", "00500000000000-AAA", ""},
		{"", "123", ""},
	}
	for _, tc := range cases {
		if got := (cfg{Provider: tc.provider, OwnerID: tc.id}).owner(); got != tc.want {
			t.Errorf("owner(%s, %q) = %q, want %q", tc.provider, tc.id, got, tc.want)
		}
	}
}

func TestLongEmailIsNotCut(t *testing.T) {
	long := strings.Repeat("a", 200) + "@example.com"
	got := fieldValue(map[string]any{"email": " " + long + " "}, "email")
	if got != long {
		t.Fatalf("email was changed: %d chars", len(got))
	}
	if !validEmail(got) {
		t.Fatal("a 212 character address is within the 254 limit")
	}
	tooLong := fieldValue(map[string]any{"email": strings.Repeat("a", 250) + "@example.com"}, "email")
	if len(tooLong) != 262 || validEmail(tooLong) {
		t.Fatal("an address over 254 characters must reach validEmail whole and be refused")
	}
	if fieldValue(map[string]any{"email": 5.0}, "email") != "" {
		t.Fatal("non-string value")
	}
}
