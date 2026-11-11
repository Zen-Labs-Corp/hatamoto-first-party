package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/registry"
)

const (
	recheckPerRun = 2
	recheckBudget = 5 * time.Second
)

type recheckRow struct {
	ID    int64          `json:"id"`
	Name  string         `json:"name"`
	Kind  string         `json:"kind"`
	Email string         `json:"email"`
	Entry registry.Entry `json:"entry"`
}

// recheckCron inspects the listed packages that were checked longest ago. A
// file whose hash or signer changed is dropped from the index and its
// developer told; any other failure only counts. Two checks a run, and none
// starts after the time budget.
func recheckCron(sdk.CronEvent) error {
	var rows []recheckRow
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY t.checked_at, t.id), '[]'::jsonb) FROM (`+
		`SELECT l.id, l.name, l.kind, l.entry, l.checked_at, coalesce(a.email, '') AS email FROM listings l `+
		`JOIN accounts a ON a.id = l.account WHERE l.status = 'active' ORDER BY l.checked_at, l.id LIMIT $1) t`, &rows, recheckPerRun)
	if err != nil {
		return err
	}
	start := now()
	dropped := false
	for _, r := range rows {
		if now().Sub(start) > recheckBudget {
			break
		}
		if recheckOne(r) {
			dropped = true
		}
	}
	if dropped {
		return publish()
	}
	return nil
}

// recheckOne inspects one listing and reports whether it was dropped.
func recheckOne(r recheckRow) bool {
	var res sdk.InspectResult
	err := callHost("packages.inspect", sdk.InspectRequest{URL: r.Entry.URL, SignerKey: r.Entry.SignerKey}, &res)
	if err != nil || res.SHA256 == "" {
		// Core answers a failed download with problems and an empty hash,
		// not an error. Only a file that was read may change the listing.
		recheckFailed(r.ID)
		return false
	}
	if reason := changedReason(r.Entry, res); reason != "" {
		n, err := dbExecN(`UPDATE listings SET status = 'dropped', reason = $2, checked_at = now() WHERE id = $1 AND status = 'active'`, r.ID, reason)
		if err != nil || n == 0 {
			return false
		}
		notify(r.Email, fmt.Sprintf("%s %s was removed from the developer listing", r.Name, r.Entry.Version),
			fmt.Sprintf("We checked the release file of %s %s and it is no longer the file that was approved (%s). "+
				"It was removed from the listing. Publish a new version and submit it again.\n", r.Name, r.Entry.Version, reason))
		return true
	}
	if len(res.Problems) > 0 {
		// The approved file, still signed by the listed key, but unreadable now.
		recheckFailed(r.ID)
		return false
	}
	if err := dbExec(`UPDATE listings SET checked_at = now(), check_failures = 0 WHERE id = $1`, r.ID); err != nil {
		logf("warn", "recheck update failed")
	}
	return false
}

func recheckFailed(id int64) {
	if err := dbExec(`UPDATE listings SET checked_at = now(), check_failures = check_failures + 1 WHERE id = $1`, id); err != nil {
		logf("warn", "recheck update failed")
	}
}

// changedReason says what changed in the file, "" when nothing did.
func changedReason(e registry.Entry, res sdk.InspectResult) string {
	if res.SHA256 != e.SHA256 {
		return "the file changed"
	}
	if raw, err := base64.StdEncoding.DecodeString(e.SignerKey); err == nil && len(raw) == ed25519.PublicKeySize {
		if res.SignerKeyID != registry.KeyID(ed25519.PublicKey(raw)) {
			return "the signer changed"
		}
	}
	return ""
}
