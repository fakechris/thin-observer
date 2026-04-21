package watcher

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
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
		"task_plan.md":  filepath.Join(root, "task_plan.md"),
		"phase27.md":    filepath.Join(root, "docs/plans/phase27.md"),
		"local.md":      filepath.Join(root, "plans/local.md"),
		"graph.md":      filepath.Join(root, ".workgraph/plans/graph.md"),
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

func TestListKnownPlanFilesWithNoPlans(t *testing.T) {
	root := t.TempDir()
	// Completely empty worktree — still must not panic.
	got := ListKnownPlanFiles(root)
	if len(got) != 0 {
		t.Errorf("ListKnownPlanFiles on empty worktree = %v, want []", got)
	}
}
