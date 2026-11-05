package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	stagePerStep    = 100 // suggestions staged per step
	maxDecorative   = 100 // decorative file names kept in the summary
	suggestPrefix   = "s/"
	decorativePfx   = "d/"
	metaKey         = "meta"
	progressPattern = "%d images checked, %d suggestions, %d decorative (skipped), %d errors"
)

// transformCursor is the checkpoint of the transform phase: the last staged
// buffer key and the counts so far.
type transformCursor struct {
	After  string `json:"after"`
	Staged int    `json:"ok"`
	Failed int    `json:"bad"`
}

// transformStep stages the buffered suggestions, a page per step, then
// reports. It uses only buffer.*, changeset.media.alt and run.* calls.
func transformStep(st sdk.RunStep) error {
	var cur transformCursor
	if st.Cursor != "" {
		if json.Unmarshal([]byte(st.Cursor), &cur) != nil {
			return guest.RunFail(callHost, msgBadCursor)
		}
	}
	keys, _, err := guest.BufferList(callHost, suggestPrefix, cur.After, stagePerStep)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return finishTransform(cur)
	}
	for _, key := range keys {
		if stage(key) {
			cur.Staged++
		} else {
			cur.Failed++
		}
	}
	cur.After = keys[len(keys)-1]
	raw, _ := json.Marshal(cur)
	return guest.RunCheckpoint(callHost, string(raw))
}

// stage stages one buffered suggestion; any problem with it is a count, not a
// failure of the run.
func stage(key string) bool {
	val, found, err := guest.BufferGet(callHost, key)
	if err != nil || !found {
		return false
	}
	var s struct {
		Alt string `json:"alt"`
	}
	if json.Unmarshal(val, &s) != nil || s.Alt == "" {
		return false
	}
	_, err = guest.MediaAlt(callHost, sdk.MediaAlt{MediaID: strings.TrimPrefix(key, suggestPrefix), Alt: s.Alt})
	return err == nil
}

// finishTransform reports the counts: the run page shows the last progress
// message, the summary is stored with the run.
func finishTransform(cur transformCursor) error {
	var m meta
	if raw, found, err := guest.BufferGet(callHost, metaKey); err == nil && found {
		_ = json.Unmarshal(raw, &m)
	}
	m.Suggested = cur.Staged
	m.Errors += cur.Failed

	names := []string{}
	keys, _, err := guest.BufferList(callHost, decorativePfx, "", maxDecorative)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if name, found, err := guest.BufferGet(callHost, key); err == nil && found {
			names = append(names, string(name))
		}
	}

	_ = guest.RunProgress(callHost, m.Checked, m.Checked, fmt.Sprintf(progressPattern, m.Checked, m.Suggested, m.Decorative, m.Errors))
	return guest.RunFinish(callHost, map[string]any{
		"checked": m.Checked, "suggested": m.Suggested, "decorative": m.Decorative,
		"errors": m.Errors, "skipped": m.Skipped, "decorative_files": names,
	})
}
