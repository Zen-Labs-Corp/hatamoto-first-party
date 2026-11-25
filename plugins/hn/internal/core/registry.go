package core

import (
	"fmt"
	"slices"
	"strings"

	"hatamoto.dev/sdk"
)

// Phases is the fixed registration order of the plugin's phases.
var Phases = []string{"accounts", "stories", "comments", "moderation", "parity", "polish"}

// CronNames equals the cron names in plugin.json.
var CronNames = []string{"rank", "comment_rank", "reconcile", "sweep", "search"}

// CommentKind selects a comment action.
type CommentKind int

// The comment actions the host sends.
const (
	OnCreated CommentKind = iota
	OnApproved
	OnSpam
	OnDeleted
	OnUpdated
	commentKinds
)

// GateKind names a check a phase may veto.
type GateKind int

// The gates: moderation registers them; stories, accounts and moderation's own flag route run them.
// GateLogin runs before a password is checked (login and password change); Target is the
// lower-case username when it is a valid name, else empty.
const (
	GateSubmit GateKind = iota
	GateVote
	GateSignup
	GateFlag
	GateLogin
)

// EventKind names an in-process story event.
type EventKind int

// The story events: stories publishes them, parity indexes them.
const (
	StorySaved EventKind = iota
	StoryDeleted
)

// GateReq is what a gate decides on.
type GateReq struct {
	Kind   GateKind
	User   *User
	Req    *Request
	Target string
	Domain string
}

// Item is a story or comment row, for row actions.
type Item struct {
	Kind     string // story or comment
	ID       string // uuid
	ItemNo   int64
	AuthorID int64
	Dead     bool
}

// Action is a link or POST button on a story row or an item head.
type Action struct {
	Label, Href, Class string
	Post               bool
}

// StoryEvent is published when a story is saved or deleted.
type StoryEvent struct {
	Kind        EventKind
	EntityID    string
	ItemNo      int64
	Title, Body string
}

// RouteInfo is a registered route and the phase that registered it.
type RouteInfo struct{ Key, Phase string }

type routeEntry struct {
	phase string
	h     func(*Request) (sdk.HTTPResponse, error)
}

type pageEntry struct {
	path, prefix string
	all          bool
	h            func(*Page) error
}

type cronEntry struct {
	phase string
	h     func(sdk.CronEvent) error
}

// Registry collects the handlers of the six phases in phase order. The guest
// package accepts one handler per export and refuses a duplicate, so plugin.go
// installs one dispatcher per export, built from the registry.
type Registry struct {
	phase string
	next  int

	routes    map[string]routeEntry
	routeKeys []string
	pages     []pageEntry
	comments  [commentKinds][]func(sdk.CommentEvent) error
	checks    []func(sdk.CommentCheck) (sdk.CommentCheck, error)
	drafts    []func(sdk.CommentDraft) (DraftAnswer, error)
	crons     map[string]cronEntry
	gates     map[GateKind][]func(*GateReq) error
	rowActs   []func(*User, Item) []Action
	subs      map[EventKind][]func(StoryEvent)
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		routes: map[string]routeEntry{},
		crons:  map[string]cronEntry{},
		gates:  map[GateKind][]func(*GateReq) error{},
		subs:   map[EventKind][]func(StoryEvent){},
	}
}

// Register runs one phase's Register function. Phases must come in the order of Phases.
func (r *Registry) Register(phase string, fn func(*Registry)) {
	if r.next >= len(Phases) || Phases[r.next] != phase {
		panic(fmt.Sprintf("core: phase %s out of order", phase))
	}
	r.phase = phase
	fn(r)
	r.next++
}

// Route registers a route as "METHOD /pattern", exactly as in plugin.json. A
// duplicate panics.
func (r *Registry) Route(key string, h func(*Request) (sdk.HTTPResponse, error)) {
	if strings.Count(key, " ") != 1 {
		panic(fmt.Sprintf("core: route %q is not \"METHOD /pattern\"", key))
	}
	if prev, dup := r.routes[key]; dup {
		panic(fmt.Sprintf("core: duplicate route %q (%s and %s)", key, prev.phase, r.phase))
	}
	r.routes[key] = routeEntry{r.phase, h}
	r.routeKeys = append(r.routeKeys, key)
}

