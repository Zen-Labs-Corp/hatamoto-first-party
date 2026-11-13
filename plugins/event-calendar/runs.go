package main

import (
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// The plugin's runs. Edit dates reads one event with entities.get and stages
// one changeset item in the transform phase; the import runs read calendars in
// the fetch phase (import_run.go). Nothing is written until the owner reviews
// the changeset, except what a standing approval lets a scheduled run publish.

const (
	// runEditDates is the run the admin starts: core's admin can start only the
	// first declared run, so duplicating an event and the manual import are
	// actions of it.
	runEditDates = "edit_dates"

	// keyPrefix marks a source key this plugin made itself. The key of an
	// object is not readable from the host, so a run that creates an event
	// writes its key into the event's source_uid and a later run finds it there.
	keyPrefix = "dup-"

	maxFailReason = 500
)

func init() { guest.OnRunStep(onStep) }

// onStep routes a run step. A panic or an unknown run fails the run with a
// plain reason; an unknown phase does nothing.
func onStep(s sdk.RunStep) (res sdk.RunStepResult, err error) {
	defer func() {
		if recover() != nil {
			res, err = failRun("The run stopped unexpectedly. Nothing was staged.")
		}
	}()
	switch s.Phase {
	case sdk.RunPhaseFetch:
		return fetchPhase(s)
	case sdk.RunPhaseTransform:
		info, err := guest.RunInfo(callHost)
		if err != nil {
			return failRun("The run inputs could not be read.")
		}
		var p map[string]any
		_ = json.Unmarshal(info.Params, &p)
		switch {
		case info.Name == runImportScheduled, info.Name == runEditDates && paramString(p, "action") == actionImportURL:
			return importTransformStep()
		case info.Name == runEditDates && paramString(p, "action") == actionApprove:
			return approveSubmission(p)
		case info.Name == runEditDates:
			return editDates(info.RunID, p)
		}
		return failRun("Unknown run.")
	}
	return sdk.RunStepResult{}, nil
}

// failRun fails the run with a one-line reason. The step itself then ends
// normally: an error returned from a step carries no reason the owner can read.
func failRun(reason string) (sdk.RunStepResult, error) {
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) > maxFailReason {
		reason = capBytes(reason, maxFailReason)
	}
	return sdk.RunStepResult{}, guest.RunFail(callHost, reason)
}

// dupKey is the source key of a copy made by run runID. It carries the copy's
// slug, so core's Duplicate (which copies source_uid but not the slug) does not
// hand the key to another event.
func dupKey(runID, slug string) string { return keyPrefix + runID + "/" + slug }

// ownedKey is the source key this plugin made for the event with this slug,
// read from the event's source_uid; false for any other event, whose key is
// not known, and for a key made for another slug.
func ownedKey(sourceUID, slug string) (string, bool) {
	uid := strings.TrimSpace(sourceUID)
	if !strings.HasPrefix(uid, keyPrefix) || slug == "" {
		return "", false
	}
	i := strings.LastIndex(uid, "/")
	if i <= len(keyPrefix) || uid[i+1:] != slug {
		return "", false
	}
	return uid, true
}

const notOwnedHint = "This event was not created by this plugin, so its dates cannot be changed here. " +
	"Hand it back in the editor (Who owns this page) to change single dates here, or edit the dates in the event itself."

// runEvent is an event as a run reads it: the whole attributes object, kept as
// received so it can be sent back unchanged.
type runEvent struct {
	ID       string                     `json:"id"`
	Slug     string                     `json:"slug"`
	Name     string                     `json:"name"`
	Locale   string                     `json:"locale"`
	Status   string                     `json:"status"`
	Schema   string                     `json:"schema_slug"`
	ParentID *string                    `json:"parent_id"`
	Attrs    map[string]json.RawMessage `json:"attributes"`
}

// loadRunEvent reads the published root-level event with the given slug. The
// error text is safe to show to the owner.
func loadRunEvent(slug string) (runEvent, error) {
	if !fileSlugRe.MatchString(slug) {
		return runEvent{}, errors.New("The event input must be the slug of an event.")
	}
	s := loadSettings()
	for _, schema := range s.slugs() {
		var ev runEvent
		err := callHost(sdk.FnEntitiesGet, map[string]any{"slug": slug, "parent_id": nil, "schema_slug": schema}, &ev)
		switch {
		case isNotFound(err):
			continue
		case err != nil:
			return runEvent{}, errors.New("The event could not be read. Try again.")
		case ev.ID == "" || ev.Status != "published":
			continue
		case ev.ParentID != nil && *ev.ParentID != "":
			return runEvent{}, errors.New("Events with a parent page are not supported.")
		}
		return ev, nil
	}
	return runEvent{}, errors.New("No published event has that slug.")
}

// attrText is a string attribute of a run event, "" when absent.
func (ev runEvent) attrText(key string) string {
	var s string
	_ = json.Unmarshal(ev.Attrs[key], &s)
	return strings.TrimSpace(s)
}

