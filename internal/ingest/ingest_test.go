package ingest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
