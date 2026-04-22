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
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

//go:embed templates/source.html
var sourceTpl string

//go:embed templates/archive.html
var archiveTpl string

//go:embed templates/plan.html
var planTpl string

//go:embed templates/facts.html
var factsTpl string

//go:embed templates/snapshot.html
var snapshotTpl string

//go:embed templates/timeline.html
var timelineTpl string

//go:embed templates/snapshot_board.html
var snapshotBoardTpl string

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
		"kanban":         kanbanTpl,
		"task":           taskTpl,
		"source":         sourceTpl,
		"archive":        archiveTpl,
		"plan":           planTpl,
		"facts":          factsTpl,
		"snapshot":       snapshotTpl,
		"timeline":       timelineTpl,
		"snapshot_board": snapshotBoardTpl,
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
	mux.HandleFunc("GET /task/{id}/source", s.handleTaskSource)
	mux.HandleFunc("GET /plan/{id}", s.handlePlan)
	mux.HandleFunc("GET /facts", s.handleFacts)
	mux.HandleFunc("GET /worktree/{wt}/facts", s.handleFacts)
	mux.HandleFunc("GET /snapshot/{id}", s.handleSnapshot)
	mux.HandleFunc("GET /worktree/{wt}/timeline", s.handleTimeline)
	mux.HandleFunc("GET /worktree/{wt}/snapshot/{sid}", s.handleSnapshotBoard)
	mux.HandleFunc("POST /override/{task_id}", s.handleOverride)
	mux.HandleFunc("GET /static/style.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Write(styleCSS)
	})
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
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
	Columns             []column
	Projects            []projectOption
	Plans               []planOption
	SelectedProjectID   string
	SelectedProjectName string
	SelectedPlanID      string
	SelectedPlanName    string
	Now                 time.Time
}

type projectOption struct {
	ID       string
	Name     string
	Selected bool
	// HREF holds the URL for the pill. The kanban handler scrubs other
	// filter params when switching project so users don't carry a stale
	// ?plan across projects.
	HREF string
}

type planOption struct {
	ID           string
	WorktreeName string
	Basename     string
	Title        string
	ProjectID    string
	Selected     bool
	HREF         string
	Missing      bool // file was not on disk during the last sweep
}

type column struct {
	Key   string
	Title string
	Cards []card
}

type card struct {
	Task         store.Task
	Worktree     store.Worktree
	ProjectName  string
	PlanBasename string
	PlanID       string
	Badges       []string
}

