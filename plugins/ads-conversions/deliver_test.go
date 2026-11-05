package main

import (
	"errors"
	"testing"

	"hatamoto.dev/sdk"
)

func TestClassifyHTTP(t *testing.T) {
	cases := []struct {
		status    int
		truncated bool
		kind      string
	}{
		{200, false, kindSent}, {201, false, kindSent}, {204, false, kindSent},
		{200, true, kindRetry},
		{401, false, kindAuth},
		{429, false, kindRetry}, {500, false, kindRetry}, {503, false, kindRetry}, {599, false, kindRetry},
		{400, false, kindFailed}, {403, false, kindFailed}, {404, false, kindFailed}, {422, false, kindFailed},
		{302, false, kindFailed},
	}
	for _, c := range cases {
		if o := classifyHTTP(c.status, c.truncated); o.Kind != c.kind {
			t.Errorf("classifyHTTP(%d, %v) = %s, want %s", c.status, c.truncated, o.Kind, c.kind)
		}
	}
	if o := classifyHTTP(503, false); o.Code != "503" {
		t.Errorf("code = %q", o.Code)
	}
}

func stubHTTP(t *testing.T, f fetched, err error) *[]request {
	t.Helper()
	var seen []request
	httpDo = func(req request, timeoutMS int) (fetched, error) {
		if timeoutMS != 8000 {
			t.Errorf("timeout = %d", timeoutMS)
		}
		seen = append(seen, req)
		return f, err
	}
	t.Cleanup(func() { httpDo = realHTTPDo })
	return &seen
}

func TestSendTransportAndHostErrorsRetry(t *testing.T) {
	stubHTTP(t, fetched{}, &sdk.HostError{Code: sdk.ErrTimeout, Message: "private detail Jane"})
	f, o := send(request{Method: "GET", URL: "https://x.example"})
	if f.Status != 0 || o.Kind != kindRetry || o.Code != "timeout" || o.Message != "" {
		t.Fatalf("f %+v o %+v", f, o)
	}
	stubHTTP(t, fetched{}, errors.New("boom with details"))
	if _, o := send(request{}); o.Kind != kindRetry || o.Code != "internal" || o.Message != "" {
		t.Fatalf("o = %+v", o)
	}
}

func TestExecuteSkipsClassifierOnTruncatedAndErrors(t *testing.T) {
	boom := func(int, []byte) outcome { t.Fatal("classifier must not run"); return outcome{} }
	stubHTTP(t, fetched{Status: 200, Body: `{"code":0}`, Truncated: true}, nil)
	if o := execute(request{}, boom); o.Kind != kindRetry || o.Reason != "truncated" {
		t.Fatalf("o = %+v", o)
	}
	stubHTTP(t, fetched{}, errors.New("x"))
	if o := execute(request{}, boom); o.Kind != kindRetry {
		t.Fatalf("o = %+v", o)
	}
	stubHTTP(t, fetched{Status: 200, Body: "b"}, nil)
	called := false
	o := execute(request{}, func(s int, b []byte) outcome {
		called = s == 200 && string(b) == "b"
		return outcome{Kind: kindSent}
	})
	if !called || o.Kind != kindSent {
		t.Fatalf("o = %+v", o)
	}
}

func TestHttpDoPassesRequestToHost(t *testing.T) {
	newHost(t)
	callHost = func(fn string, args, out any) error {
		m := args.(map[string]any)
		if fn != "http.fetch" || m["method"] != "POST" || m["url"] != "https://x.example/p" || m["body"] != `{"a":1}` || m["timeout_ms"] != 8000 {
			t.Errorf("call %s %v", fn, m)
		}
		return nil
	}
	if _, err := realHTTPDo(request{Method: "POST", URL: "https://x.example/p", Headers: map[string]string{"A": "b"}, Body: []byte(`{"a":1}`)}, 8000); err != nil {
		t.Fatal(err)
	}
}