// attrList is a list-of-strings attribute of a run event.
func (ev runEvent) attrList(key string) []string {
	var l []string
	_ = json.Unmarshal(ev.Attrs[key], &l)
	return l
}

// upsert returns the staged upsert of ev under key with the attributes
// replaced by attrs (which already hold every attribute to keep).
func (ev runEvent) upsert(key string, attrs map[string]json.RawMessage) (sdk.EntityUpsert, error) {
	raw, err := json.Marshal(attrs)
	if err != nil {
		return sdk.EntityUpsert{}, errors.New("The event could not be encoded.")
	}
	return sdk.EntityUpsert{
		SourceKey: key, Schema: ev.Schema, Slug: ev.Slug, Locale: ev.Locale,
		Name: ev.Name, Attributes: raw,
	}, nil
}

// cloneAttrs copies the attribute map so a run changes only what it means to.
func cloneAttrs(m map[string]json.RawMessage) map[string]json.RawMessage {
	c := make(map[string]json.RawMessage, len(m)+2)
	for k, v := range m {
		c[k] = v
	}
	return c
}

// setList stores a list of strings, or removes the attribute when empty.
func setList(m map[string]json.RawMessage, key string, list []string) {
	if len(list) == 0 {
		delete(m, key)
		return
	}
	m[key], _ = json.Marshal(list)
}

// paramString reads a run input as trimmed text; a number input may arrive as
// a JSON number.
func paramString(p map[string]any, key string) string {
	switch v := p[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

var clockRe = regexp.MustCompile(`^\d{1,2}:\d{2}$`)

// dateTimeInput reads a start or end input of a changed date: a time of day
// on that date ("18:30"), or a full "YYYY-MM-DDTHH:MM".
func dateTimeInput(in, date string) (string, bool) {
	if clockRe.MatchString(in) {
		if len(in) == 4 {
			in = "0" + in
		}
		in = date + "T" + in
	}
	if _, err := time.Parse("2006-01-02T15:04", in); err != nil {
		return "", false
	}
	return in, true
}

// editDates stages one event with a date skipped, changed or restored, or a
// copy of it (action duplicate).
func editDates(runID string, p map[string]any) (sdk.RunStepResult, error) {
	action := strings.ToLower(paramString(p, "action"))
	date := paramString(p, "date")
	if action == "duplicate" {
		return duplicateEvent(runID, p)
	}
	if action != "skip" && action != "change" && action != "restore" {
		return failRun("The action must be skip, change, restore or duplicate.")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return failRun("The date must look like 2026-03-03.")
	}
	var start, end string
	if action == "change" {
		var ok bool
		if start, ok = dateTimeInput(paramString(p, "start_at"), date); !ok {
			return failRun("A changed date needs a start time like 18:30 or 2026-03-03T18:30.")
		}
		if e := paramString(p, "end_at"); e != "" {
			if end, ok = dateTimeInput(e, date); !ok {
				return failRun("The end time must look like 20:00 or 2026-03-03T20:00.")
			}
		}
	}
	ev, err := loadRunEvent(paramString(p, "event"))
	if err != nil {
		return failRun(err.Error())
	}
	key, ok := ownedKey(ev.attrText("source_uid"), ev.Slug)
	if !ok {
		return failRun(notOwnedHint)
	}

	skips := without(ev.attrList("repeat_skip_dates"), date)
	var overrides []string
	for _, line := range ev.attrList("repeat_overrides") {
		if day, _, _ := strings.Cut(line, "|"); strings.TrimSpace(day) != date {
			overrides = append(overrides, line)
		}
	}
	switch action {
	case "skip":
		skips = append(skips, date)
	case "change":
		line := date + "|" + start
		if end != "" {
			line += "|" + end
		}
		overrides = append(overrides, line)
	}
	sort.Strings(skips)
	sort.Strings(overrides)

	attrs := cloneAttrs(ev.Attrs)
	setList(attrs, "repeat_skip_dates", skips)
	setList(attrs, "repeat_overrides", overrides)
	up, err := ev.upsert(key, attrs)
	if err != nil {
		return failRun(err.Error())
	}
	res, err := guest.EntityUpsert(callHost, up)
	if err != nil {
		if guest.NotOwned(err) {
			return failRun(notOwnedHint)
		}
		return failRun("The change could not be staged. Try again.")
	}
	// source_uid is editable, so the key may not map to this event. The run
	// cannot see which object a key maps to before staging, but the host says
	// whether it did: created means no earlier run made this key, so the item
	// would be a second event. Failing the run discards the changeset.
	if res.Created {
		return failRun(notOwnedHint)
	}
	return sdk.RunStepResult{}, guest.RunFinish(callHost, map[string]any{"staged": 1, "event": ev.Slug, "date": date, "action": action})
}

// without returns list minus every entry equal to v, in order.
func without(list []string, v string) []string {
	var out []string
	for _, s := range list {
		if strings.TrimSpace(s) != v {
			out = append(out, s)
		}
	}
	return out
}
