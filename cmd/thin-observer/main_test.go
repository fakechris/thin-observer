package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/chris/thin-observer/internal/store"
)

func TestArchiveVanishedWorktrees(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	// Two worktrees: one with an existing directory, one pointing at a path
	// that was never created (stand-in for `git worktree remove`).
	present := filepath.Join(dir, "alive-wt")
	if err := os.MkdirAll(present, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "gone-wt")

	_ = s.UpsertProject(ctx, store.Project{ID: "p1", Name: "demo", RootPath: dir})
	_ = s.UpsertWorktree(ctx, store.Worktree{ID: "w-alive", ProjectID: "p1", Name: "alive", Path: present})
	_ = s.UpsertWorktree(ctx, store.Worktree{ID: "w-gone", ProjectID: "p1", Name: "gone", Path: missing})

	// Seed a task on the vanished worktree so we can prove it survives.
	task := store.Task{
		ID: "t1", WorktreeID: "w-gone", ProjectID: "p1",
		CurrentTitle: "Outstanding work", Status: "in_progress", Confidence: 1.0,
	}
	if err := s.UpsertTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := archiveVanishedWorktrees(ctx, s, logger); err != nil {
		t.Fatalf("archiveVanishedWorktrees: %v", err)
	}

	active, _ := s.ListWorktrees(ctx, false)
	if len(active) != 1 || active[0].ID != "w-alive" {
		t.Errorf("active worktrees = %+v, want only w-alive", active)
	}
	all, _ := s.ListWorktrees(ctx, true)
	if len(all) != 2 {
		t.Errorf("all worktrees = %d, want 2 (archive must preserve the row)", len(all))
	}
	// Invariant #6: the task on the vanished worktree must still be
	// queryable — the archive path is its only route to history.
	if got, err := s.TaskByID(ctx, "t1"); err != nil {
		t.Errorf("task on archived worktree lost: %v", err)
	} else if got.CurrentTitle != "Outstanding work" {
		t.Errorf("archived task mutated: %+v", got)
	}
}