func (s *Server) handleKanban(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		s.internalError(w, err)
		return
	}
	selectedProjectID := r.URL.Query().Get("project")
	selectedPlanID := r.URL.Query().Get("plan")
	selectedProjectName := "All Projects"
	if selectedProjectID != "" {
		found := false
		for _, p := range projects {
			if p.ID == selectedProjectID {
				found = true
				selectedProjectName = p.Name
				break
			}
		}
		if !found {
			http.Error(w, "project not found: "+selectedProjectID, http.StatusNotFound)
			return
		}
	}

	// Load plan options for the current scope (project filter when set,
	// otherwise across all active worktrees). If the request asks for a
	// specific plan, validate it belongs to the current project scope.
	plans, err := s.planOptions(ctx, selectedProjectID, selectedPlanID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	selectedPlanName := ""
	var selectedPlanSourceFile string
	if selectedPlanID != "" {
		pd, err := s.store.PlanDocByID(ctx, selectedPlanID)
		if err != nil {
			http.Error(w, "plan not found: "+selectedPlanID, http.StatusNotFound)
			return
		}
		// When a project filter is set, the plan must belong to it. Keep
		// a real DB error distinct from a scope mismatch — swallowing the
		// former behind "plan does not belong to project" obscures outages.
		if selectedProjectID != "" {
			wt, err := s.store.WorktreeByID(ctx, pd.WorktreeID)
			if err != nil {
				s.internalError(w, fmt.Errorf("lookup worktree for plan %s: %w", pd.ID, err))
				return
			}
			if wt.ProjectID != selectedProjectID {
				http.Error(w, "plan does not belong to project", http.StatusBadRequest)
				return
			}
		}
		selectedPlanName = pd.Title
		if selectedPlanName == "" {
			selectedPlanName = filepath.Base(pd.SourceFile)
		}
		selectedPlanSourceFile = pd.SourceFile
	}

	cards, err := s.buildCards(ctx, false, selectedProjectID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if selectedPlanSourceFile != "" {
		filtered := cards[:0]
		for _, c := range cards {
			if c.Task.SourceFile == selectedPlanSourceFile {
				filtered = append(filtered, c)
			}
		}
		cards = filtered
	}
	columns := bucketize(cards, time.Now())
	data := kanbanData{
		Columns:             columns,
		Projects:            projectOptions(projects, selectedProjectID),
		Plans:               plans,
		SelectedProjectID:   selectedProjectID,
		SelectedProjectName: selectedProjectName,
		SelectedPlanID:      selectedPlanID,
		SelectedPlanName:    selectedPlanName,
		Now:                 time.Now(),
	}
	s.render(w, "kanban", data)
}

// buildCards loads all tasks (across all worktrees) into card view models.
// When includeArchived is false, cards from archived worktrees are skipped.
func (s *Server) buildCards(ctx context.Context, includeArchived bool, projectID string) ([]card, error) {
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
		if projectID != "" && wt.ProjectID != projectID {
			continue
		}
		// Pre-index plan_docs by source_file so every task rendered from this
		// worktree can carry its originating plan's ID/basename chip without
		// re-querying per-task.
		docs, _ := s.store.PlanDocsByWorktree(ctx, wt.ID)
		docByFile := map[string]store.PlanDoc{}
		for _, d := range docs {
			docByFile[d.SourceFile] = d
		}
		tasks, err := s.store.TasksByWorktree(ctx, wt.ID)
		if err != nil {
			return nil, err
		}
		for _, t := range tasks {
			c := card{Task: t, Worktree: wt, ProjectName: projects[wt.ProjectID]}
			if t.SourceFile != "" {
				c.PlanBasename = filepath.Base(t.SourceFile)
				if d, ok := docByFile[t.SourceFile]; ok {
					c.PlanID = d.ID
				}
			}
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

func projectOptions(projects []store.Project, selectedProjectID string) []projectOption {
	out := make([]projectOption, 0, len(projects))
	for _, p := range projects {
		out = append(out, projectOption{
			ID:       p.ID,
			Name:     p.Name,
			Selected: p.ID == selectedProjectID,
			HREF:     "/?project=" + url.QueryEscape(p.ID),
		})
	}
	return out
}

// planOptions returns one option per plan_doc visible in the current project
// scope. Ordering is: worktree name, then plan_doc source_file — stable and
// cheap to reason about in the template. The href carries the project filter
// so clicking a plan pill inside a project-scoped board keeps the scope.
func (s *Server) planOptions(ctx context.Context, projectID, selectedPlanID string) ([]planOption, error) {
	wts, err := s.store.ListWorktrees(ctx, false)
	if err != nil {
		return nil, err
	}
	sort.Slice(wts, func(i, j int) bool { return wts[i].Name < wts[j].Name })
	var out []planOption
	for _, wt := range wts {
		if projectID != "" && wt.ProjectID != projectID {
			continue
		}
		docs, err := s.store.PlanDocsByWorktree(ctx, wt.ID)
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			base := filepath.Base(d.SourceFile)
			q := url.Values{}
			if projectID != "" {
				q.Set("project", projectID)
			}
			q.Set("plan", d.ID)
			out = append(out, planOption{
				ID:           d.ID,
				WorktreeName: wt.Name,
				Basename:     base,
				Title:        d.Title,
				ProjectID:    wt.ProjectID,
				Selected:     d.ID == selectedPlanID,
				HREF:         "/?" + q.Encode(),
				Missing:      d.MissingSince != nil,
			})
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
	Revisions []taskHistoryRow
}

// taskHistoryRow decorates a TaskRevision with the href to its snapshot board
// so the template stays declarative. Changed flags trim the rendered diff so
// the reader sees the actual transitions, not a wall of duplicated rows.
type taskHistoryRow struct {
	Rev           store.TaskRevision
	SnapshotHref  string
	StatusChanged bool
	TitleChanged  bool
}

type sourceData struct {
	Task     store.Task
	Worktree *store.Worktree
	Source   sourceContext
}

type sourceContext struct {
	File      string
	Line      int
	StartLine int
	EndLine   int
	Lines     []sourceLine
}

type sourceLine struct {
	Number    int
	Content   string
	Highlight bool
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

	revs, err := s.store.TaskHistoryByTask(ctx, id)
	if err != nil {
		// Log and continue: the primary task row already rendered. Silently
		// omitting the history section otherwise leaves no trace of the DB
		// fault for the operator to diagnose.
		s.logger.Warn("task_history_failed", "task_id", id, "err", err)
	}
	history := make([]taskHistoryRow, 0, len(revs))
	var prevStatus, prevTitle string
	for i, rev := range revs {
		row := taskHistoryRow{
			Rev:          rev,
			SnapshotHref: "/worktree/" + rev.WorktreeID + "/snapshot/" + rev.SnapshotID,
		}
		if i == 0 {
			row.StatusChanged = true
			row.TitleChanged = true
		} else {
			row.StatusChanged = rev.Status != prevStatus
			row.TitleChanged = rev.Title != prevTitle
		}
		prevStatus, prevTitle = rev.Status, rev.Title
		history = append(history, row)
	}

	s.render(w, "task", taskData{
		Task: *t, Worktree: wt, Events: events, Overrides: overs,
		Lineage: lineagePanel{
			Parents: parents, Children: children, Renamed: t.RenamedFrom,
		},
		Revisions: history,
	})
}

func (s *Server) handleTaskSource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	t, err := s.store.TaskByID(ctx, id)
	if err != nil {
		http.Error(w, "task not found: "+id, http.StatusNotFound)
		return
	}
	if t.SourceFile == "" {
		http.Error(w, "task has no source file", http.StatusNotFound)
		return
	}
	source, err := readSourceContext(t.SourceFile, t.SourceLine, 14)
	if err != nil {
		http.Error(w, "read source: "+err.Error(), http.StatusNotFound)
		return
	}
	wt, _ := s.store.WorktreeByID(ctx, t.WorktreeID)
	s.render(w, "source", sourceData{
		Task: *t, Worktree: wt, Source: source,
	})
}

func readSourceContext(path string, line, radius int) (sourceContext, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sourceContext{}, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	if line < 1 {
		line = 1
	}
	if line > len(lines) {
		line = len(lines)
	}
	if radius < 0 {
		radius = 0
	}
	start := line - radius
	if start < 1 {
		start = 1
	}
	end := line + radius
	if end > len(lines) {
		end = len(lines)
	}
	out := sourceContext{
		File:      path,
		Line:      line,
		StartLine: start,
		EndLine:   end,
		Lines:     make([]sourceLine, 0, end-start+1),
	}
	for i := start; i <= end; i++ {
		out.Lines = append(out.Lines, sourceLine{
			Number:    i,
			Content:   lines[i-1],
			Highlight: i == line,
		})
	}
	return out, nil
}

// ---- Plan detail ----

type planDetailData struct {
	Plan           store.PlanDoc
	Worktree       *store.Worktree
	ProjectName    string
	LatestSnapshot *store.Snapshot
	LatestShortSHA string
	TaskCount      int
	Cards          []card
	OutgoingLinks  []planLinkRow
	IncomingLinks  []planLinkRow
}

// planLinkRow is a view-model for one row in the Links panels. Either the
// target plan_doc exists (ToPlan != nil, ID and title are rendered as a
// link) or it doesn't, in which case we only have the raw target path.
type planLinkRow struct {
	Link   store.PlanLink
	ToPlan *store.PlanDoc
}

func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	pd, err := s.store.PlanDocByID(ctx, id)
	if err != nil {
		http.Error(w, "plan not found: "+id, http.StatusNotFound)
		return
	}
	wt, _ := s.store.WorktreeByID(ctx, pd.WorktreeID)

	projectName := ""
	if wt != nil {
		projects, _ := s.store.ListProjects(ctx)
		for _, p := range projects {
			if p.ID == wt.ProjectID {
				projectName = p.Name
				break
			}
		}
	}

	var latest *store.Snapshot
	if wt != nil {
		latest, _ = s.store.LatestSnapshot(ctx, wt.ID, pd.SourceFile)
	}

	// Tasks that currently point at this plan's source_file.
	var cards []card
	if wt != nil {
		all, err := s.store.TasksByWorktree(ctx, wt.ID)
		if err != nil {
			s.internalError(w, err)
			return
		}
		for _, t := range all {
			if t.SourceFile != pd.SourceFile {
				continue
			}
			c := card{
				Task:         t,
				Worktree:     *wt,
				ProjectName:  projectName,
				PlanBasename: filepath.Base(t.SourceFile),
				PlanID:       pd.ID,
			}
			cards = append(cards, c)
		}
	}
	sort.Slice(cards, func(i, j int) bool {
		return cards[i].Task.SourceLine < cards[j].Task.SourceLine
	})

	// Outgoing links: read as stored, then resolve any ToPlanID to its PlanDoc.
	out, _ := s.store.LinksFrom(ctx, pd.ID)
	outRows := make([]planLinkRow, 0, len(out))
	for _, l := range out {
		row := planLinkRow{Link: l}
		if l.ToPlanID != "" {
			if tp, err := s.store.PlanDocByID(ctx, l.ToPlanID); err == nil {
				row.ToPlan = tp
			}
		}
		outRows = append(outRows, row)
	}

	// Incoming: scan all plan_docs in the worktree for links pointing back here.
	var inRows []planLinkRow
	if wt != nil {
		sibs, _ := s.store.PlanDocsByWorktree(ctx, wt.ID)
		for _, sib := range sibs {
			if sib.ID == pd.ID {
				continue
			}
			ls, _ := s.store.LinksFrom(ctx, sib.ID)
			for _, l := range ls {
				if l.ToPlanID == pd.ID {
					sibCopy := sib
					inRows = append(inRows, planLinkRow{Link: l, ToPlan: &sibCopy})
				}
			}
		}
	}

	taskCount, _ := s.store.TaskCountByPlanDoc(ctx, pd.ID)

	// Guard the short-SHA slice in Go so a short/empty commit_sha can never
	// panic the template. Non-git worktrees store "".
	latestShort := ""
	if latest != nil && len(latest.CommitSHA) >= 7 {
		latestShort = latest.CommitSHA[:7]
	}

	s.render(w, "plan", planDetailData{
		Plan:           *pd,
		Worktree:       wt,
		ProjectName:    projectName,
		LatestSnapshot: latest,
		LatestShortSHA: latestShort,
		TaskCount:      taskCount,
		Cards:          cards,
		OutgoingLinks:  outRows,
		IncomingLinks:  inRows,
	})
}

// ---- Facts & Snapshot ----

type factsData struct {
	Worktree     *store.Worktree
	ProjectName  string
	Snapshots    []snapshotRow
	AllWorktrees bool
}

type snapshotRow struct {
	Snap        store.Snapshot
	Worktree    store.Worktree
	ProjectName string
	ShortSHA    string
}

type snapshotDetailData struct {
	Snap          store.Snapshot
	Worktree      *store.Worktree
	ProjectName   string
	Plan          *store.PlanDoc
	PhasesPretty  string
	Events        []store.Event
	TaskRevisions []taskRevisionRow
	ShortSHA      string
}

type taskRevisionRow struct {
	Rev      store.TaskRevision
	TaskLink string
}

func (s *Server) handleFacts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	worktreeID := r.PathValue("wt")

	var data factsData
	if worktreeID == "" {
		data.AllWorktrees = true
	} else {
		wt, err := s.store.WorktreeByID(ctx, worktreeID)
		if err != nil {
			http.Error(w, "worktree not found", http.StatusNotFound)
			return
		}
		data.Worktree = wt
		if ps, _ := s.store.ListProjects(ctx); ps != nil {
			for _, p := range ps {
				if p.ID == wt.ProjectID {
					data.ProjectName = p.Name
				}
			}
		}
	}

	snaps, err := s.store.ListSnapshots(ctx, worktreeID, 100)
	if err != nil {
		s.internalError(w, err)
		return
	}
	// Index worktrees and projects so each row resolves its labels without
	// an N+1 query per snapshot.
	wts, _ := s.store.ListWorktrees(ctx, true)
	wtByID := map[string]store.Worktree{}
	for _, wt := range wts {
		wtByID[wt.ID] = wt
	}
	projects, _ := s.store.ListProjects(ctx)
	projectByID := map[string]string{}
	for _, p := range projects {
		projectByID[p.ID] = p.Name
	}
	for _, snap := range snaps {
		row := snapshotRow{Snap: snap}
		if wt, ok := wtByID[snap.WorktreeID]; ok {
			row.Worktree = wt
			row.ProjectName = projectByID[wt.ProjectID]
		}
		if len(snap.CommitSHA) >= 7 {
			row.ShortSHA = snap.CommitSHA[:7]
		}
		data.Snapshots = append(data.Snapshots, row)
	}
	s.render(w, "facts", data)
}

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	snap, err := s.store.SnapshotByID(ctx, id)
	if err != nil {
		http.Error(w, "snapshot not found: "+id, http.StatusNotFound)
		return
	}
	wt, _ := s.store.WorktreeByID(ctx, snap.WorktreeID)
	projectName := ""
	if wt != nil {
		if ps, _ := s.store.ListProjects(ctx); ps != nil {
			for _, p := range ps {
				if p.ID == wt.ProjectID {
					projectName = p.Name
				}
			}
		}
	}
	plan, _ := s.store.PlanDocByWorktreeAndFile(ctx, snap.WorktreeID, snap.SourceFile)

	// Pretty-print phases_json if it parses. If not, fall back to the raw
	// string rather than failing the page — raw facts mode has to render
	// even when data is malformed.
	pretty := snap.PhasesJSON
	var tmp any
	if err := json.Unmarshal([]byte(snap.PhasesJSON), &tmp); err == nil {
		if b, err := json.MarshalIndent(tmp, "", "  "); err == nil {
			pretty = string(b)
		}
	}

	events, _ := s.store.EventsBySnapshot(ctx, snap.ID)
	revs, _ := s.store.TaskRevisionsBySnapshot(ctx, snap.ID)

	revRows := make([]taskRevisionRow, 0, len(revs))
	for _, rev := range revs {
		revRows = append(revRows, taskRevisionRow{
			Rev:      rev,
			TaskLink: "/task/" + rev.TaskID,
		})
	}

	short := ""
	if len(snap.CommitSHA) >= 7 {
		short = snap.CommitSHA[:7]
	}

	s.render(w, "snapshot", snapshotDetailData{
		Snap:          *snap,
		Worktree:      wt,
		ProjectName:   projectName,
		Plan:          plan,
		PhasesPretty:  pretty,
		Events:        events,
		TaskRevisions: revRows,
		ShortSHA:      short,
	})
}

