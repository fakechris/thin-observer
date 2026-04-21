// Package web serves the read-only thin-observer kanban board.
//
// Board is read-only in one sense: it never touches the plan markdown files.
// Users CAN, however, record manual overrides (e.g., "these are the same task",
// "this is actually dropped, not lost"). Overrides live only in observer state
// and are applied during future ingestion.
package web

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/chris/thin-observer/internal/store"
	"github.com/oklog/ulid/v2"
)

//go:embed templates/layout.html
var layoutTpl string

//go:embed templates/kanban.html
var kanbanTpl string

//go:embed templates/task.html
var taskTpl string

//go:embed templates/archive.html
var archiveTpl string

//go:embed static/style.css
var styleCSS []byte

// Server is the HTTP handler.
type Server struct {
	store  *store.Store
	logger *slog.Logger
	pages  map[string]*template.Template
}

// New builds a Server wired to the given store.
func New(s *store.Store, logger *slog.Logger) (*Server, error) {
	funcs := template.FuncMap{
		"humanAge": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			d := time.Since(t)
			switch {
			case d < time.Minute:
				return fmt.Sprintf("%ds", int(d.Seconds()))
			case d < time.Hour:
				return fmt.Sprintf("%dm", int(d.Minutes()))
			case d < 24*time.Hour:
				return fmt.Sprintf("%dh", int(d.Hours()))
			default:
				return fmt.Sprintf("%dd", int(d.Hours()/24))
			}
		},
		"statusBadge": func(s string) string {
			switch s {
			case "done":
				return "done"
			case "in_progress":
				return "active"
			case "lost":
				return "lost"
			case "skipped":
				return "skipped"
			case "dropped":
				return "dropped"
			default:
				return "pending"
			}
		},
		"confidenceClass": func(c float64) string {
			if c >= 0.9 {
				return "conf-high"
			}
			if c >= 0.7 {
				return "conf-med"
			}
			return "conf-low"
		},
		"join": func(xs []string, sep string) string { return strings.Join(xs, sep) },
	}
	base, err := template.New("layout").Funcs(funcs).Parse(layoutTpl)
	if err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}
	pages := map[string]*template.Template{}
	for name, src := range map[string]string{
		"kanban":  kanbanTpl,
		"task":    taskTpl,
		"archive": archiveTpl,
	} {
		cl, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone for %s: %w", name, err)
		}
		if _, err := cl.Parse(src); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		pages[name] = cl
	}
	return &Server{store: s, logger: logger, pages: pages}, nil
}

// Routes returns a *http.ServeMux with all handlers registered.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleKanban)
	mux.HandleFunc("GET /archive", s.handleArchive)
	mux.HandleFunc("GET /task/{id}", s.handleTask)
	mux.HandleFunc("POST /override/{task_id}", s.handleOverride)
	mux.HandleFunc("GET /static/style.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Write(styleCSS)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})
	return mux
}

