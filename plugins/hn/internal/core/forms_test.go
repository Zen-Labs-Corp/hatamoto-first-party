package core

import (
	"reflect"
	"testing"
)

func TestLoginForms(t *testing.T) {
	want := map[string]any{"login_action": "/p/hn/login", "signup_action": "/p/hn/signup", "email_signin_href": "/-/comments/signin", "next": "/submit"}
	if got := LoginForms("/submit"); !reflect.DeepEqual(got, want) {
		t.Errorf("LoginForms(/submit) = %v, want %v", got, want)
	}
	for _, next := range []string{"//evil.example", "https://x.example/", "", `/\evil`} {
		if got := LoginForms(next)["next"]; got != "/" {
			t.Errorf("LoginForms(%q) next = %v, want /", next, got)
		}
	}
}
