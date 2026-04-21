package recap

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chris/thin-observer/internal/ingest"
	"github.com/chris/thin-observer/internal/parser"
	"github.com/chris/thin-observer/internal/store"
)

func setup(t *testing.T) (*store.Store, store.Worktree) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if err := s.UpsertProject(ctx, store.Project{ID: "p1", Name: "demo", RootPath: "/tmp/demo"}); err != nil {
		t.Fatal(err)
	}
	w := store.Worktree{ID: "w1", ProjectID: "p1", Name: "feature", Path: "/tmp/demo/feature"}
	if err := s.UpsertWorktree(ctx, w); err != nil {
		t.Fatal(err)
	}
	return s, w
}

func TestOverviewAndStatusRender(t *testing.T) {
	s, w := setup(t)
	in := ingest.New(s)

	planFile := filepath.Join("..", "..", "testdata", "plans", "planning-with-files.md")
	doc, err := parser.ParseFile(planFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Apply(context.Background(), w, doc); err != nil {
		t.Fatal(err)
	}

	rows, err := Overview(context.Background(), s, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("overview rows = %d, want 1", len(rows))
	}
	if rows[0].DoneCount+rows[0].PendingCount+rows[0].ActiveCount+rows[0].LostCount == 0 {
		t.Fatalf("no tasks counted: %+v", rows[0])
	}
	out := RenderStatus(rows, time.Now().UTC())
	if !strings.Contains(out, "feature") {
		t.Errorf("render missing worktree name: %q", out)
	}
	if !strings.Contains(out, "PROJECT") {
		t.Errorf("render missing header: %q", out)
	}
}

func TestWorktreeRecapIsAgentReadable(t *testing.T) {
	s, w := setup(t)
	in := ingest.New(s)
	doc, err := parser.ParseFile(filepath.Join("..", "..", "testdata", "plans", "plain-todo.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Apply(context.Background(), w, doc); err != nil {
		t.Fatal(err)
	}
	out, err := Worktree(context.Background(), s, w)
	if err != nil {
		t.Fatal(err)
	}
	// Structural requirements for agent-consumability:
	if !strings.HasPrefix(out, "# Recap: ") {
		t.Errorf("recap must start with '# Recap: ', got %q", out[:30])
	}
	if !strings.Contains(out, "# stats: ") {
		t.Errorf("recap must contain stats footer, got %q", out)
	}
	// No ANSI escape codes.
	if strings.Contains(out, "\x1b[") {
		t.Errorf("recap contains ANSI escapes, should be plain")
	}
}

func TestHumanAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{2 * time.Hour, "2h"},
		{3 * 24 * time.Hour, "3d"},
	}
	for _, c := range cases {
		if got := humanAge(c.d); got != c.want {
			t.Errorf("humanAge(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
