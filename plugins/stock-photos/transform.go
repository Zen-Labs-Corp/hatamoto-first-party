package main

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	listLimit      = 100
	maxSkipped     = 50   // entries named in the run summary
	maxSkipKeys    = 1000 // skip keys counted at most
	maxCarriedSkip = 20   // attach rejections carried in the checkpoint
)

// stopKey is the skip entry the fetch phase writes when the search stopped early.
const stopKey = "skip/search:stopped"

type skipped struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

// transformCursor is the transform checkpoint, used only when the step runs
// out of time: After is the last item key staged.
type transformCursor struct {
	After    string    `json:"after"`
	Staged   int       `json:"staged"`
	Existing int       `json:"existing,omitempty"` // already in the library: core attach is create-only
	Rejected []skipped `json:"rejected,omitempty"`
	Dropped  int       `json:"dropped,omitempty"` // rejections beyond maxCarriedSkip
}

// transformStep stages every buffered photo as changeset media and finishes
// with a summary. It makes no network call.
func transformStep(s sdk.RunStep) (sdk.RunStepResult, error) {
	var cur transformCursor
	if s.Cursor != "" {
		if err := json.Unmarshal([]byte(s.Cursor), &cur); err != nil {
			return failRun("The run state could not be read. Start the run again.")
		}
	}
	start := time.Now()
	for {
		keys, next, err := guest.BufferList(callHost, "item/", cur.After, listLimit)
		if err != nil {
			return failFromError(err)
		}
		for _, k := range keys {
			if time.Since(start) >= stepBudget {
				return checkpointTransform(cur)
			}
			if err := stageItem(k, &cur); err != nil {
				return failFromError(err)
			}
			cur.After = k
		}
		if next == "" || len(keys) == 0 {
			break
		}
	}

	list, total, err := skippedEntries()
	if err != nil {
		return failFromError(err)
	}
	list = append(cur.Rejected, list...)
	total += len(cur.Rejected) + cur.Dropped
	if len(list) > maxSkipped {
		list = list[:maxSkipped]
	}
	if list == nil {
		list = []skipped{}
	}
	_ = guest.RunProgress(callHost, cur.Staged, cur.Staged, "Staged "+strconv.Itoa(cur.Staged)+" photos")
	summary := map[string]any{"staged": cur.Staged, "already_in_library": cur.Existing, "skipped": list, "skipped_total": total}
	if err := guest.RunFinish(callHost, summary); err != nil {
		return failFromError(err)
	}
	return sdk.RunStepResult{}, nil
}

func checkpointTransform(cur transformCursor) (sdk.RunStepResult, error) {
	next, _ := json.Marshal(cur)
	if err := guest.RunCheckpoint(callHost, string(next)); err != nil {
		return failFromError(err)
	}
	return sdk.RunStepResult{}, nil
}

// stageItem attaches one buffered photo. Only a problem with that photo is
// tolerated (recorded as skipped); anything else is returned.
func stageItem(bufKey string, cur *transformCursor) error {
	val, found, err := guest.BufferGet(callHost, bufKey)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	var it bufferItem
	if json.Unmarshal(val, &it) != nil || it.SourceKey == "" || it.Handle == "" {
		return nil
	}
	res, err := guest.MediaAttach(callHost, sdk.MediaAttach{SourceKey: it.SourceKey, Handle: it.Handle, Alt: it.Alt, Caption: it.Caption, Credit: it.Credit})
	var he *sdk.HostError
	if errors.As(err, &he) && (he.Code == sdk.ErrInvalidArgument || he.Code == sdk.ErrNotOwned) {
		if len(cur.Rejected) < maxCarriedSkip {
			cur.Rejected = append(cur.Rejected, skipped{it.SourceKey, "not staged: " + he.Code})
		} else {
			cur.Dropped++
		}
		return nil
	}
	if err != nil {
		return err
	}
	if res.Created {
		cur.Staged++
	} else {
		cur.Existing++
	}
	return nil
}

// skippedEntries reads what the fetch phase left in skip/: the first
// maxSkipped entries and the total.
func skippedEntries() (list []skipped, total int, err error) {
	// The reason the search stopped always comes first, ahead of the capped list.
	if val, found, err := guest.BufferGet(callHost, stopKey); err != nil {
		return nil, 0, err
	} else if found {
		var v struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(val, &v)
		list = append(list, skipped{strings.TrimPrefix(stopKey, "skip/"), cleanText(v.Reason, 200)})
	}
	after := ""
	for total < maxSkipKeys {
		keys, next, err := guest.BufferList(callHost, "skip/", after, listLimit)
		if err != nil {
			return nil, 0, err
		}
		for _, k := range keys {
			total++
			if k == stopKey || len(list) >= maxSkipped {
				continue
			}
			val, found, err := guest.BufferGet(callHost, k)
			if err != nil {
				return nil, 0, err
			}
			var v struct {
				Reason string `json:"reason"`
			}
			if found {
				_ = json.Unmarshal(val, &v)
			}
			list = append(list, skipped{strings.TrimPrefix(k, "skip/"), cleanText(v.Reason, 200)})
		}
		if next == "" || len(keys) == 0 {
			break
		}
		after = next
	}
	return list, total, nil
}
