package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	if err := s.UpsertProject(ctx, Project{ID: "p1", Name: "demo", RootPath: "/tmp/demo"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertWorktree(ctx, Worktree{ID: "w1", ProjectID: "p1", Name: "feature-auth", Path: "/tmp/demo/feature-auth"}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	task := Task{
		ID:           "t1",
		WorktreeID:   "w1",
		ProjectID:    "p1",
		CurrentTitle: "Add endpoints",
		Aliases:      []string{"Add API endpoints"},
		Phase:        "Phase 2",
		Status:       "in_progress",
		Confidence:   1.0,
		SourceFile:   "task_plan.md",
		SourceLine:   10,
		FirstSeenAt:  now,
		LastSeenAt:   now,
	}
	if err := s.UpsertTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	got, err := s.TaskByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentTitle != "Add endpoints" {
		t.Fatalf("title = %q", got.CurrentTitle)
	}
	if len(got.Aliases) != 1 || got.Aliases[0] != "Add API endpoints" {
		t.Fatalf("aliases = %v", got.Aliases)
	}

	if err := s.InsertEvent(ctx, Event{ID: "e1", Type: "task_created", TaskID: "t1", Data: map[string]any{"foo": "bar"}}); err != nil {
		t.Fatal(err)
	}
	events, err := s.EventsForTask(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}

	worktrees, err := s.ListWorktrees(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 1 {
		t.Fatalf("worktrees = %d", len(worktrees))
	}
}

func TestArchiveWorktree(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	_ = s.UpsertProject(ctx, Project{ID: "p1", Name: "demo", RootPath: "/tmp/demo"})
	_ = s.UpsertWorktree(ctx, Worktree{ID: "w1", ProjectID: "p1", Name: "old-feature", Path: "/tmp/old"})
	if err := s.ArchiveWorktree(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	active, _ := s.ListWorktrees(ctx, false)
	all, _ := s.ListWorktrees(ctx, true)
	if len(active) != 0 {
		t.Fatalf("active = %d, want 0", len(active))
	}
	if len(all) != 1 {
		t.Fatalf("all = %d, want 1", len(all))
	}
}
