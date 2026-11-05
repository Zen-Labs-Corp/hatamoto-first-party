package main

import (
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// siteTick makes monitor 1 the site and runs one tick.
func siteTick(h *tickHost) {
	h.monitors[1].IsSite = true
	h.monitors[1].IntervalS = 60
	h.monitors[1].NextCheck = h.clock.Add(-time.Second)
	checkTick()
}

func TestHeartbeatAfterGoodSiteCheck(t *testing.T) {
	h := newTickHost(t, 3)
	siteTick(h)
	if h.heartbeats() != 1 {
		t.Fatalf("heartbeats %d", h.heartbeats())
	}
	last := h.fetched[len(h.fetched)-1]
	if last.url != heartbeatSecret || last.timeoutMS > 3000 {
		t.Fatalf("%+v", last)
	}
	// The ping follows the other checks of the tick.
	if len(h.fetched) != 4 {
		t.Fatalf("fetches %d", len(h.fetched))
	}
}

func TestHeartbeatAfterSlowSiteCheck(t *testing.T) {
	h := newTickHost(t, 1)
	h.monitors[1].MaxMS = 100
	h.takes = 400 * time.Millisecond
	siteTick(h)
	if h.monitors[1].Status != statusSlow || h.heartbeats() != 1 {
		t.Fatalf("status %s heartbeats %d", h.monitors[1].Status, h.heartbeats())
	}
}

func TestNoHeartbeatForOtherMonitorsOrFailures(t *testing.T) {
	h := newTickHost(t, 2)
	checkTick() // nobody is the site
	if h.heartbeats() != 0 {
		t.Fatal("heartbeat for a monitor that is not the site")
	}
	h = newTickHost(t, 2)
	h.status["http://m1.example.org/"] = 500
	siteTick(h)
	if h.heartbeats() != 0 {
		t.Fatal("heartbeat after a failed check")
	}
	h = newTickHost(t, 1)
	h.st.HeartbeatURL = ""
	siteTick(h)
	if len(h.fetched) != 1 {
		t.Fatalf("no URL, no ping: %v", h.fetched)
	}
}

func TestNoHeartbeatAfterConfigOrSkippedCheck(t *testing.T) {
	h := newTickHost(t, 1)
	newFakeHost(t, func(c call, out any) error {
		if c.Fn == "http.fetch" && c.Args["url"] != heartbeatSecret {
			return hostErr(sdk.ErrBlocked, "fetch blocked")
		}
		return h.reply(c, out)
	})
	siteTick(h)
	if h.heartbeats() != 0 {
		t.Fatal("heartbeat after a config result")
	}
}

func TestHeartbeatAtMostOncePerMinute(t *testing.T) {
	h := newTickHost(t, 1)
	siteTick(h)
	h.clock = h.clock.Add(30 * time.Second)
	siteTick(h)
	if h.heartbeats() != 1 {
		t.Fatalf("twice within a minute: %d", h.heartbeats())
	}
	h.clock = h.clock.Add(31 * time.Second)
	siteTick(h)
	if h.heartbeats() != 2 {
		t.Fatalf("after a minute: %d", h.heartbeats())
	}
}

func TestHeartbeatNeedsTimeInTheBudget(t *testing.T) {
	h := newTickHost(t, 1)
	h.takes = 7 * time.Second // the check leaves 1 s, minus the margin
	siteTick(h)
	if h.heartbeats() != 0 {
		t.Fatal("heartbeat started without time left")
	}
	h = newTickHost(t, 1)
	h.takes = 5 * time.Second // 3 s left: 2.5 s timeout
	siteTick(h)
	if h.heartbeats() != 1 || h.fetched[1].timeoutMS != 2500 {
		t.Fatalf("heartbeats %d fetches %v", h.heartbeats(), h.fetched)
	}
}

func TestHeartbeatFailureIsLoggedWithoutTheURL(t *testing.T) {
	for name, err := range map[string]error{
		"host error":  hostErr(sdk.ErrTimeout, "call timed out fetching "+heartbeatSecret),
		"plain error": errString("dial " + heartbeatSecret + ": refused"),
		"blocked":     hostErr(sdk.ErrBlocked, "fetch blocked: "+heartbeatSecret),
	} {
		h := newTickHost(t, 1)
		h.hbErr = err
		var f *fakeHost
		f = newFakeHost(t, h.reply)
		now = func() time.Time { return h.clock }
		siteTick(h)
		if h.heartbeats() != 1 {
			t.Fatalf("%s: heartbeats %d", name, h.heartbeats())
		}
		var failed int
		for _, l := range f.logs() {
			if strings.Contains(l, "heartbeat failed") {
				failed++
			}
		}
		if failed != 1 {
			t.Fatalf("%s: logs %v", name, f.logs())
		}
		assertNoSecret(t, f)
		if h.monitors[1].Status != statusUp {
			t.Fatalf("%s: a failed heartbeat changed the monitor: %+v", name, h.monitors[1])
		}
	}
}

func TestHeartbeatHTTPErrorIsLogged(t *testing.T) {
	h := newTickHost(t, 1)
	var f *fakeHost
	f = newFakeHost(t, func(c call, out any) error {
		if c.Fn == "http.fetch" && c.Args["url"] == heartbeatSecret {
			h.fetched = append(h.fetched, fetchRecord{url: heartbeatSecret})
			return jsonInto(out, `{"status":404,"body":"gone `+heartbeatSecret+`"}`)
		}
		return h.reply(c, out)
	})
	now = func() time.Time { return h.clock }
	siteTick(h)
	if logs := strings.Join(f.logs(), "\n"); !strings.Contains(logs, "heartbeat failed code=http_404") {
		t.Fatalf("logs %q", logs)
	}
	assertNoSecret(t, f)
}

func TestNoLogOrMailEverHoldsTheHeartbeatURL(t *testing.T) {
	h := newTickHost(t, 3)
	h.st.AlertEmails = []string{"owner@example.org"}
	h.status["http://m2.example.org/"] = 503
	f := newFakeHost(t, h.reply)
	now = func() time.Time { return h.clock }
	siteTick(h)
	h.clock = h.clock.Add(2 * time.Minute)
	siteTick(h)
	assertNoSecret(t, f)
}

func assertNoSecret(t *testing.T, f *fakeHost) {
	t.Helper()
	for _, c := range f.calls {
		if c.Fn == "http.fetch" && c.Args["url"] == heartbeatSecret {
			continue
		}
		if raw := strings.Join([]string{c.Fn, c.sql()}, " "); strings.Contains(raw, "s3cr3t") {
			t.Fatalf("secret in %s", raw)
		}
		for _, v := range c.Args {
			if s, ok := v.(string); ok && strings.Contains(s, "s3cr3t") {
				t.Fatalf("secret in a %s argument: %q", c.Fn, s)
			}
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
