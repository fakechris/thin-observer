package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chris/thin-observer/internal/ingest"
	"github.com/chris/thin-observer/internal/lineage"
	"github.com/chris/thin-observer/internal/parser"
	"github.com/chris/thin-observer/internal/store"
)

func testServer(t *testing.T) (*Server, *store.Store, store.Worktree) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	_ = s.UpsertProject(ctx, store.Project{ID: "p1", Name: "demo", RootPath: "/tmp/demo"})
	w := store.Worktree{ID: "w1", ProjectID: "p1", Name: "feature", Path: "/tmp/demo/feature"}
	_ = s.UpsertWorktree(ctx, w)

	in := ingest.New(s)
	in.Inferrer = lineage.New()
	doc, _ := parser.ParseFile(filepath.Join("..", "..", "testdata", "plans", "planning-with-files.md"))
	_, _ = in.Apply(ctx, w, doc)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := New(s, logger)
	if err != nil {
		t.Fatal(err)
	}
	return srv, s, w
}

func TestKanbanRenders(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, want := range []string{"Inbox", "Active", "Done", "thin-observer", "feature"} {
		if !strings.Contains(body, want) {
			t.Errorf("kanban missing %q", want)
		}
	}
	// Per-card Time Machine link was noisy; it lives on task/plan detail now.
	if strings.Contains(body, "/worktree/w1/timeline") {
		t.Errorf("kanban should not render per-card timeline links")
	}
}

func TestKanbanCanFilterByProject(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	_ = s.UpsertProject(ctx, store.Project{ID: "p1", Name: "alpha", RootPath: "/tmp/alpha"})
	_ = s.UpsertProject(ctx, store.Project{ID: "p2", Name: "beta", RootPath: "/tmp/beta"})
	_ = s.UpsertWorktree(ctx, store.Worktree{ID: "w1", ProjectID: "p1", Name: "alpha-wt", Path: "/tmp/alpha/wt"})
	_ = s.UpsertWorktree(ctx, store.Worktree{ID: "w2", ProjectID: "p2", Name: "beta-wt", Path: "/tmp/beta/wt"})
	now := time.Now().UTC()
	_ = s.UpsertTask(ctx, store.Task{
		ID: "t-alpha", WorktreeID: "w1", ProjectID: "p1", CurrentTitle: "Alpha task",
		Status: "pending", Confidence: 1, FirstSeenAt: now, LastSeenAt: now,
	})
	_ = s.UpsertTask(ctx, store.Task{
		ID: "t-beta", WorktreeID: "w2", ProjectID: "p2", CurrentTitle: "Beta task",
		Status: "pending", Confidence: 1, FirstSeenAt: now, LastSeenAt: now,
	})

	srv, err := New(s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/?project=p2", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{"project-switcher", "All Projects", "beta", "Beta task", "?project=p1"} {
		if !strings.Contains(body, want) {
			t.Errorf("filtered kanban missing %q", want)
		}
	}
	if strings.Contains(body, "Alpha task") {
		t.Errorf("filtered kanban included task from another project")
	}
}