// ---- Time machine ----

type timelineData struct {
	Worktree    store.Worktree
	ProjectName string
	Rows        []timelineRow
}

type timelineRow struct {
	Snap        store.Snapshot
	ShortSHA    string
	TaskCount   int
	EventCount  int
	SourceBase  string
	BoardURL    string
	FactsURL    string
}

type snapshotBoardData struct {
	Worktree     store.Worktree
	ProjectName  string
	Snap         store.Snapshot
	ShortSHA     string
	Columns      []column
	FactsURL     string
	TimelineURL  string
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	worktreeID := r.PathValue("wt")
	wt, err := s.store.WorktreeByID(ctx, worktreeID)
	if err != nil {
		http.Error(w, "worktree not found", http.StatusNotFound)
		return
	}
	projectName := ""
	if ps, _ := s.store.ListProjects(ctx); ps != nil {
		for _, p := range ps {
			if p.ID == wt.ProjectID {
				projectName = p.Name
			}
		}
	}

	snaps, err := s.store.ListSnapshots(ctx, worktreeID, 200)
	if err != nil {
		s.internalError(w, err)
		return
	}

	rows := make([]timelineRow, 0, len(snaps))
	for _, snap := range snaps {
		revs, _ := s.store.TaskRevisionsBySnapshot(ctx, snap.ID)
		events, _ := s.store.EventsBySnapshot(ctx, snap.ID)
		row := timelineRow{
			Snap:       snap,
			TaskCount:  len(revs),
			EventCount: len(events),
			SourceBase: filepath.Base(snap.SourceFile),
			BoardURL:   "/worktree/" + worktreeID + "/snapshot/" + snap.ID,
			FactsURL:   "/snapshot/" + snap.ID,
		}
		if len(snap.CommitSHA) >= 7 {
			row.ShortSHA = snap.CommitSHA[:7]
		}
		rows = append(rows, row)
	}

	s.render(w, "timeline", timelineData{
		Worktree:    *wt,
		ProjectName: projectName,
		Rows:        rows,
	})
}

