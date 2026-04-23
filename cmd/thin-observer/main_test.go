package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/chris/thin-observer/internal/discovery"
	"github.com/chris/thin-observer/internal/ingest"
	"github.com/chris/thin-observer/internal/lineage"
	"github.com/chris/thin-observer/internal/store"
	"github.com/chris/thin-observer/internal/watcher"
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

	if err := s.UpsertProject(ctx, store.Project{ID: "p1", Name: "demo", RootPath: dir}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertWorktree(ctx, store.Worktree{ID: "w-alive", ProjectID: "p1", Name: "alive", Path: present}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertWorktree(ctx, store.Worktree{ID: "w-gone", ProjectID: "p1", Name: "gone", Path: missing}); err != nil {
		t.Fatal(err)
	}

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

	active, err := s.ListWorktrees(ctx, false)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 1 || active[0].ID != "w-alive" {
		t.Errorf("active worktrees = %+v, want only w-alive", active)
	}
	all, err := s.ListWorktrees(ctx, true)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
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

// TestRegisterAllRevivesArchivedWorktreeWhenPathReturns simulates the user
// running `git worktree remove` (archives the row) and then `git worktree add`
// at the same path. Without the explicit un-archive in registerAll, the row
// would stay archived because UpsertWorktree's ON CONFLICT clause preserves
// 'archived' state. This test fails if that regression ever reappears.
func TestRegisterAllRevivesArchivedWorktreeWhenPathReturns(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	// Seed: project + already-archived worktree whose path now exists on disk.
	wtPath := filepath.Join(dir, "revived-wt")
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertProject(ctx, store.Project{ID: "p1", Name: "demo", RootPath: dir}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertWorktree(ctx, store.Worktree{
		ID: "w1", ProjectID: "p1", Name: "revived", Path: wtPath,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveWorktree(ctx, "w1"); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wch, err := watcher.New(logger)
	if err != nil {
		t.Fatal(err)
	}
	defer wch.Close()
	ing := ingest.New(s)
	ing.Inferrer = lineage.New()

	found := []discovery.Found{{
		ProjectRoot:  dir,
		ProjectName:  "demo",
		WorktreePath: wtPath,
		WorktreeName: "revived",
	}}
	out, err := registerAll(ctx, s, wch, found, logger, ing)
	if err != nil {
		t.Fatalf("registerAll: %v", err)
	}
	stored, ok := out[wtPath]
	if !ok {
		t.Fatalf("registerAll did not return entry for %s", wtPath)
	}
	if stored.Status != "active" {
		t.Errorf("registerAll status=%q want active — archived row was not revived", stored.Status)
	}
	// Double-check against the DB, not just the returned struct.
	reloaded, _ := s.WorktreeByID(ctx, "w1")
	if reloaded.Status != "active" {
		t.Errorf("DB status=%q want active", reloaded.Status)
	}
	if reloaded.ArchivedAt != nil {
		t.Errorf("DB archived_at=%v want nil", reloaded.ArchivedAt)
	}
}