func TestKanbanShowsPlanSwitcherAndFiltersByPlan(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()

	// One project, one worktree, two plan files — root task_plan.md and a
	// detailed docs/plans/phase27.md — each with its own task. Filtering by
	// one plan should hide tasks from the other.
	root := t.TempDir()
	_ = s.UpsertProject(ctx, store.Project{ID: "p1", Name: "demo", RootPath: root})
	w := store.Worktree{ID: "w1", ProjectID: "p1", Name: "feature", Path: root}
	_ = s.UpsertWorktree(ctx, w)
	if err := os.MkdirAll(filepath.Join(root, "docs/plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	taskPlan := filepath.Join(root, "task_plan.md")
	phasePlan := filepath.Join(root, "docs/plans/phase27.md")
	if err := os.WriteFile(taskPlan, []byte("## TODO\n- [ ] Root task\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(phasePlan, []byte("---\ntitle: Phase 27\n---\n\n## Phase 27\n- [ ] Phase task\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	in := ingest.New(s)
	d1, _ := parser.ParseFile(taskPlan)
	if _, err := in.Apply(ctx, w, d1); err != nil {
		t.Fatal(err)
	}
	d2, _ := parser.ParseFile(phasePlan)
	if _, err := in.Apply(ctx, w, d2); err != nil {
		t.Fatal(err)
	}

	srv, err := New(s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	// Full board renders both plan pills and both tasks.
	r := httptest.NewRequest("GET", "/?project=p1", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"plan-drawer",
		"Plan files",
		"data-plan-count=\"2\"",
		"task_plan.md",
		"phase27.md",
		"Root task",
		"Phase task",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("full board missing %q", want)
		}
	}

	// Now load /plan/<id> for the root plan so we can find its ID.
	docs, _ := s.PlanDocsByWorktree(ctx, w.ID)
	var rootID string
	for _, d := range docs {
		if d.SourceFile == taskPlan {
			rootID = d.ID
		}
	}
	if rootID == "" {
		t.Fatal("root plan doc not found")
	}

	// Filtered by root plan: Phase task must be hidden.
	r2 := httptest.NewRequest("GET", "/?project=p1&plan="+rootID, nil)
	rec2 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec2, r2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("filter status=%d body=%s", rec2.Code, rec2.Body)
	}
	b2 := rec2.Body.String()
	if !strings.Contains(b2, "Root task") {
		t.Error("plan filter dropped root task")
	}
	if strings.Contains(b2, "Phase task") {
		t.Error("plan filter did not hide task from other plan")
	}
}

func TestPlanDrawerMarksMissingPlanFiles(t *testing.T) {
	// Once a plan_doc has its missing_since set (simulating a sweep that
	// didn't find the file on disk), both the plan drawer on the board and
	// the plan detail page should show a visible "missing" marker.
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()

	root := t.TempDir()
	_ = s.UpsertProject(ctx, store.Project{ID: "p1", Name: "demo", RootPath: root})
	w := store.Worktree{ID: "w1", ProjectID: "p1", Name: "feature", Path: root}
	_ = s.UpsertWorktree(ctx, w)
	_ = os.MkdirAll(filepath.Join(root, "docs/plans"), 0o755)
	taskPlan := filepath.Join(root, "task_plan.md")
	phasePlan := filepath.Join(root, "docs/plans/phase27.md")
	_ = os.WriteFile(taskPlan, []byte("## TODO\n- [ ] Root task\n"), 0o644)
	_ = os.WriteFile(phasePlan, []byte("## Phase\n- [ ] Phase task\n"), 0o644)
	in := ingest.New(s)
	d1, _ := parser.ParseFile(taskPlan)
	_, _ = in.Apply(ctx, w, d1)
	d2, _ := parser.ParseFile(phasePlan)
	_, _ = in.Apply(ctx, w, d2)

	// Delete phase plan from disk, run a sweep against the surviving file only.
	_ = os.Remove(phasePlan)
	if err := s.MarkPlanDocsMissing(ctx, w.ID, []string{taskPlan}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	srv, err := New(s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	// Board: drawer should render a missing marker on the phase pill.
	r := httptest.NewRequest("GET", "/?project=p1", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("board status=%d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "plan-pill-missing") {
		t.Errorf("board missing plan-pill-missing class")
	}

	// Plan detail: flag visible too.
	docs, _ := s.PlanDocsByWorktree(ctx, w.ID)
	var phaseID string
	for _, d := range docs {
		if d.SourceFile == phasePlan {
			phaseID = d.ID
		}
	}
	if phaseID == "" {
		t.Fatal("phase plan_doc not found")
	}
	r2 := httptest.NewRequest("GET", "/plan/"+phaseID, nil)
	rec2 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec2, r2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("plan detail status=%d", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "missing since") {
		t.Errorf("plan detail missing 'missing since' flag")
	}
}

func TestKanbanUnknownPlanReturnsNotFound(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/?plan=doesnotexist", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rec.Code)
	}
}

func TestPlanDetailPage(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()

	root := t.TempDir()
	_ = s.UpsertProject(ctx, store.Project{ID: "p1", Name: "demo", RootPath: root})
	w := store.Worktree{ID: "w1", ProjectID: "p1", Name: "feature", Path: root}
	_ = s.UpsertWorktree(ctx, w)
	if err := os.MkdirAll(filepath.Join(root, "docs/plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	taskPlan := filepath.Join(root, "task_plan.md")
	phasePlan := filepath.Join(root, "docs/plans/phase27.md")
	_ = os.WriteFile(taskPlan, []byte("## TODO\n- [ ] See [Phase 27](docs/plans/phase27.md)\n"), 0o644)
	_ = os.WriteFile(phasePlan, []byte("---\ntitle: Phase 27 Closeout\n---\n\n## Phase 27\n- [ ] Wire queue\n"), 0o644)

	in := ingest.New(s)
	d1, _ := parser.ParseFile(taskPlan)
	_, _ = in.Apply(ctx, w, d1)
	d2, _ := parser.ParseFile(phasePlan)
	_, _ = in.Apply(ctx, w, d2)

	docs, _ := s.PlanDocsByWorktree(ctx, w.ID)
	var rootID, phaseID string
	for _, d := range docs {
		switch d.SourceFile {
		case taskPlan:
			rootID = d.ID
		case phasePlan:
			phaseID = d.ID
		}
	}
	if rootID == "" || phaseID == "" {
		t.Fatal("plans missing")
	}

	srv, err := New(s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	// Root plan page: linked plan is rendered, task from root plan is listed,
	// phase task is NOT listed.
	r := httptest.NewRequest("GET", "/plan/"+rootID, nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan page status=%d body=%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Phase 27 Closeout",           // linked plan title
		"task_plan.md",                // basename
		"/plan/" + phaseID,            // resolved link target
		"/worktree/" + w.ID + "/timeline", // time machine entry point
	} {
		if !strings.Contains(body, want) {
			t.Errorf("root plan page missing %q", want)
		}
	}
	if strings.Contains(body, "Wire queue") {
		t.Error("root plan page leaked tasks from other plan")
	}

	// Phase plan page: should show incoming link from root.
	r2 := httptest.NewRequest("GET", "/plan/"+phaseID, nil)
	rec2 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec2, r2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("phase plan status=%d body=%s", rec2.Code, rec2.Body)
	}
	b2 := rec2.Body.String()
	if !strings.Contains(b2, "/plan/"+rootID) {
		t.Error("phase plan page missing incoming link to root plan")
	}
	if !strings.Contains(b2, "Wire queue") {
		t.Error("phase plan page missing its own task")
	}
}

func TestFactsIndexListsRecentSnapshots(t *testing.T) {
	srv, s, w := testServer(t)
	ctx := context.Background()
	snaps, _ := s.ListSnapshots(ctx, w.ID, 10)
	if len(snaps) == 0 {
		t.Fatal("expected a seeded snapshot")
	}

	r := httptest.NewRequest("GET", "/facts", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("facts status=%d body=%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, snaps[0].ID) {
		t.Errorf("facts page missing snapshot id %q", snaps[0].ID)
	}
	if !strings.Contains(body, w.Name) {
		t.Errorf("facts page missing worktree name %q", w.Name)
	}
}

func TestFactsScopedToWorktree(t *testing.T) {
	srv, s, w := testServer(t)
	ctx := context.Background()
	_ = s.UpsertWorktree(ctx, store.Worktree{ID: "w2", ProjectID: "p1", Name: "other-wt", Path: "/tmp/other"})
	if err := s.InsertSnapshot(ctx, store.Snapshot{
		ID: "snap-other", WorktreeID: "w2", SourceFile: "/tmp/other/plan.md",
		Timestamp: time.Now().UTC(), RawHash: "zzz", PhasesJSON: "[]",
	}); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/worktree/"+w.ID+"/facts", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("scoped facts status=%d body=%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, w.Name) {
		t.Errorf("scoped facts missing own worktree name")
	}
	if strings.Contains(body, "snap-other") {
		t.Errorf("scoped facts leaked snapshot from other worktree")
	}
}

func TestSnapshotDetailPageRendersFacts(t *testing.T) {
	srv, s, w := testServer(t)
	ctx := context.Background()
	snaps, _ := s.ListSnapshots(ctx, w.ID, 10)
	if len(snaps) == 0 {
		t.Fatal("expected a seeded snapshot")
	}
	snap := snaps[0]

	r := httptest.NewRequest("GET", "/snapshot/"+snap.ID, nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot page status=%d body=%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{
		snap.ID,
		"phases_json",
		"Task revisions",
		snap.RawHash,
		snap.SourceFile,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("snapshot page missing %q", want)
		}
	}
}

func TestSnapshotUnknownIDReturnsNotFound(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/snapshot/doesnotexist", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rec.Code)
	}
}

func TestTimelineScopedToOneWorktree(t *testing.T) {
	srv, s, w := testServer(t)
	ctx := context.Background()
	_ = s.UpsertWorktree(ctx, store.Worktree{ID: "w2", ProjectID: "p1", Name: "other-wt", Path: "/tmp/other"})
	if err := s.InsertSnapshot(ctx, store.Snapshot{
		ID: "snap-other", WorktreeID: "w2", SourceFile: "/tmp/other/plan.md",
		Timestamp: time.Now().UTC(), RawHash: "zzz", PhasesJSON: "[]",
	}); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/worktree/"+w.ID+"/timeline", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("timeline status=%d body=%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, w.Name) {
		t.Errorf("timeline missing own worktree name")
	}
	if strings.Contains(body, "snap-other") {
		t.Errorf("timeline leaked snapshot from other worktree")
	}
}

func TestSnapshotBoardShowsHistoricalStateAfterMutation(t *testing.T) {
	// The whole point of Phase 6/9 is that re-ingests don't overwrite what
	// the board showed at time T. Seed a task at status=pending, snapshot,
	// then mutate the live task row to status=done and confirm the snapshot
	// board still renders "pending".
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()

	root := t.TempDir()
	_ = s.UpsertProject(ctx, store.Project{ID: "p1", Name: "demo", RootPath: root})
	w := store.Worktree{ID: "w1", ProjectID: "p1", Name: "feature", Path: root}
	_ = s.UpsertWorktree(ctx, w)

	plan := filepath.Join(root, "task_plan.md")
	if err := os.WriteFile(plan, []byte("## Phase 1\n- [ ] alpha\n- [ ] beta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := ingest.New(s)
	d1, _ := parser.ParseFile(plan)
	res1, err := in.Apply(ctx, w, d1)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a re-ingest with alpha → done and new 'gamma' task.
	if err := os.WriteFile(plan, []byte("## Phase 1\n- [x] alpha\n- [ ] beta\n- [ ] gamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d2, _ := parser.ParseFile(plan)
	if _, err := in.Apply(ctx, w, d2); err != nil {
		t.Fatal(err)
	}

	srv, err := New(s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	// Snapshot 1 board must show alpha as pending and must NOT show gamma.
	r := httptest.NewRequest("GET", "/worktree/"+w.ID+"/snapshot/"+res1.SnapshotID, nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot board status=%d body=%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "alpha") {
		t.Error("snapshot board missing alpha")
	}
	if strings.Contains(body, "gamma") {
		t.Error("snapshot board leaked future task gamma — historical render broken")
	}
	// The Done column should NOT list alpha at snapshot 1, since at that
	// point alpha was still pending. Grep for the substring sequence that
	// only appears if alpha shows up in the Inbox column.
	if !strings.Contains(body, ">pending<") {
		t.Error("snapshot board missing pending status badge for alpha")
	}
}

func TestSnapshotBoardRejectsMismatchedWorktree(t *testing.T) {
	srv, s, w := testServer(t)
	ctx := context.Background()
	snaps, _ := s.ListSnapshots(ctx, w.ID, 1)
	if len(snaps) == 0 {
		t.Fatal("no snapshot to test against")
	}
	snap := snaps[0]
	_ = s.UpsertWorktree(ctx, store.Worktree{ID: "w-other", ProjectID: "p1", Name: "decoy", Path: "/tmp/decoy"})

	r := httptest.NewRequest("GET", "/worktree/w-other/snapshot/"+snap.ID, nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
}

func TestTimelineUnknownWorktreeReturnsNotFound(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/worktree/doesnotexist/timeline", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rec.Code)
	}
}

func TestPlanPageUnknownIDReturnsNotFound(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/plan/doesnotexist", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rec.Code)
	}
}

func TestKanbanUnknownProjectReturnsNotFound(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/?project=missing", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body)
	}
}

func TestTaskPageAndOverride(t *testing.T) {
	srv, s, w := testServer(t)
	tasks, _ := s.TasksByWorktree(context.Background(), w.ID)
	if len(tasks) == 0 {
		t.Fatal("expected seeded tasks")
	}
	taskID := tasks[0].ID

	r := httptest.NewRequest("GET", "/task/"+taskID, nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("task detail status=%d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, tasks[0].CurrentTitle) {
		t.Errorf("task page missing title: %q", tasks[0].CurrentTitle)
	}
	if !strings.Contains(body, "Manual override") {
		t.Errorf("task page missing override form")
	}
	if !strings.Contains(body, "/task/"+taskID+"/source") {
		t.Errorf("task page missing source context link")
	}
	if !strings.Contains(body, "/worktree/"+w.ID+"/timeline") {
		t.Errorf("task page missing time machine link for its worktree")
	}

	// POST an override; expect redirect back to /task/<id>.
	form := url.Values{}
	form.Set("kind", "mark_dropped")
	form.Set("note", "we will not do this one")
	r2 := httptest.NewRequest("POST", "/override/"+taskID, strings.NewReader(form.Encode()))
	r2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r2.Header.Set("Origin", "http://"+r2.Host)
	rec2 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec2, r2)
	if rec2.Code != http.StatusSeeOther {
		t.Fatalf("override status=%d want 303", rec2.Code)
	}
	overs, _ := s.OverridesForTask(context.Background(), taskID)
	if len(overs) != 1 || overs[0].Kind != "mark_dropped" {
		t.Fatalf("expected 1 override kind=mark_dropped, got %+v", overs)
	}
	// mark_dropped must actually flip the task's status, not just log.
	t2, err := s.TaskByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if t2.Status != "dropped" {
		t.Errorf("task status=%q want dropped — override did not apply", t2.Status)
	}
}

func TestTaskPageRendersRevisionHistory(t *testing.T) {
	// After two ingests that change a task's status, the task detail page
	// should list both revisions with links back to their snapshots. Relying
	// on events is not enough — events record what fired, revisions record
	// the resulting task state.
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()

	root := t.TempDir()
	_ = s.UpsertProject(ctx, store.Project{ID: "p1", Name: "demo", RootPath: root})
	w := store.Worktree{ID: "w1", ProjectID: "p1", Name: "feature", Path: root}
	_ = s.UpsertWorktree(ctx, w)

	plan := filepath.Join(root, "task_plan.md")
	if err := os.WriteFile(plan, []byte("## Phase 1\n- [ ] alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := ingest.New(s)
	d1, _ := parser.ParseFile(plan)
	res1, err := in.Apply(ctx, w, d1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plan, []byte("## Phase 1\n- [x] alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d2, _ := parser.ParseFile(plan)
	res2, err := in.Apply(ctx, w, d2)
	if err != nil {
		t.Fatal(err)
	}

	tasks, _ := s.TasksByWorktree(ctx, w.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	taskID := tasks[0].ID

	srv, err := New(s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/task/"+taskID, nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("task detail status=%d body=%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	// Both revisions rendered, each linked to its own snapshot.
	for _, want := range []string{
		"Revisions",
		"/worktree/" + w.ID + "/snapshot/" + res1.SnapshotID,
		"/worktree/" + w.ID + "/snapshot/" + res2.SnapshotID,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("task page missing %q", want)
		}
	}
}

func TestTaskSourcePageRendersLineContext(t *testing.T) {
	srv, s, w := testServer(t)
	tasks, _ := s.TasksByWorktree(context.Background(), w.ID)
	var task store.Task
	for _, candidate := range tasks {
		if candidate.SourceFile != "" && candidate.SourceLine > 0 {
			task = candidate
			break
		}
	}
	if task.ID == "" {
		t.Fatal("expected seeded task with source")
	}

	r := httptest.NewRequest("GET", "/task/"+task.ID+"/source", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("source status=%d body=%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"source-context",
		"line-highlight",
		"L" + strconv.Itoa(task.SourceLine),
		task.SourceFile,
		task.CurrentTitle,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("source page missing %q", want)
		}
	}
}

func TestOverrideRejectsCrossOrigin(t *testing.T) {
	srv, s, w := testServer(t)
	tasks, _ := s.TasksByWorktree(context.Background(), w.ID)
	if len(tasks) == 0 {
		t.Fatal("expected seeded tasks")
	}
	taskID := tasks[0].ID
	form := url.Values{}
	form.Set("kind", "mark_dropped")
	r := httptest.NewRequest("POST", "/override/"+taskID, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://attacker.example")
	r.RemoteAddr = "203.0.113.9:1234" // non-loopback
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin override status=%d want 403", rec.Code)
	}
	overs, _ := s.OverridesForTask(context.Background(), taskID)
	if len(overs) != 0 {
		t.Errorf("cross-origin POST still wrote %d overrides", len(overs))
	}
}

func TestOverrideRejectsSelfReference(t *testing.T) {
	srv, s, w := testServer(t)
	tasks, _ := s.TasksByWorktree(context.Background(), w.ID)
	if len(tasks) == 0 {
		t.Fatal("expected seeded tasks")
	}
	taskID := tasks[0].ID

	form := url.Values{}
	form.Set("kind", "mark_same")
	form.Set("related_task_id", taskID) // same as path param — nonsense
	r := httptest.NewRequest("POST", "/override/"+taskID, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://"+r.Host)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("self-ref override status=%d want 400", rec.Code)
	}
	overs, _ := s.OverridesForTask(context.Background(), taskID)
	if len(overs) != 0 {
		t.Errorf("self-ref POST still wrote %d overrides", len(overs))
	}
}

func TestOverrideMarkSameSetsSupersedes(t *testing.T) {
	srv, s, w := testServer(t)
	ctx := context.Background()
	tasks, _ := s.TasksByWorktree(ctx, w.ID)
	if len(tasks) < 2 {
		t.Fatal("need at least two tasks")
	}
	dupID, canonID := tasks[0].ID, tasks[1].ID

	form := url.Values{}
	form.Set("kind", "mark_same")
	form.Set("related_task_id", canonID)
	r := httptest.NewRequest("POST", "/override/"+dupID, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://"+r.Host)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("override status=%d want 303", rec.Code)
	}
	got, err := s.TaskByID(ctx, dupID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Supersedes != canonID {
		t.Errorf("supersedes=%q want %q", got.Supersedes, canonID)
	}
	if got.Status != "dropped" {
		t.Errorf("status=%q want dropped", got.Status)
	}
}

func TestArchivePage(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/archive", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("archive status=%d body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "Archive") {
		t.Error("archive page missing heading")
	}
}

func TestStaticCSSServed(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/static/style.css", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("css status=%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/css" {
		t.Errorf("wrong content-type: %q", ct)
	}
}

func TestHealthz(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Errorf("healthz failed: code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestFaviconDoesNotPolluteConsole(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/favicon.ico", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("favicon status=%d want 204", rec.Code)
	}
}

func TestReasonFor(t *testing.T) {
	cases := []struct {
		name string
		task store.Task
		want string
	}{
		{
			name: "lost task",
			task: store.Task{Status: "lost", Confidence: 1.0},
			want: "missing from recent plan updates",
		},
		{
			name: "low-confidence rename carries previous title",
			task: store.Task{
				Status:       "in_progress",
				Confidence:   0.62,
				CurrentTitle: "Ship feature X",
				Aliases:      []string{"Prototype feature X"},
			},
			want: `renamed from "Prototype feature X" @ 0.62`,
		},
		{
			name: "low-confidence split child",
			task: store.Task{Status: "pending", Confidence: 0.55, SplitFrom: []string{"parent-1"}},
			want: "split from earlier task @ 0.55",
		},
		{
			name: "low-confidence merge child",
			task: store.Task{
				Status:     "pending",
				Confidence: 0.5,
				MergedFrom: []string{"a", "b", "c"},
			},
			want: "merged from 3 earlier tasks @ 0.50",
		},
		{
			name: "low-confidence fallback",
			task: store.Task{Status: "pending", Confidence: 0.3},
			want: "low confidence @ 0.30",
		},
		{
			name: "healthy task has no reason",
			task: store.Task{Status: "in_progress", Confidence: 1.0},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reasonFor(tc.task); got != tc.want {
				t.Errorf("reasonFor = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestAttentionCardShowsReason(t *testing.T) {
	srv, s, wt := testServer(t)
	ctx := context.Background()

	now := time.Now().UTC()
	lost := store.Task{
		ID:           "t-lost",
		WorktreeID:   wt.ID,
		ProjectID:    wt.ProjectID,
		CurrentTitle: "Disappeared task",
		Status:       "lost",
		Confidence:   1.0,
		SourceFile:   "plan.md",
		FirstSeenAt:  now,
		LastSeenAt:   now,
	}
	renamed := store.Task{
		ID:           "t-renamed",
		WorktreeID:   wt.ID,
		ProjectID:    wt.ProjectID,
		CurrentTitle: "New title",
		Aliases:      []string{"Old title"},
		Status:       "in_progress",
		Confidence:   0.55,
		SourceFile:   "plan.md",
		FirstSeenAt:  now,
		LastSeenAt:   now,
	}
	if err := s.UpsertTask(ctx, lost); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTask(ctx, renamed); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()

	for _, want := range []string{
		`class="card-reason"`,
		"missing from recent plan updates",
		`renamed from &#34;Old title&#34; @ 0.55`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("kanban body missing %q", want)
		}
	}

	// Task detail page must surface the same reason near the meta grid so the
	// override form has context one scroll away.
	rDetail := httptest.NewRequest("GET", "/task/"+renamed.ID, nil)
	recDetail := httptest.NewRecorder()
	srv.Routes().ServeHTTP(recDetail, rDetail)
	if recDetail.Code != 200 {
		t.Fatalf("task detail status=%d", recDetail.Code)
	}
	detailBody := recDetail.Body.String()
	for _, want := range []string{
		`class="task-reason"`,
		`renamed from &#34;Old title&#34; @ 0.55`,
	} {
		if !strings.Contains(detailBody, want) {
			t.Errorf("task detail body missing %q", want)
		}
	}
}

func TestBucketizeClassification(t *testing.T) {
	now := time.Now()
	cards := []card{
		{Task: store.Task{Status: "in_progress", LastSeenAt: now, Confidence: 1.0}},
		{Task: store.Task{Status: "done", LastSeenAt: now, Confidence: 1.0}},
		{Task: store.Task{Status: "lost", LastSeenAt: now, Confidence: 1.0}},
		{Task: store.Task{Status: "pending", LastSeenAt: now.Add(-2 * time.Hour), Confidence: 1.0}},
		{Task: store.Task{Status: "pending", LastSeenAt: now, Confidence: 1.0}},
		{Task: store.Task{Status: "pending", LastSeenAt: now, Confidence: 0.5}},
	}
	cols := bucketize(cards, now)
	counts := map[string]int{}
	for _, c := range cols {
		counts[c.Key] = len(c.Cards)
	}
	if counts["active"] != 1 {
		t.Errorf("active=%d want 1", counts["active"])
	}
	if counts["done"] != 1 {
		t.Errorf("done=%d want 1", counts["done"])
	}
	if counts["attention"] != 2 { // lost + low-confidence
		t.Errorf("attention=%d want 2", counts["attention"])
	}
	if counts["stalled"] != 1 {
		t.Errorf("stalled=%d want 1", counts["stalled"])
	}
	if counts["inbox"] != 1 {
		t.Errorf("inbox=%d want 1", counts["inbox"])
	}
}
