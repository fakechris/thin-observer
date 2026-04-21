package gitutil

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestHeadSHA_InInitializedRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed; skipping")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	run("init", "--quiet")
	run("-c", "user.email=t@example.com", "-c", "user.name=Tester", "commit", "--allow-empty", "-m", "init")

	sha, err := HeadSHA(context.Background(), dir)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if len(sha) != 40 {
		t.Errorf("sha len = %d, want 40 (%q)", len(sha), sha)
	}
}

func TestHeadSHA_NonGitDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed; skipping")
	}
	dir := t.TempDir()
	// A plain temp dir with no .git anywhere above it should fail.
	sha, err := HeadSHA(context.Background(), filepath.Join(dir, "nonexistent-subdir"))
	if err == nil {
		t.Fatalf("expected error for non-git dir; got sha=%q", sha)
	}
}
