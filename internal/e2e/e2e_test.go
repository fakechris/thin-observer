// Package e2e wires the full thin-observer stack together against a real
// temp git repo and in-process HTTP server, so we catch regressions that
// would slip through per-package tests. Skips automatically when git is
// unavailable — we'd rather log-skip than silently no-op.
package e2e

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chris/thin-observer/internal/ingest"
	"github.com/chris/thin-observer/internal/parser"
	"github.com/chris/thin-observer/internal/store"
	"github.com/chris/thin-observer/internal/watcher"
	"github.com/chris/thin-observer/internal/web"
)

func TestMultiPlanViewAndTimeMachine(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not installed — skipping e2e test: %v", err)
	}

	root := t.TempDir()
	initGitRepo(t, root)

	taskPlan := filepath.Join(root, "task_plan.md")
	phasePlan := filepath.Join(root, "docs/plans/phase27.md")
	if err := os.MkdirAll(filepath.Dir(phasePlan), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, taskPlan, `# Roadmap

## TODO

- [ ] Ship observer
- [ ] See [Phase 27 plan](docs/plans/phase27.md)
`)
	writeFile(t, phasePlan, `---
title: Phase 27 Closeout
---

## Tasks

1. Wire action queue
2. Drain legacy jobs
`)
	gitCommitAll(t, root, "seed plans")

	// Open a real store and drive ingest the same way watch --once would:
	// project/worktree registration + parse every file the watcher finds.
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	proj := store.Project{ID: "p1", Name: "demo", RootPath: root}
	if err := s.UpsertProject(ctx, proj); err != nil {
		t.Fatal(err)
	}
	wt := store.Worktree{ID: "w1", ProjectID: "p1", Name: "main", Path: root}
	if err := s.UpsertWorktree(ctx, wt); err != nil {
		t.Fatal(err)
	}

	in := ingest.New(s)
	ingestAll(t, ctx, in, wt)

	// Acceptance: two plan_docs, one resolved plan_link, partitioned tasks,
	// snapshot.commit_sha matches HEAD, task_revision count == live tasks.
	docs, err := s.PlanDocsByWorktree(ctx, wt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("plan_docs = %d, want 2 (%+v)", len(docs), docs)
	}
	var taskDoc, phaseDoc *store.PlanDoc
	for i := range docs {
		d := &docs[i]
		switch d.SourceFile {
		case taskPlan:
			taskDoc = d
		case phasePlan:
			phaseDoc = d
		}
	}
	if taskDoc == nil || phaseDoc == nil {
		t.Fatalf("plans missing: %+v", docs)
	}
	links, err := s.LinksFrom(ctx, taskDoc.ID)
	if err != nil {
		t.Fatalf("LinksFrom: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("links from root = %d, want 1", len(links))
	}
	if links[0].ToPlanID != phaseDoc.ID {
		t.Fatalf("link unresolved: to_plan_id=%q, want %q", links[0].ToPlanID, phaseDoc.ID)
	}

	// Task partition: 2 tasks on root, 2 on phase plan.
	allTasks, err := s.TasksByWorktree(ctx, wt.ID)
	if err != nil {
		t.Fatalf("TasksByWorktree: %v", err)
	}
	byFile := map[string]int{}
	for _, t := range allTasks {
		byFile[t.SourceFile]++
	}
	if byFile[taskPlan] != 2 {
		t.Errorf("task_plan tasks = %d, want 2", byFile[taskPlan])
	}
	if byFile[phasePlan] != 2 {
		t.Errorf("phase_plan tasks = %d, want 2", byFile[phasePlan])
	}

	// commit_sha on latest snapshot matches HEAD.
	head := gitHead(t, root)
	taskSnap, err := s.LatestSnapshot(ctx, wt.ID, taskPlan)
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if taskSnap == nil || taskSnap.CommitSHA != head {
		t.Fatalf("task_plan snapshot commit_sha = %q, want %q", snapSHA(taskSnap), head)
	}

	// task_revision count per snapshot matches live tasks in that file.
	revs, err := s.TaskRevisionsBySnapshot(ctx, taskSnap.ID)
	if err != nil {
		t.Fatalf("TaskRevisionsBySnapshot: %v", err)
	}
	if len(revs) != 2 {
		t.Errorf("revisions for task_plan snapshot = %d, want 2", len(revs))
	}

	// Record the earliest revisions so we can assert they stay frozen after
	// the second ingest (the regression the whole time machine exists for).
	preEditTitles := map[string]string{}
	for _, rev := range revs {
		preEditTitles[rev.TaskID] = rev.Title
	}

	// Stand up a real in-process HTTP server and hit every route.
	srv, err := web.New(s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	client := ts.Client()

	kanbanURL := ts.URL + "/?project=" + proj.ID
	assertContains(t, client, kanbanURL, []string{"Ship observer", "Wire action queue", "task_plan.md", "phase27.md"})

	// Filter to root plan — phase tasks must disappear.
	planFiltered := kanbanURL + "&plan=" + taskDoc.ID
	bodyFiltered := mustGet(t, client, planFiltered)
	if !strings.Contains(bodyFiltered, "Ship observer") {
		t.Error("plan filter dropped root task")
	}
	if strings.Contains(bodyFiltered, "Wire action queue") {
		t.Error("plan filter did not hide phase task")
	}

	assertContains(t, client, ts.URL+"/plan/"+taskDoc.ID, []string{"Phase 27 Closeout", "/plan/" + phaseDoc.ID})

	// Task source page regression guard.
	var srcTask store.Task
	for _, tk := range allTasks {
		if tk.SourceFile == taskPlan {
			srcTask = tk
			break
		}
	}
	if srcTask.ID == "" {
		t.Fatal("no task to probe source for")
	}
	assertContains(t, client, ts.URL+"/task/"+srcTask.ID+"/source", []string{"source-context"})

	// Snapshot detail: commit SHA + phases + revisions.
	assertContains(t, client, ts.URL+"/snapshot/"+taskSnap.ID, []string{
		taskSnap.ID, head, "phases_json", "Task revisions",
	})

	// Timeline + historical board.
	assertContains(t, client, ts.URL+"/worktree/"+wt.ID+"/timeline", []string{taskSnap.ID, "task_plan.md"})
	assertContains(t, client, ts.URL+"/worktree/"+wt.ID+"/snapshot/"+taskSnap.ID, []string{"Ship observer"})

	// --- Mutability regression: re-ingest with edits, ensure history frozen.
	writeFile(t, taskPlan, `# Roadmap

## TODO

- [ ] Ship observer v2
- [ ] See [Phase 27 plan](docs/plans/phase27.md)
- [ ] Newly added item
`)
	gitCommitAll(t, root, "edit root plan")

	ingestAll(t, ctx, in, wt)

	// Live task should now be "Ship observer v2".
	afterTasks, err := s.TasksByWorktree(ctx, wt.ID)
	if err != nil {
		t.Fatalf("TasksByWorktree post-edit: %v", err)
	}
	var liveRenamed, liveNew bool
	for _, tk := range afterTasks {
		if tk.CurrentTitle == "Ship observer v2" {
			liveRenamed = true
		}
		if tk.CurrentTitle == "Newly added item" {
			liveNew = true
		}
	}
	if !liveRenamed {
		t.Error("expected renamed live task 'Ship observer v2' after re-ingest")
	}
	if !liveNew {
		t.Error("expected new live task after re-ingest")
	}

	// But the first snapshot's revisions must be unchanged — this is the
	// core guarantee Phase 6 exists to provide.
	revsAgain, err := s.TaskRevisionsBySnapshot(ctx, taskSnap.ID)
	if err != nil {
		t.Fatalf("TaskRevisionsBySnapshot post-edit: %v", err)
	}
	frozenTitles := map[string]string{}
	for _, r := range revsAgain {
		frozenTitles[r.TaskID] = r.Title
	}
	for id, want := range preEditTitles {
		if got := frozenTitles[id]; got != want {
			t.Errorf("snapshot-1 revision for task %s mutated: got %q, want %q", id, got, want)
		}
	}

	// The historical board should still render the pre-edit title.
	historical := mustGet(t, client, ts.URL+"/worktree/"+wt.ID+"/snapshot/"+taskSnap.ID)
	if !strings.Contains(historical, "Ship observer") || strings.Contains(historical, "Ship observer v2") {
		t.Errorf("historical board reflects live state instead of frozen revision")
	}
	if strings.Contains(historical, "Newly added item") {
		t.Errorf("historical board shows task that didn't exist yet at snapshot time")
	}
}

// ingestAll parses and applies every plan file the watcher would surface on
// a fresh watch --once pass.
func ingestAll(t *testing.T, ctx context.Context, in *ingest.Ingester, wt store.Worktree) {
	t.Helper()
	files := watcher.ListKnownPlanFiles(wt.Path)
	if len(files) == 0 {
		t.Fatal("ListKnownPlanFiles returned 0 — discovery broken")
	}
	for _, f := range files {
		doc, err := parser.ParseFile(f)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		if _, err := in.Apply(ctx, wt, doc); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "--quiet")
	run("-c", "user.email=t@example.com", "-c", "user.name=Tester", "commit", "--allow-empty", "-m", "init")
}

func gitCommitAll(t *testing.T, dir, msg string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "add", "-A")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=Tester", "commit", "-m", msg)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

func gitHead(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func mustGet(t *testing.T, client *http.Client, url string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest %s: %v", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status=%d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

func assertContains(t *testing.T, client *http.Client, url string, wants []string) {
	t.Helper()
	body := mustGet(t, client, url)
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("GET %s missing substring %q", url, w)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func snapSHA(s *store.Snapshot) string {
	if s == nil {
		return "<nil>"
	}
	return s.CommitSHA
}
