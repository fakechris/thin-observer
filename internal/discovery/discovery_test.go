package discovery

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWalkFindsGitRepo(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var found []string
	walkRoots(dir, nil, func(r string) { found = append(found, r) })
	if len(found) != 1 || found[0] != repo {
		t.Fatalf("found = %v", found)
	}
}

func TestExcludeSkipsMatch(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "node_modules", "foo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var found []string
	walkRoots(dir, []string{"node_modules"}, func(r string) { found = append(found, r) })
	if len(found) != 0 {
		t.Fatalf("expected nothing, got %v", found)
	}
}

func TestListWorktrees_RealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	must := func(cmd *exec.Cmd) {
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	must(exec.Command("git", "-C", dir, "init"))
	must(exec.Command("git", "-C", dir, "config", "user.email", "t@example.com"))
	must(exec.Command("git", "-C", dir, "config", "user.name", "t"))
	must(exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", "init"))
	// listWorktrees should return [] (only the main worktree).
	wts := listWorktrees(dir)
	if len(wts) != 0 {
		t.Fatalf("unexpected worktrees: %v", wts)
	}
}
