package ingest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chris/thin-observer/internal/lineage"
	"github.com/chris/thin-observer/internal/parser"
	"github.com/chris/thin-observer/internal/store"
)

func setupStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seedWorktree(t *testing.T, s *store.Store) store.Worktree {
	t.Helper()
	ctx := context.Background()
	proj := store.Project{ID: "p1", Name: "demo", RootPath: "/tmp/demo"}
	if err := s.UpsertProject(ctx, proj); err != nil {
		t.Fatal(err)
	}
	w := store.Worktree{ID: "w1", ProjectID: "p1", Name: "feature", Path: "/tmp/demo/feature"}
	if err := s.UpsertWorktree(ctx, w); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestApplyFirstTimeCreatesTasks(t *testing.T) {
	s := setupStore(t)
	w := seedWorktree(t, s)
	in := New(s)

	doc, err := parser.ParseFile(filepath.Join("..", "..", "testdata", "plans", "planning-with-files.md"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := in.Apply(context.Background(), w, doc)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 14 {
		t.Fatalf("created = %d, want 14", res.Created)
	}
	tasks, err := s.TasksByWorktree(context.Background(), w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 14 {
		t.Fatalf("tasks = %d, want 14", len(tasks))
	}
}

func TestApplyUnchanged(t *testing.T) {
	s := setupStore(t)
	w := seedWorktree(t, s)
	in := New(s)

	doc, _ := parser.ParseFile(filepath.Join("..", "..", "testdata", "plans", "planning-with-files.md"))
	_, _ = in.Apply(context.Background(), w, doc)
	// Second apply with same hash should be a no-op.
	res, err := in.Apply(context.Background(), w, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unchanged {
		t.Fatalf("expected Unchanged, got %+v", res)
	}
}

func TestApplySameRawHashButDifferentParsedTasksReingests(t *testing.T) {
	s := setupStore(t)
	w := seedWorktree(t, s)
	in := New(s)

	first := &parser.PlanDoc{
		SourceFile: "/tmp/task_plan.md",
		RawHash:    "same-content-hash",
		Phases: []parser.Phase{
			{Name: "Current TODO", Status: "pending", Line: 1},
		},
	}
	if _, err := in.Apply(context.Background(), w, first); err != nil {
		t.Fatal(err)
	}

	second := &parser.PlanDoc{
		SourceFile: "/tmp/task_plan.md",
		RawHash:    "same-content-hash",
		Phases: []parser.Phase{
			{
				Name:   "Current TODO",
				Status: "pending",
				Line:   1,
				Tasks: []parser.TaskItem{
					{Title: "Backfilled parser task", Status: "pending", Line: 3},
				},
			},
		},
	}
	res, err := in.Apply(context.Background(), w, second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Unchanged {
		t.Fatalf("expected reingest when parsed phases differ, got unchanged")
	}
	if res.Created != 1 {
		t.Fatalf("created = %d, want 1", res.Created)
	}
}

func TestApplySecondTimeExactMatch(t *testing.T) {
	s := setupStore(t)
	w := seedWorktree(t, s)
	in := New(s)

	// Use a tmp file so we can mutate it between applies.
	tmp := filepath.Join(t.TempDir(), "plan.md")
	writeFile(t, tmp, `# Plan

## Phase 1 [in_progress]
- [x] done task
- [ ] pending task
`)
	doc1, _ := parser.ParseFile(tmp)
	_, _ = in.Apply(context.Background(), w, doc1)

	// Now modify: mark pending as done, add one new task.
	writeFile(t, tmp, `# Plan

## Phase 1 [in_progress]
- [x] done task
- [x] pending task
- [ ] brand new task
`)
	doc2, _ := parser.ParseFile(tmp)
	res, err := in.Apply(context.Background(), w, doc2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 1 {
		t.Fatalf("created = %d, want 1", res.Created)
	}
	if res.Updated != 2 {
		t.Fatalf("updated = %d, want 2", res.Updated)
	}
}

func TestApplyMarksLostAfterTwoMissingRevs(t *testing.T) {
	s := setupStore(t)
	w := seedWorktree(t, s)
	in := New(s)

	tmp := filepath.Join(t.TempDir(), "plan.md")
	writeFile(t, tmp, "## Phase 1\n- [ ] alpha\n- [ ] beta\n")
	d1, _ := parser.ParseFile(tmp)
	_, _ = in.Apply(context.Background(), w, d1)

	// Remove beta.
	writeFile(t, tmp, "## Phase 1\n- [ ] alpha\n")
	d2, _ := parser.ParseFile(tmp)
	_, _ = in.Apply(context.Background(), w, d2)

	// Still missing.
	writeFile(t, tmp, "## Phase 1\n- [ ] alpha\n## Phase 2\n- [ ] other\n")
	d3, _ := parser.ParseFile(tmp)
	res, err := in.Apply(context.Background(), w, d3)
	if err != nil {
		t.Fatal(err)
	}
	if res.Lost != 1 {
		t.Fatalf("lost = %d, want 1", res.Lost)
	}
	tasks, _ := s.TasksByWorktree(context.Background(), w.ID)
	foundLost := false
	for _, tk := range tasks {
		if tk.CurrentTitle == "beta" && tk.Status == "lost" {
			foundLost = true
		}
	}
	if !foundLost {
		t.Fatal("expected beta to be marked lost")
	}
}

func TestApplyWithInferrer_DetectsSplit(t *testing.T) {
	s := setupStore(t)
	w := seedWorktree(t, s)
	in := New(s)
	in.Inferrer = lineage.New()

	tmp := filepath.Join(t.TempDir(), "plan.md")
	writeFile(t, tmp, `## Auth
- [ ] Implement authentication and authorization
`)
	d1, _ := parser.ParseFile(tmp)
	if _, err := in.Apply(context.Background(), w, d1); err != nil {
		t.Fatal(err)
	}

	writeFile(t, tmp, `## Auth
- [ ] Implement authentication
- [ ] Implement authorization
`)
	d2, _ := parser.ParseFile(tmp)
	res, err := in.Apply(context.Background(), w, d2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Split != 1 {
		t.Fatalf("split = %d, want 1 (result=%+v)", res.Split, res)
	}
	tasks, _ := s.TasksByWorktree(context.Background(), w.ID)
	var children, parents int
	for _, tk := range tasks {
		if len(tk.SplitFrom) > 0 {
			children++
		}
		if tk.CurrentTitle == "Implement authentication and authorization" {
			parents++
			if tk.Status != "dropped" {
				t.Errorf("expected umbrella parent status=dropped, got %s", tk.Status)
			}
		}
	}
	if children != 2 {
		t.Errorf("expected 2 children with SplitFrom, got %d", children)
	}
	if parents != 1 {
		t.Errorf("expected 1 umbrella parent, got %d", parents)
	}
}

func TestApplyWithInferrer_DetectsRename(t *testing.T) {
	s := setupStore(t)
	w := seedWorktree(t, s)
	in := New(s)
	in.Inferrer = lineage.New()

	tmp := filepath.Join(t.TempDir(), "plan.md")
	writeFile(t, tmp, "## Stage 1\n- [ ] Install project dependencies\n")
	d1, _ := parser.ParseFile(tmp)
	_, _ = in.Apply(context.Background(), w, d1)

	writeFile(t, tmp, "## Stage 1\n- [ ] Install project dependency\n")
	d2, _ := parser.ParseFile(tmp)
	res, err := in.Apply(context.Background(), w, d2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Renamed != 1 {
		t.Fatalf("renamed = %d, want 1", res.Renamed)
	}
	tasks, _ := s.TasksByWorktree(context.Background(), w.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task after rename, got %d", len(tasks))
	}
	tk := tasks[0]
	if tk.CurrentTitle != "Install project dependency" {
		t.Errorf("expected new title, got %q", tk.CurrentTitle)
	}
	if !containsStr(tk.Aliases, "Install project dependencies") {
		t.Errorf("expected old title in aliases, got %v", tk.Aliases)
	}
}

func TestApplyWritesPlanDocAndLinks(t *testing.T) {
	s := setupStore(t)
	w := seedWorktree(t, s)
	in := New(s)

	// Two plan files in the same worktree: task_plan.md that links to a
	// detailed plan under docs/plans/. After ingesting both, the resolver
	// should wire the link's to_plan_id to the detailed plan's ID.
	root := t.TempDir()
	w.Path = root
	_ = s.UpsertWorktree(context.Background(), w)
	if err := os.MkdirAll(filepath.Join(root, "docs/plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	taskPlan := filepath.Join(root, "task_plan.md")
	phasePlan := filepath.Join(root, "docs/plans/phase27.md")
	writeFile(t, taskPlan, `# Roadmap

## TODO

- [ ] See [Phase 27 plan](docs/plans/phase27.md)
`)
	writeFile(t, phasePlan, `---
title: Phase 27 Closeout
---

## Phase 27

- [ ] Wire action queue
`)

	d1, err := parser.ParseFile(taskPlan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Apply(context.Background(), w, d1); err != nil {
		t.Fatal(err)
	}
	d2, err := parser.ParseFile(phasePlan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Apply(context.Background(), w, d2); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	docs, err := s.PlanDocsByWorktree(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("plan_docs = %d, want 2 (%+v)", len(docs), docs)
	}

	var rootDoc, phaseDoc *store.PlanDoc
	for i := range docs {
		d := &docs[i]
		switch d.SourceFile {
		case taskPlan:
			rootDoc = d
		case phasePlan:
			phaseDoc = d
		}
	}
	if rootDoc == nil || phaseDoc == nil {
		t.Fatalf("expected both plans; got %+v", docs)
	}
	if rootDoc.Kind != "task_plan" {
		t.Errorf("root kind = %q, want task_plan", rootDoc.Kind)
	}
	if phaseDoc.Kind != "detailed_plan" {
		t.Errorf("phase kind = %q, want detailed_plan", phaseDoc.Kind)
	}
	if phaseDoc.Title != "Phase 27 Closeout" {
		t.Errorf("phase title = %q, want frontmatter value", phaseDoc.Title)
	}

	links, err := s.LinksFrom(ctx, rootDoc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 {
		t.Fatalf("links = %d, want 1 (%+v)", len(links), links)
	}
	l := links[0]
	if l.ToSourceFile != phasePlan {
		t.Errorf("to_source_file = %q, want %q", l.ToSourceFile, phasePlan)
	}
	if l.ToPlanID != phaseDoc.ID {
		t.Errorf("to_plan_id = %q, want %q (unresolved after both plans ingested)", l.ToPlanID, phaseDoc.ID)
	}
	if l.Label != "Phase 27 plan" {
		t.Errorf("label = %q", l.Label)
	}

	// Re-ingest the root doc: ID must be stable, links must be replaced
	// (not duplicated).
	if _, err := in.Apply(context.Background(), w, d1); err != nil {
		t.Fatal(err)
	}
	rootDoc2, err := s.PlanDocByWorktreeAndFile(ctx, w.ID, taskPlan)
	if err != nil {
		t.Fatal(err)
	}
	if rootDoc2.ID != rootDoc.ID {
		t.Errorf("plan_doc ID mutated across re-ingest: %q vs %q", rootDoc2.ID, rootDoc.ID)
	}
	links2, _ := s.LinksFrom(ctx, rootDoc.ID)
	if len(links2) != 1 {
		t.Errorf("re-ingest duplicated links: got %d, want 1", len(links2))
	}
}

func TestApplyStoresCommitSHA(t *testing.T) {
	s := setupStore(t)
	w := seedWorktree(t, s)
	in := New(s)
	const fakeSHA = "0123456789abcdef0123456789abcdef01234567"
	in.headSHA = func(ctx context.Context, path string) (string, error) {
		return fakeSHA, nil
	}

	tmp := filepath.Join(t.TempDir(), "plan.md")
	writeFile(t, tmp, "## Phase 1\n- [ ] hello\n")
	doc, err := parser.ParseFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	res, err := in.Apply(context.Background(), w, doc)
	if err != nil {
		t.Fatal(err)
	}

	snap, err := s.LatestSnapshot(context.Background(), w.ID, doc.SourceFile)
	if err != nil {
		t.Fatal(err)
	}
	if snap == nil {
		t.Fatalf("no snapshot stored (res=%+v)", res)
	}
	if snap.CommitSHA != fakeSHA {
		t.Errorf("snapshot commit_sha = %q, want %q", snap.CommitSHA, fakeSHA)
	}
}

func TestApplyCommitSHAErrorIsNonFatal(t *testing.T) {
	s := setupStore(t)
	w := seedWorktree(t, s)
	in := New(s)
	in.headSHA = func(ctx context.Context, path string) (string, error) {
		return "", fmt.Errorf("simulated non-git worktree")
	}

	tmp := filepath.Join(t.TempDir(), "plan.md")
	writeFile(t, tmp, "## Phase 1\n- [ ] hello\n")
	doc, _ := parser.ParseFile(tmp)
	if _, err := in.Apply(context.Background(), w, doc); err != nil {
		t.Fatalf("Apply returned error for non-git worktree: %v", err)
	}
	snap, _ := s.LatestSnapshot(context.Background(), w.ID, doc.SourceFile)
	if snap == nil {
		t.Fatal("expected snapshot even when commit SHA lookup fails")
	}
	if snap.CommitSHA != "" {
		t.Errorf("snapshot commit_sha = %q, want empty", snap.CommitSHA)
	}
}

func TestApplyWritesTaskRevisions(t *testing.T) {
	s := setupStore(t)
	w := seedWorktree(t, s)
	in := New(s)

	tmp := filepath.Join(t.TempDir(), "plan.md")
	writeFile(t, tmp, "## Phase 1\n- [ ] alpha\n- [ ] beta\n")
	d1, err := parser.ParseFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	res1, err := in.Apply(context.Background(), w, d1)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	r1, err := s.TaskRevisionsBySnapshot(ctx, res1.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if len(r1) != 2 {
		t.Fatalf("revisions after first ingest = %d, want 2", len(r1))
	}
	for _, rev := range r1 {
		if rev.ProjectID != w.ProjectID {
			t.Errorf("revision project_id = %q, want %q", rev.ProjectID, w.ProjectID)
		}
		if rev.Status != "pending" {
			t.Errorf("revision %s status = %q, want pending", rev.Title, rev.Status)
		}
	}

	// Second ingest: mark alpha done, keep beta pending. Both live tasks get
	// a fresh revision; the snapshot-1 revisions must remain untouched so the
	// time machine can replay the earlier state.
	writeFile(t, tmp, "## Phase 1\n- [x] alpha\n- [ ] beta\n")
	d2, _ := parser.ParseFile(tmp)
	res2, err := in.Apply(context.Background(), w, d2)
	if err != nil {
		t.Fatal(err)
	}
	if res2.SnapshotID == res1.SnapshotID {
		t.Fatalf("expected a new snapshot id on second ingest")
	}

	r2, err := s.TaskRevisionsBySnapshot(ctx, res2.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if len(r2) != 2 {
		t.Fatalf("revisions after second ingest = %d, want 2", len(r2))
	}
	var r2Alpha *store.TaskRevision
	for i := range r2 {
		if r2[i].Title == "alpha" {
			r2Alpha = &r2[i]
		}
	}
	if r2Alpha == nil || r2Alpha.Status != "done" {
		t.Fatalf("expected alpha revision 2 to be done, got %+v", r2Alpha)
	}

	// The snapshot-1 alpha revision is still pending.
	r1Reread, _ := s.TaskRevisionsBySnapshot(ctx, res1.SnapshotID)
	var r1Alpha *store.TaskRevision
	for i := range r1Reread {
		if r1Reread[i].Title == "alpha" {
			r1Alpha = &r1Reread[i]
		}
	}
	if r1Alpha == nil || r1Alpha.Status != "pending" {
		t.Errorf("snapshot-1 alpha changed under us: %+v", r1Alpha)
	}

	// History for alpha should be length 2 in recorded order.
	hist, _ := s.TaskHistoryByTask(ctx, r1Alpha.TaskID)
	if len(hist) != 2 {
		t.Fatalf("task history = %d, want 2", len(hist))
	}
	if hist[0].Status != "pending" || hist[1].Status != "done" {
		t.Errorf("history statuses = [%s, %s], want [pending, done]", hist[0].Status, hist[1].Status)
	}
}

func TestPlanDocKindClassification(t *testing.T) {
	cases := map[string]string{
		"/repo/task_plan.md":                    "task_plan",
		"/repo/plan.md":                         "task_plan",
		"/repo/TODO.md":                         "task_plan",
		"/repo/progress.md":                     "progress",
		"/repo/findings.md":                     "findings",
		"/repo/docs/plans/2026-04-21-foo.md":    "detailed_plan",
		"/repo/plans/bar.md":                    "detailed_plan",
		"/repo/misc.md":                         "unknown",
	}
	for path, want := range cases {
		if got := planDocKind(path); got != want {
			t.Errorf("planDocKind(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestResolveLinkTarget(t *testing.T) {
	const root = "/repo/docs/plans/a.md"
	cases := map[string]string{
		"b.md":                    "/repo/docs/plans/b.md",
		"../plans/b.md":           "/repo/docs/plans/b.md",
		"/repo/task_plan.md":      "/repo/task_plan.md",
		"./sibling.md":            "/repo/docs/plans/sibling.md",
		"b.md#heading":            "/repo/docs/plans/b.md",
	}
	for in, want := range cases {
		if got := resolveLinkTarget(root, in); got != want {
			t.Errorf("resolveLinkTarget(%q, %q) = %q, want %q", root, in, got, want)
		}
	}
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