// ListenAndServe is a convenience wrapper.
func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:         addr,
		Handler:      s.Routes(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

// ---- Kanban ----

type kanbanData struct {
	Columns []column
	Now     time.Time
}

type column struct {
	Key   string
	Title string
	Cards []card
}

type card struct {
	Task        store.Task
	Worktree    store.Worktree
	ProjectName string
	Badges      []string
}

func (s *Server) handleKanban(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cards, err := s.buildCards(ctx, false)
	if err != nil {
		s.internalError(w, err)
		return
	}
	columns := bucketize(cards, time.Now())
	data := kanbanData{Columns: columns, Now: time.Now()}
	s.render(w, "kanban", data)
}

// buildCards loads all tasks (across all worktrees) into card view models.
// When includeArchived is false, cards from archived worktrees are skipped.
func (s *Server) buildCards(ctx context.Context, includeArchived bool) ([]card, error) {
	wts, err := s.store.ListWorktrees(ctx, includeArchived)
	if err != nil {
		return nil, err
	}
	projects := map[string]string{}
	pr, _ := s.store.ListProjects(ctx)
	for _, p := range pr {
		projects[p.ID] = p.Name
	}
	var out []card
	for _, wt := range wts {
		tasks, err := s.store.TasksByWorktree(ctx, wt.ID)
		if err != nil {
			return nil, err
		}
		for _, t := range tasks {
			c := card{Task: t, Worktree: wt, ProjectName: projects[wt.ProjectID]}
			if t.Confidence > 0 && t.Confidence < 0.7 {
				c.Badges = append(c.Badges, "low-confidence")
			}
			if len(t.SplitFrom) > 0 {
				c.Badges = append(c.Badges, "split")
			}
			if len(t.MergedFrom) > 0 {
				c.Badges = append(c.Badges, "merged")
			}
			if t.Status == "lost" {
				c.Badges = append(c.Badges, "lost")
			}
			out = append(out, c)
		}
	}
	return out, nil
}

// bucketize sorts cards into the six display columns.
//
// Rules:
//
//	Inbox      → pending, no activity yet (first-seen == last-seen and recent)
//	Active     → in_progress
//	Stalled    → pending or in_progress, last activity > 1h
//	Attention  → lost, or confidence < 0.7
//	Done       → done or skipped
//	Archive    → dropped or archived-worktree tasks (handled separately)
func bucketize(cards []card, now time.Time) []column {
	cols := []column{
		{Key: "inbox", Title: "Inbox"},
		{Key: "active", Title: "Active"},
		{Key: "stalled", Title: "Stalled"},
		{Key: "attention", Title: "Needs Attention"},
		{Key: "done", Title: "Done"},
	}
	idx := map[string]int{}
	for i, c := range cols {
		idx[c.Key] = i
	}

	for _, c := range cards {
		if c.Worktree.Status == "archived" || c.Task.Status == "dropped" {
			continue
		}
		key := classify(c, now)
		cols[idx[key]].Cards = append(cols[idx[key]].Cards, c)
	}

	for i := range cols {
		sort.Slice(cols[i].Cards, func(a, b int) bool {
			return cols[i].Cards[a].Task.LastSeenAt.After(cols[i].Cards[b].Task.LastSeenAt)
		})
	}
	return cols
}

func classify(c card, now time.Time) string {
	t := c.Task
	if t.Status == "done" || t.Status == "skipped" {
		return "done"
	}
	if t.Status == "lost" || (t.Confidence > 0 && t.Confidence < 0.7) {
		return "attention"
	}
	if t.Status == "in_progress" {
		return "active"
	}
	// pending / unknown
	if !t.LastSeenAt.IsZero() && now.Sub(t.LastSeenAt) > time.Hour {
		return "stalled"
	}
	return "inbox"
}

// ---- Task detail ----

type taskData struct {
	Task      store.Task
	Worktree  *store.Worktree
	Events    []store.Event
	Overrides []store.Override
	Lineage   lineagePanel
}

type lineagePanel struct {
	Parents  []store.Task // tasks referenced in SplitFrom / MergedFrom
	Children []store.Task // tasks whose SplitFrom / MergedFrom contains this task
	Renamed  string
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	t, err := s.store.TaskByID(ctx, id)
	if err != nil {
		http.Error(w, "task not found: "+id, http.StatusNotFound)
		return
	}
	wt, _ := s.store.WorktreeByID(ctx, t.WorktreeID)
	events, _ := s.store.EventsForTask(ctx, id)
	overs, _ := s.store.OverridesForTask(ctx, id)

	// Resolve lineage: look up parents and find children.
	var parents []store.Task
	for _, pid := range t.SplitFrom {
		if p, err := s.store.TaskByID(ctx, pid); err == nil {
			parents = append(parents, *p)
		}
	}
	for _, pid := range t.MergedFrom {
		if p, err := s.store.TaskByID(ctx, pid); err == nil {
			parents = append(parents, *p)
		}
	}
	var children []store.Task
	if all, err := s.store.TasksByWorktree(ctx, t.WorktreeID); err == nil {
		for _, ot := range all {
			for _, pid := range ot.SplitFrom {
				if pid == id {
					children = append(children, ot)
				}
			}
			for _, pid := range ot.MergedFrom {
				if pid == id {
					children = append(children, ot)
				}
			}
		}
	}

	s.render(w, "task", taskData{
		Task: *t, Worktree: wt, Events: events, Overrides: overs,
		Lineage: lineagePanel{
			Parents: parents, Children: children, Renamed: t.RenamedFrom,
		},
	})
}

// ---- Archive ----

type archiveData struct {
	ArchivedWorktrees []store.Worktree
	DroppedTasks      []card
	LostTasks         []card
}

func (s *Server) handleArchive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cards, err := s.buildCards(ctx, true)
	if err != nil {
		s.internalError(w, err)
		return
	}
	data := archiveData{}
	wts, _ := s.store.ListWorktrees(ctx, true)
	for _, wt := range wts {
		if wt.Status == "archived" {
			data.ArchivedWorktrees = append(data.ArchivedWorktrees, wt)
		}
	}
	for _, c := range cards {
		switch {
		case c.Task.Status == "dropped":
			data.DroppedTasks = append(data.DroppedTasks, c)
		case c.Task.Status == "lost":
			data.LostTasks = append(data.LostTasks, c)
		}
	}
	s.render(w, "archive", data)
}

// ---- Override POST ----

// allowedOverrideKinds matches the options in templates/task.html. Keeping the
// whitelist here rather than trusting form input prevents clients from writing
// garbage kinds that the ingester would later ignore.
var allowedOverrideKinds = map[string]bool{
	"mark_same":        true,
	"mark_dropped":     true,
	"mark_split_from":  true,
	"mark_merged_from": true,
}

// kindsNeedingRelated are override kinds that describe a relationship to
// another task. For these, related_task_id is mandatory.
var kindsNeedingRelated = map[string]bool{
	"mark_same":        true,
	"mark_split_from":  true,
	"mark_merged_from": true,
}

