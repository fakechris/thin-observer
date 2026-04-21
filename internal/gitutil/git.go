// Package gitutil wraps the small set of git commands thin-observer needs.
// Keeping it in its own package means internal/ingest can reach for a commit
// SHA without pulling git concerns into the top-level CLI, and the helpers
// stay easy to stub in tests.
package gitutil

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// HeadSHA returns the full 40-character HEAD SHA for the git worktree at the
// given path. Returns an error if the directory is not a git worktree or the
// git command fails. Callers typically log and continue with an empty SHA.
//
// The command is run with a hard 2-second timeout and no network is ever
// touched (rev-parse is local-only).
func HeadSHA(ctx context.Context, worktreePath string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, "git", "-C", worktreePath, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	if len(sha) != 40 {
		return "", fmt.Errorf("unexpected git rev-parse output %q", sha)
	}
	return sha, nil
}