func (s *Server) handleSnapshotBoard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	worktreeID := r.PathValue("wt")
	snapID := r.PathValue("sid")

	wt, err := s.store.WorktreeByID(ctx, worktreeID)
	if err != nil {
		http.Error(w, "worktree not found", http.StatusNotFound)
		return
	}
	snap, err := s.store.SnapshotByID(ctx, snapID)
	if err != nil {
		http.Error(w, "snapshot not found", http.StatusNotFound)
		return
	}
	// Reject mismatched wt/snapshot — otherwise the board would render
	// another worktree's revisions under this worktree's chrome.
	if snap.WorktreeID != worktreeID {
		http.Error(w, "snapshot does not belong to worktree", http.StatusBadRequest)
		return
	}

	projectName := ""
	if ps, _ := s.store.ListProjects(ctx); ps != nil {
		for _, p := range ps {
			if p.ID == wt.ProjectID {
				projectName = p.Name
			}
		}
	}

	revs, err := s.store.TaskRevisionsBySnapshot(ctx, snapID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	// Build cards directly from revisions so we render the historical state,
	// not the live `task` row (which may have mutated since). Lineage fields
	// are copied off the revision too — otherwise the historical board would
	// render a task that was split/merged/lost with no badge, losing the
	// very ancestry the time machine exists to preserve.
	cards := make([]card, 0, len(revs))
	for _, rev := range revs {
		t := store.Task{
			ID:           rev.TaskID,
			WorktreeID:   rev.WorktreeID,
			ProjectID:    rev.ProjectID,
			CurrentTitle: rev.Title,
			Aliases:      rev.Aliases,
			Phase:        rev.Phase,
			Status:       rev.Status,
			Confidence:   rev.Confidence,
			SourceFile:   rev.SourceFile,
			SourceLine:   rev.SourceLine,
			LastSeenAt:   rev.RecordedAt,
			RenamedFrom:  rev.RenamedFrom,
			SplitFrom:    rev.SplitFrom,
			MergedFrom:   rev.MergedFrom,
			Supersedes:   rev.Supersedes,
		}
		c := card{
			Task:         t,
			Worktree:     *wt,
			ProjectName:  projectName,
			PlanBasename: filepath.Base(rev.SourceFile),
		}
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
		cards = append(cards, c)
	}

	short := ""
	if len(snap.CommitSHA) >= 7 {
		short = snap.CommitSHA[:7]
	}

	s.render(w, "snapshot_board", snapshotBoardData{
		Worktree:    *wt,
		ProjectName: projectName,
		Snap:        *snap,
		ShortSHA:    short,
		Columns:     bucketize(cards, snap.Timestamp),
		FactsURL:    "/snapshot/" + snapID,
		TimelineURL: "/worktree/" + worktreeID + "/timeline",
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
	cards, err := s.buildCards(ctx, true, "")
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
	// Self-reference is nonsense state: mark_same would make the task
	// supersede itself, split/merge would make it its own lineage parent.
	// Reject before writing, since overrides are append-only.
	if relatedID != "" && relatedID == taskID {
		http.Error(w, "related_task_id must differ from task id", http.StatusBadRequest)
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