func (s *Server) handleOverride(w http.ResponseWriter, r *http.Request) {
	// CSRF mitigation: the board is meant to run on localhost. Any POST that
	// didn't originate from the same host is almost certainly a cross-site
	// forgery attempt — reject without touching state.
	if !sameOriginPOST(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	taskID := r.PathValue("task_id")
	kind := r.FormValue("kind")
	note := r.FormValue("note")
	relatedID := strings.TrimSpace(r.FormValue("related_task_id"))
	if kind == "" {
		http.Error(w, "kind required", http.StatusBadRequest)
		return
	}
	if !allowedOverrideKinds[kind] {
		http.Error(w, "unknown override kind: "+kind, http.StatusBadRequest)
		return
	}
	if kindsNeedingRelated[kind] && relatedID == "" {
		http.Error(w, "related_task_id required for kind "+kind, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	// Existence checks *outside* the tx so we can return crisp 404/400s.
	if _, err := s.store.TaskByID(ctx, taskID); err != nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	if relatedID != "" {
		if _, err := s.store.TaskByID(ctx, relatedID); err != nil {
			http.Error(w, "related task not found: "+relatedID, http.StatusBadRequest)
			return
		}
	}

	now := time.Now().UTC()
	overrideID, err := ulid.New(ulid.Timestamp(now), ulid.DefaultEntropy())
	if err != nil {
		s.internalError(w, fmt.Errorf("ulid: %w", err))
		return
	}
	eventID, err := ulid.New(ulid.Timestamp(now), ulid.DefaultEntropy())
	if err != nil {
		s.internalError(w, fmt.Errorf("ulid: %w", err))
		return
	}

	// Wrap the override insert + task-state mutation in a single transaction.
	// The override table by itself was merely audit — inert unless we also
	// apply the effect to the task, which is what the user actually sees.
	txErr := s.store.WithTx(ctx, func(tx *store.Store) error {
		t, err := tx.TaskByID(ctx, taskID)
		if err != nil {
			return fmt.Errorf("task: %w", err)
		}
		o := store.Override{
			ID: overrideID.String(), TaskID: taskID, Kind: kind, Note: note, CreatedAt: now,
		}
		if relatedID != "" {
			o.Data = map[string]any{"related_task_id": relatedID}
		}
		if err := tx.InsertOverride(ctx, o); err != nil {
			return fmt.Errorf("insert override: %w", err)
		}

		// Apply the kind's effect on task state.
		switch kind {
		case "mark_dropped":
			t.Status = "dropped"
		case "mark_same":
			// This task is the duplicate; related is canonical.
			t.Status = "dropped"
			t.Supersedes = relatedID
		case "mark_split_from":
			t.SplitFrom = appendUniqueStr(t.SplitFrom, relatedID)
			// User-asserted lineage is high confidence.
			if t.Confidence < 1.0 {
				t.Confidence = 1.0
			}
		case "mark_merged_from":
			t.MergedFrom = appendUniqueStr(t.MergedFrom, relatedID)
			if t.Confidence < 1.0 {
				t.Confidence = 1.0
			}
		}
		t.LastSeenAt = now
		if err := tx.UpsertTask(ctx, *t); err != nil {
			return fmt.Errorf("upsert task: %w", err)
		}

		return tx.InsertEvent(ctx, store.Event{
			ID: eventID.String(), Timestamp: now, Type: "task_override",
			TaskID: taskID, WorktreeID: t.WorktreeID,
			Data: map[string]any{"kind": kind, "related_task_id": relatedID, "note": note},
		})
	})
	if txErr != nil {
		s.internalError(w, txErr)
		return
	}
	http.Redirect(w, r, "/task/"+taskID, http.StatusSeeOther)
}

// sameOriginPOST returns true when the request's Origin or Referer header
// points to the same host the request was served from. Falls back to allowing
// the request only if both headers are absent *and* it's from a loopback
// address — which a CSRF-attack browser won't be.
func sameOriginPOST(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return u.Host == r.Host
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		u, err := url.Parse(ref)
		if err != nil {
			return false
		}
		return u.Host == r.Host
	}
	// No origin info at all — accept only for loopback clients (curl, tests).
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func appendUniqueStr(xs []string, s string) []string {
	if s == "" {
		return xs
	}
	for _, x := range xs {
		if x == s {
			return xs
		}
	}
	return append(xs, s)
}

// ---- helpers ----

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tpl, ok := s.pages[name]
	if !ok {
		s.logger.Warn("render.unknown_page", "name", name)
		http.Error(w, "unknown page", http.StatusInternalServerError)
		return
	}
	if err := tpl.ExecuteTemplate(w, "layout", data); err != nil {
		s.logger.Warn("render", "tpl", name, "err", err)
	}
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.logger.Warn("web.error", "err", err)
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
