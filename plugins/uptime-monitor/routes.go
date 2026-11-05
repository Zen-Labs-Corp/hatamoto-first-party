package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

const (
	// checkNowReuse: a check this young is returned as is, so the button
	// cannot be used to hammer a site.
	checkNowReuse = 10 * time.Second
	// checkNowTimeoutMS leaves room in the 10 s route timeout for the
	// database calls around the request.
	checkNowTimeoutMS = 8000
)

// handleRoute answers the owner-only admin routes. Database trouble is a 503
// JSON answer, never a Go error, so the host does not count it as a plugin
// failure.
func handleRoute(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "GET /overview":
		return reply(http.StatusOK, buildOverview())
	case "POST /monitors":
		return handleMonitors(r)
	case "POST /check-now":
		return handleCheckNow(r)
	case "POST /test-alert":
		return handleTestAlert(r)
	}
	return badRequest(http.StatusNotFound, "not found")
}

type monitorsRequest struct {
	Action  string        `json:"action"`
	ID      int64         `json:"id"`
	Monitor *monitorInput `json:"monitor"`
}

func handleMonitors(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	var req monitorsRequest
	if err := decodeStrict(r.Body, &req); err != nil {
		return badRequest(http.StatusBadRequest, err.Error())
	}
	switch req.Action {
	case "create":
		return saveMonitor(req, 0)
	case "update", "delete":
		if req.ID < 1 {
			return badRequest(http.StatusBadRequest, "id must be a positive number")
		}
		if _, found, err := getMonitor(req.ID); err != nil {
			return notReady(err)
		} else if !found {
			return badRequest(http.StatusNotFound, "no such monitor")
		}
		if req.Action == "delete" {
			if err := deleteMonitor(req.ID); err != nil {
				return notReady(err)
			}
			return reply(http.StatusOK, map[string]any{"id": req.ID})
		}
		return saveMonitor(req, req.ID)
	}
	return badRequest(http.StatusBadRequest, "action must be create, update or delete")
}

// saveMonitor validates the monitor of req and creates it (id 0) or replaces
// the editable fields of monitor id.
func saveMonitor(req monitorsRequest, id int64) (sdk.HTTPResponse, error) {
	if req.Monitor == nil {
		return badRequest(http.StatusBadRequest, "monitor is required")
	}
	m, err := validateMonitor(*req.Monitor)
	if err != nil {
		return badRequest(http.StatusBadRequest, err.Error())
	}
	m.ID = id
	existing, err := listMonitors()
	if err != nil {
		return notReady(err)
	}
	if err := checkCapacity(existing, m); err != nil {
		return badRequest(http.StatusBadRequest, err.Error())
	}
	if id == 0 {
		if m.ID, err = insertMonitor(m); err != nil {
			return notReady(err)
		}
	} else if err := updateMonitor(m); err != nil {
		return notReady(err)
	}
	return reply(http.StatusOK, map[string]any{"id": m.ID})
}

func handleCheckNow(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := decodeStrict(r.Body, &req); err != nil {
		return badRequest(http.StatusBadRequest, err.Error())
	}
	m, found, err := getMonitor(req.ID)
	if err != nil {
		return notReady(err)
	}
	if !found {
		return badRequest(http.StatusNotFound, "no such monitor")
	}
	if m.LastCheck != nil && now().Sub(*m.LastCheck) < checkNowReuse {
		return reply(http.StatusOK, viewOf(m))
	}
	res := checkMonitor(m, checkNowTimeoutMS)
	updated, err := applyResult(m, res, loadSettings())
	if err != nil {
		return notReady(err)
	}
	if err := flushOutbox(now().Add(time.Second)); err != nil {
		logf("warn", "uptime-monitor: mail queue failed code=%s", hostErrCode(err))
	}
	return reply(http.StatusOK, viewOf(updated))
}

func handleTestAlert(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	if strings.TrimSpace(r.Body) != "" {
		var none struct{}
		if err := decodeStrict(r.Body, &none); err != nil {
			return badRequest(http.StatusBadRequest, err.Error())
		}
	}
	st := loadSettings()
	if len(st.AlertEmails) == 0 {
		return badRequest(http.StatusBadRequest, "add an alert email in Settings first")
	}
	sent, reason := sendTestAlert(st.AlertEmails)
	if reason != "" {
		return reply(http.StatusOK, map[string]any{"sent": 0, "reason": reason})
	}
	return reply(http.StatusOK, map[string]any{"sent": sent})
}

func notReady(err error) (sdk.HTTPResponse, error) {
	logf("warn", "uptime-monitor: admin route failed code=%s", hostErrCode(err))
	return badRequest(http.StatusServiceUnavailable, "the database is not ready")
}

func badRequest(code int, msg string) (sdk.HTTPResponse, error) {
	return reply(code, map[string]string{"error": msg})
}

func reply(code int, v any) (sdk.HTTPResponse, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return sdk.HTTPResponse{}, err
	}
	return sdk.HTTPResponse{Status: code, Headers: map[string]string{"Content-Type": "application/json"}, Body: string(b)}, nil
}
