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

func TestSaveLoadConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := &Config{
		Projects: []ProjectConfig{
			{Name: "alpha", Path: "/tmp/alpha"},
		},
	}
	if err := SaveConfig(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 1 || got.Projects[0].Name != "alpha" {
		t.Fatalf("round-trip failed: %+v", got.Projects)
	}
}

func TestAddProjectDedup(t *testing.T) {
	cfg := &Config{}
	if !AddProject(cfg, "foo", "/tmp/foo") {
		t.Fatal("first add should return true")
	}
	if AddProject(cfg, "foo2", "/tmp/foo") {
		t.Fatal("duplicate path should return false")
	}
	if len(cfg.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(cfg.Projects))
	}
}

func TestRemoveProject(t *testing.T) {
	cfg := &Config{
		Projects: []ProjectConfig{
			{Name: "alpha", Path: "/tmp/alpha"},
			{Name: "beta", Path: "/tmp/beta"},
		},
	}
	if !RemoveProject(cfg, "alpha") {
		t.Fatal("remove by name should return true")
	}
	if len(cfg.Projects) != 1 || cfg.Projects[0].Name != "beta" {
		t.Fatalf("after remove: %+v", cfg.Projects)
	}
	if RemoveProject(cfg, "nonexistent") {
		t.Fatal("removing nonexistent should return false")
	}
}

func TestRemoveProjectByBasename(t *testing.T) {
	cfg := &Config{
		Projects: []ProjectConfig{
			{Name: "my-app", Path: "/home/user/workspace/my-app"},
		},
	}
	if !RemoveProject(cfg, "my-app") {
		t.Fatal("remove by name should work")
	}
	if len(cfg.Projects) != 0 {
		t.Fatalf("expected 0 projects, got %d", len(cfg.Projects))
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
