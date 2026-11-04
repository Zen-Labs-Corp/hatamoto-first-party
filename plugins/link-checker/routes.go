package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const pageSize = 50

func init() {
	guest.OnHTTP(serve)
}

// serve answers the owner-only admin routes. Database trouble (including a
// missing grant) is a 503 JSON answer, never a Go error, so the host does not
// count it as a plugin failure.
func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "GET /links":
		return handleLinks(r)
	case "POST /recheck/{id}":
		return handleChange(r, setRecheck)
	case "POST /ignore/{id}":
		return handleChange(r, toggleIgnore)
	}
	return reply(http.StatusNotFound, map[string]string{"error": "not found"})
}

type linksReply struct {
	Links      []linkRow      `json:"links"`
	NextCursor string         `json:"next_cursor"`
	Counts     map[string]int `json:"counts"`
	LastScan   string         `json:"last_scan"`
	Queue      int            `json:"queue"`
	PagesTotal int            `json:"pages_total"`
}

func handleLinks(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	status := first(r.Query["status"])
	if !slices.Contains([]string{statusBroken, statusRedirected, statusOK, statusIgnored, statusUnknown}, status) {
		status = statusBroken
	}
	cursor, _ := strconv.ParseInt(first(r.Query["cursor"]), 10, 64)
	rows, err := listLinks(status, max(cursor, 0), pageSize+1)
	if err != nil {
		return notReady(err)
	}
	c, err := countLinks()
	if err != nil {
		return notReady(err)
	}
	queue, err := queueSize()
	if err != nil {
		return notReady(err)
	}
	out := linksReply{
		Links: rows,
		Counts: map[string]int{
			"broken": c.Broken, "redirected": c.Redirected, "ok": c.OK,
			"ignored": c.Ignored, "unknown": c.Unknown, "pending": c.Pending,
		},
		Queue:      queue,
		PagesTotal: c.Pages,
	}
	if len(rows) > pageSize {
		out.Links = rows[:pageSize]
		out.NextCursor = strconv.FormatInt(out.Links[pageSize-1].ID, 10)
	}
	if out.Links == nil {
		out.Links = []linkRow{}
	}
	var last string
	if ok, err := kvGet(lastScanKey, &last); err != nil || !ok {
		last = ""
	}
	out.LastScan = last
	return reply(http.StatusOK, out)
}

// handleChange runs recheck or ignore for the link in the path.
func handleChange(r sdk.HTTPRequest, change func(int64) (linkRow, bool, error)) (sdk.HTTPResponse, error) {
	id, err := strconv.ParseInt(r.PathValues["id"], 10, 64)
	if err != nil || id < 1 {
		return reply(http.StatusBadRequest, map[string]string{"error": "id must be a positive number"})
	}
	row, found, err := change(id)
	if err != nil {
		return notReady(err)
	}
	if !found {
		return reply(http.StatusNotFound, map[string]string{"error": "no such link"})
	}
	return reply(http.StatusOK, row)
}

func notReady(err error) (sdk.HTTPResponse, error) {
	logf("warn", "admin route: %v", err)
	return reply(http.StatusServiceUnavailable, map[string]string{"error": "the database is not ready"})
}

func reply(code int, v any) (sdk.HTTPResponse, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return sdk.HTTPResponse{}, err
	}
	return sdk.HTTPResponse{Status: code, Headers: map[string]string{"Content-Type": "application/json"}, Body: string(b)}, nil
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}
