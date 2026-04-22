package watcher

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// makeTree creates files inside root. Directories are created implicitly.
func makeTree(t *testing.T, root string, files []string) {
	t.Helper()
	for _, rel := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte("# placeholder\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
}

func TestPlanDirsIncludesExistingPlanSubdirs(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, []string{
		"task_plan.md",
		"docs/plans/phase27.md",
		"plans/local.md",
		".workgraph/plans/graph.md",
		"docs/random.md", // must not cause docs/ itself to be returned
	})

	got := planDirs(root)
	sort.Strings(got)

	want := []string{
		root,
		filepath.Join(root, ".workgraph/plans"),
		filepath.Join(root, "docs/plans"),
		filepath.Join(root, "plans"),
	}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("planDirs len = %d, want %d\n got: %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("planDirs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPlanDirsSkipsMissingSubdirs(t *testing.T) {
	root := t.TempDir()
	// Only the root exists; no plan subdirs at all.
	got := planDirs(root)
	if len(got) != 1 || got[0] != root {
		t.Errorf("planDirs with no subdirs = %v, want [%q]", got, root)
	}
}

func TestListKnownPlanFilesIncludesDocsPlans(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, []string{
		"task_plan.md",
		"docs/plans/phase27.md",
		"docs/random.md", // must be excluded
		"plans/local.md",
		".workgraph/plans/graph.md",
	})

	got := ListKnownPlanFiles(root)
	sort.Strings(got)

	// Verify docs/random.md is excluded — the single most important assertion
	// for this phase.
	for _, p := range got {
		if filepath.Base(p) == "random.md" {
			t.Fatalf("ListKnownPlanFiles unexpectedly included %q (docs/random.md must be filtered)", p)
		}
	}

	wantBasenames := map[string]string{
		"task_plan.md": filepath.Join(root, "task_plan.md"),
		"phase27.md":   filepath.Join(root, "docs/plans/phase27.md"),
		"local.md":     filepath.Join(root, "plans/local.md"),
		"graph.md":     filepath.Join(root, ".workgraph/plans/graph.md"),
	}
	for base, absPath := range wantBasenames {
		found := false
		for _, p := range got {
			if p == absPath {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ListKnownPlanFiles missing %s (%s); got: %v", base, absPath, got)
		}
	}
}

// TestRemoveEmitsRemovedKind drives the watcher end-to-end: write a plan file,
// drain the write event, remove the file, and assert the next emitted event
// carries Kind == "removed". This is what the daemon consumes to call
// MarkPlanDocsMissing in real time instead of waiting for startup reconcile.
func TestRemoveEmitsRemovedKind(t *testing.T) {
	root := t.TempDir()
	plan := filepath.Join(root, "task_plan.md")
	if err := os.WriteFile(plan, []byte("# initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w, err := New(logger)
	if err != nil {
		t.Fatal(err)
	}
	// Speed the debounce window so the test completes quickly.
	w.period = 50 * time.Millisecond
	w.maxDelay = 200 * time.Millisecond
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Errorf("watcher.Close: %v", err)
		}
	})

	if err := w.AddWorktree(root); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Touch the file to trigger one write event; drain it so the removal
	// event below is what the next receive observes.
	if err := os.WriteFile(plan, []byte("# updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-w.Events:
		if ev.Kind != "write" {
			t.Fatalf("expected write kind, got %q", ev.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial write event")
	}

	if err := os.Remove(plan); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-w.Events:
		if ev.Kind != "removed" {
			t.Errorf("after os.Remove, got Kind=%q want %q", ev.Kind, "removed")
		}
		if ev.File != plan {
			t.Errorf("ev.File = %q want %q", ev.File, plan)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for removed event")
	}
}

// TestRenameAwayEmitsRemovedKind covers the case where a plan file is renamed
// out of the watched tree. On macOS this fires fsnotify.Rename (not Remove),
// so the debounce `removed` flag stays false — the flush path must re-stat and
// classify the event as removed. Otherwise the daemon would try ParseFile on a
// now-missing path and never call MarkPlanDocsMissing.
func TestRenameAwayEmitsRemovedKind(t *testing.T) {
	root := t.TempDir()
	plan := filepath.Join(root, "task_plan.md")
	if err := os.WriteFile(plan, []byte("# initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w, err := New(logger)
	if err != nil {
		t.Fatal(err)
	}
	w.period = 50 * time.Millisecond
	w.maxDelay = 200 * time.Millisecond
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Errorf("watcher.Close: %v", err)
		}
	})

	if err := w.AddWorktree(root); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Rename the plan out of the watched tree. Destination sits outside `root`
	// so no Create event fires inside the watched dir — only the source Rename.
	dst := filepath.Join(t.TempDir(), "archived.md")
	if err := os.Rename(plan, dst); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-w.Events:
		if ev.Kind != "removed" {
			t.Errorf("after rename-away, got Kind=%q want %q", ev.Kind, "removed")
		}
		if ev.File != plan {
			t.Errorf("ev.File = %q want %q", ev.File, plan)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for rename-away event")
	}
}

func TestListKnownPlanFilesWithNoPlans(t *testing.T) {
	root := t.TempDir()
	// Completely empty worktree — still must not panic.
	got := ListKnownPlanFiles(root)
	if len(got) != 0 {
		t.Errorf("ListKnownPlanFiles on empty worktree = %v, want []", got)
	}
}