// Routes lists the registered routes in registration order.
func (r *Registry) Routes() []RouteInfo {
	out := make([]RouteInfo, 0, len(r.routeKeys))
	for _, k := range r.routeKeys {
		out = append(out, RouteInfo{k, r.routes[k].phase})
	}
	return out
}

// Page registers a render.page handler for one exact path.
func (r *Registry) Page(path string, h func(*Page) error) {
	r.pages = append(r.pages, pageEntry{path: path, h: h})
}

// PagePrefix registers a render.page handler for every path below prefix.
func (r *Registry) PagePrefix(prefix string, h func(*Page) error) {
	r.pages = append(r.pages, pageEntry{prefix: prefix, h: h})
}

// PageAll registers a render.page handler for every page the host sends.
func (r *Registry) PageAll(h func(*Page) error) {
	r.pages = append(r.pages, pageEntry{all: true, h: h})
}

// Comment registers a handler for a comment action.
func (r *Registry) Comment(k CommentKind, h func(sdk.CommentEvent) error) {
	r.comments[k] = append(r.comments[k], h)
}

// Check registers a comment.check handler; the scores add up across phases.
func (r *Registry) Check(h func(sdk.CommentCheck) (sdk.CommentCheck, error)) {
	r.checks = append(r.checks, h)
}

// DraftAnswer is what a comment.draft handler decides: Name is the canonical
// author name it vouches for, Reject a public refusal text. Both empty is a
// pass-through, which is not an answer.
type DraftAnswer struct{ Name, Reject string }

// Draft registers a comment.draft handler. The draft is accepted only when a
// handler returns a Name and none rejects (see DraftComment).
func (r *Registry) Draft(h func(sdk.CommentDraft) (DraftAnswer, error)) {
	r.drafts = append(r.drafts, h)
}

// Cron registers the handler of a cron job. The name must be in CronNames and
// have one owner: a duplicate panics.
func (r *Registry) Cron(name string, h func(sdk.CronEvent) error) {
	if !slices.Contains(CronNames, name) {
		panic(fmt.Sprintf("core: unknown cron %q", name))
	}
	if prev, dup := r.crons[name]; dup {
		panic(fmt.Sprintf("core: duplicate cron %q (%s and %s)", name, prev.phase, r.phase))
	}
	r.crons[name] = cronEntry{r.phase, h}
}

// Gate registers a check for a kind of action. A refusal shows the member the notice of an error that implements Notice() core.Notice with a defined code (else the caller's generic refusal); JSON callers may get Error().
func (r *Registry) Gate(k GateKind, h func(*GateReq) error) {
	r.gates[k] = append(r.gates[k], h)
}

// RunGates runs the gates of a kind in phase order; the first error wins. A
// panicking gate counts as an error with a generic text.
func (r *Registry) RunGates(k GateKind, req *GateReq) (err error) {
	for _, g := range r.gates[k] {
		if err = runGate(g, req); err != nil {
			return err
		}
	}
	return nil
}

func runGate(g func(*GateReq) error, req *GateReq) (err error) {
	defer func() {
		if recover() != nil {
			err = errGateFailed
		}
	}()
	return g(req)
}

// RowActions registers a provider of actions for story rows and item heads.
func (r *Registry) RowActions(h func(v *User, it Item) []Action) {
	r.rowActs = append(r.rowActs, h)
}

// ActionsFor concatenates the actions of every phase, in phase order. A
// panicking provider adds nothing.
func (r *Registry) ActionsFor(v *User, it Item) []Action {
	var out []Action
	for _, h := range r.rowActs {
		out = append(out, safeActions(h, v, it)...)
	}
	return out
}

func safeActions(h func(*User, Item) []Action, v *User, it Item) (out []Action) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	return h(v, it)
}

// Subscribe registers a handler of a story event.
func (r *Registry) Subscribe(k EventKind, h func(StoryEvent)) {
	r.subs[k] = append(r.subs[k], h)
}

// Publish delivers an event to every subscriber, synchronously, in phase
// order. A panicking subscriber does not stop the others.
func (r *Registry) Publish(ev StoryEvent) {
	for _, h := range r.subs[ev.Kind] {
		func() {
			defer func() { _ = recover() }()
			h(ev)
		}()
	}
}
