package store

import (
	"context"
	"database/sql"
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

func TestPlanDocRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	_ = s.UpsertProject(ctx, Project{ID: "p1", Name: "demo", RootPath: "/tmp/demo"})
	_ = s.UpsertWorktree(ctx, Worktree{ID: "w1", ProjectID: "p1", Name: "main", Path: "/tmp/demo/main"})

	// First insert.
	pd := PlanDoc{
		ID:         "pd1",
		WorktreeID: "w1",
		SourceFile: "/tmp/demo/main/task_plan.md",
		Title:      "Roadmap",
		Kind:       "task_plan",
		LastSeenAt: time.Now().UTC(),
	}
	if err := s.UpsertPlanDoc(ctx, pd); err != nil {
		t.Fatal(err)
	}

	got, err := s.PlanDocByWorktreeAndFile(ctx, "w1", "/tmp/demo/main/task_plan.md")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "pd1" || got.Title != "Roadmap" || got.Kind != "task_plan" {
		t.Fatalf("got %#v", got)
	}

	// Upsert with a different ID must preserve the existing row ID (not
	// create a duplicate) so plan_link foreign keys stay stable.
	pd2 := pd
	pd2.ID = "pd1-shadow" // caller-supplied ID, but conflict should keep original
	pd2.Title = "Roadmap v2"
	pd2.Kind = "task_plan"
	if err := s.UpsertPlanDoc(ctx, pd2); err != nil {
		t.Fatal(err)
	}
	got2, err := s.PlanDocByWorktreeAndFile(ctx, "w1", "/tmp/demo/main/task_plan.md")
	if err != nil {
		t.Fatal(err)
	}
	if got2.ID != "pd1" {
		t.Fatalf("ID mutated by upsert: got %q, want pd1", got2.ID)
	}
	if got2.Title != "Roadmap v2" {
		t.Fatalf("title not updated: got %q", got2.Title)
	}

	// Listing.
	list, err := s.PlanDocsByWorktree(ctx, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
}

func TestMarkPlanDocsMissingAndResurrection(t *testing.T) {
	// Two plan_docs; one is present on disk, one vanished. After a sweep with
	// only the present file, the vanished row must get missing_since set and
	// the present row must stay clear. On the next sweep that lists both
	// files again, the previously-missing row must be cleared (resurrection).
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	_ = s.UpsertProject(ctx, Project{ID: "p1", Name: "demo", RootPath: "/tmp/demo"})
	_ = s.UpsertWorktree(ctx, Worktree{ID: "w1", ProjectID: "p1", Name: "main", Path: "/tmp/demo/main"})
	now := time.Now().UTC()
	_ = s.UpsertPlanDoc(ctx, PlanDoc{
		ID: "pd1", WorktreeID: "w1", SourceFile: "/tmp/demo/main/task_plan.md",
		Title: "Roadmap", Kind: "task_plan", LastSeenAt: now,
	})
	_ = s.UpsertPlanDoc(ctx, PlanDoc{
		ID: "pd2", WorktreeID: "w1", SourceFile: "/tmp/demo/main/docs/plans/phase27.md",
		Title: "Phase 27", Kind: "detailed_plan", LastSeenAt: now,
	})

	// Sweep 1: only pd1's file is present on disk. pd2 should go missing.
	at1 := now.Add(time.Minute)
	if err := s.MarkPlanDocsMissing(ctx, "w1", []string{"/tmp/demo/main/task_plan.md"}, at1); err != nil {
		t.Fatal(err)
	}
	pd1, _ := s.PlanDocByWorktreeAndFile(ctx, "w1", "/tmp/demo/main/task_plan.md")
	pd2, _ := s.PlanDocByWorktreeAndFile(ctx, "w1", "/tmp/demo/main/docs/plans/phase27.md")
	if pd1.MissingSince != nil {
		t.Errorf("pd1 (present file) wrongly flagged missing: %v", *pd1.MissingSince)
	}
	if pd2.MissingSince == nil {
		t.Fatal("pd2 (absent file) not flagged missing")
	}
	if !pd2.MissingSince.Equal(at1) {
		t.Errorf("pd2 missing_since = %v, want %v", *pd2.MissingSince, at1)
	}

	// Sweep 2: pd2's file returns. Flag should be cleared.
	at2 := at1.Add(time.Minute)
	if err := s.MarkPlanDocsMissing(ctx, "w1", []string{
		"/tmp/demo/main/task_plan.md",
		"/tmp/demo/main/docs/plans/phase27.md",
	}, at2); err != nil {
		t.Fatal(err)
	}
	pd2, _ = s.PlanDocByWorktreeAndFile(ctx, "w1", "/tmp/demo/main/docs/plans/phase27.md")
	if pd2.MissingSince != nil {
		t.Errorf("pd2 still flagged missing after resurrection: %v", *pd2.MissingSince)
	}
}

func TestPlanLinksReplaceAndResolve(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	_ = s.UpsertProject(ctx, Project{ID: "p1", Name: "demo", RootPath: "/tmp/demo"})
	_ = s.UpsertWorktree(ctx, Worktree{ID: "w1", ProjectID: "p1", Name: "main", Path: "/tmp/demo/main"})

	now := time.Now().UTC()
	_ = s.UpsertPlanDoc(ctx, PlanDoc{ID: "pd1", WorktreeID: "w1", SourceFile: "/tmp/demo/main/task_plan.md", Title: "Roadmap", Kind: "task_plan", LastSeenAt: now})
	_ = s.UpsertPlanDoc(ctx, PlanDoc{ID: "pd2", WorktreeID: "w1", SourceFile: "/tmp/demo/main/docs/plans/phase27.md", Title: "Phase 27", Kind: "detailed_plan", LastSeenAt: now})

	// Insert two links: one points at an existing plan_doc, one doesn't.
	links := []PlanLink{
		{ID: "l1", FromPlanID: "pd1", ToSourceFile: "/tmp/demo/main/docs/plans/phase27.md", SourceLine: 10, Label: "Phase 27"},
		{ID: "l2", FromPlanID: "pd1", ToSourceFile: "/tmp/demo/main/docs/plans/missing.md", SourceLine: 11, Label: "Missing"},
	}
	if err := s.ReplacePlanLinks(ctx, "pd1", links); err != nil {
		t.Fatal(err)
	}

	// Before resolution: both links present, to_plan_id empty.
	got, _ := s.LinksFrom(ctx, "pd1")
	if len(got) != 2 {
		t.Fatalf("links = %d, want 2", len(got))
	}
	for _, l := range got {
		if l.ToPlanID != "" {
			t.Fatalf("link %s already resolved before call", l.ID)
		}
	}

	if err := s.ResolvePlanLinkTargets(ctx, "w1"); err != nil {
		t.Fatal(err)
	}

	got, _ = s.LinksFrom(ctx, "pd1")
	var resolved, unresolved int
	for _, l := range got {
		if l.ToPlanID != "" {
			resolved++
			if l.ToPlanID != "pd2" {
				t.Errorf("link %s to_plan_id = %q, want pd2", l.ID, l.ToPlanID)
			}
		} else {
			unresolved++
		}
	}
	if resolved != 1 || unresolved != 1 {
		t.Fatalf("resolved=%d unresolved=%d, want 1 and 1", resolved, unresolved)
	}

	// Replace links: the old set must be wiped out, not merged.
	if err := s.ReplacePlanLinks(ctx, "pd1", nil); err != nil {
		t.Fatal(err)
	}
	got, _ = s.LinksFrom(ctx, "pd1")
	if len(got) != 0 {
		t.Fatalf("after replace with empty: links = %d, want 0", len(got))
	}
}

func TestTaskCountByPlanDoc(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	_ = s.UpsertProject(ctx, Project{ID: "p1", Name: "demo", RootPath: "/tmp/demo"})
	_ = s.UpsertWorktree(ctx, Worktree{ID: "w1", ProjectID: "p1", Name: "main", Path: "/tmp/demo/main"})
	now := time.Now().UTC()
	_ = s.UpsertPlanDoc(ctx, PlanDoc{ID: "pd1", WorktreeID: "w1", SourceFile: "/tmp/demo/main/task_plan.md", Title: "Roadmap", Kind: "task_plan", LastSeenAt: now})
	_ = s.UpsertPlanDoc(ctx, PlanDoc{ID: "pd2", WorktreeID: "w1", SourceFile: "/tmp/demo/main/docs/plans/phase27.md", Title: "Phase 27", Kind: "detailed_plan", LastSeenAt: now})

	for _, tt := range []Task{
		{ID: "t1", WorktreeID: "w1", ProjectID: "p1", CurrentTitle: "A", Status: "pending", SourceFile: "/tmp/demo/main/task_plan.md", FirstSeenAt: now, LastSeenAt: now},
		{ID: "t2", WorktreeID: "w1", ProjectID: "p1", CurrentTitle: "B", Status: "done", SourceFile: "/tmp/demo/main/task_plan.md", FirstSeenAt: now, LastSeenAt: now},
		{ID: "t3", WorktreeID: "w1", ProjectID: "p1", CurrentTitle: "C", Status: "pending", SourceFile: "/tmp/demo/main/docs/plans/phase27.md", FirstSeenAt: now, LastSeenAt: now},
	} {
		if err := s.UpsertTask(ctx, tt); err != nil {
			t.Fatal(err)
		}
	}

	n1, err := s.TaskCountByPlanDoc(ctx, "pd1")
	if err != nil {
		t.Fatal(err)
	}
	if n1 != 2 {
		t.Errorf("pd1 count = %d, want 2", n1)
	}
	n2, _ := s.TaskCountByPlanDoc(ctx, "pd2")
	if n2 != 1 {
		t.Errorf("pd2 count = %d, want 1", n2)
	}
}

func TestTaskRevisionAppendAndQuery(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	_ = s.UpsertProject(ctx, Project{ID: "p1", Name: "demo", RootPath: "/tmp/demo"})
	_ = s.UpsertWorktree(ctx, Worktree{ID: "w1", ProjectID: "p1", Name: "feature", Path: "/tmp/demo/feature"})
	now := time.Now().UTC()
	snap1 := Snapshot{ID: "s1", WorktreeID: "w1", SourceFile: "/tmp/demo/feature/task_plan.md", Timestamp: now, RawHash: "h1", PhasesJSON: "[]"}
	if err := s.InsertSnapshot(ctx, snap1); err != nil {
		t.Fatal(err)
	}
	task := Task{ID: "t1", WorktreeID: "w1", ProjectID: "p1", CurrentTitle: "alpha", Status: "pending", SourceFile: "/tmp/demo/feature/task_plan.md", FirstSeenAt: now, LastSeenAt: now, Confidence: 1.0}
	if err := s.UpsertTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	rev1 := TaskRevision{
		ID: "r1", SnapshotID: "s1", TaskID: "t1", WorktreeID: "w1", ProjectID: "p1",
		SourceFile: "/tmp/demo/feature/task_plan.md", Title: "alpha", Phase: "Phase 1", Status: "pending",
		Confidence: 1.0, SourceLine: 3, RecordedAt: now,
	}
	if err := s.InsertTaskRevision(ctx, rev1); err != nil {
		t.Fatal(err)
	}

	later := now.Add(5 * time.Minute)
	snap2 := Snapshot{ID: "s2", WorktreeID: "w1", SourceFile: "/tmp/demo/feature/task_plan.md", Timestamp: later, RawHash: "h2", PhasesJSON: "[]"}
	if err := s.InsertSnapshot(ctx, snap2); err != nil {
		t.Fatal(err)
	}
	rev2 := rev1
	rev2.ID = "r2"
	rev2.SnapshotID = "s2"
	rev2.Status = "done"
	rev2.RecordedAt = later
	if err := s.InsertTaskRevision(ctx, rev2); err != nil {
		t.Fatal(err)
	}

	// Mutate the live task — revisions must not follow.
	task.CurrentTitle = "alpha-renamed"
	task.Status = "dropped"
	_ = s.UpsertTask(ctx, task)

	hist, err := s.TaskHistoryByTask(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("history = %d, want 2", len(hist))
	}
	if hist[0].Title != "alpha" || hist[0].Status != "pending" {
		t.Errorf("hist[0] = %+v, want frozen pre-edit state", hist[0])
	}
	if hist[1].Status != "done" {
		t.Errorf("hist[1].Status = %q, want done", hist[1].Status)
	}

	bySnap, err := s.TaskRevisionsBySnapshot(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(bySnap) != 1 || bySnap[0].Title != "alpha" {
		t.Errorf("by snapshot s1 = %+v", bySnap)
	}
}

// TestOpenMigratesPreSnapshotEventTable simulates opening a real pre-upgrade
// database. The old event table lacks snapshot_id; Open must migrate it before
// applying schema objects that reference event(snapshot_id).
func TestOpenMigratesPreSnapshotEventTable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE event (
			id          TEXT PRIMARY KEY,
			timestamp   TEXT NOT NULL,
			type        TEXT NOT NULL,
			task_id     TEXT,
			worktree_id TEXT,
			data_json   TEXT NOT NULL DEFAULT '{}'
		)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open old DB: %v", err)
	}
	defer s.Close()

	has, err := columnExists(context.Background(), s.DB, "event", "snapshot_id")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("Open did not add event.snapshot_id")
	}
}

// TestMigrateAddsEventSnapshotID simulates a pre-PR database (event table
// without snapshot_id) and verifies migrate() adds the column so that
// InsertEvent can then write to it. Without this migration, upgrading an
// existing DB would fail at runtime on the next event insert.
func TestMigrateAddsEventSnapshotID(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Recreate the event table without snapshot_id to mimic a pre-migration DB.
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE event`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `
		CREATE TABLE event (
			id          TEXT PRIMARY KEY,
			timestamp   TEXT NOT NULL,
			type        TEXT NOT NULL,
			task_id     TEXT,
			worktree_id TEXT,
			data_json   TEXT NOT NULL DEFAULT '{}'
		)`); err != nil {
		t.Fatal(err)
	}

	has, err := columnExists(ctx, s.DB, "event", "snapshot_id")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("precondition: event.snapshot_id should be absent")
	}

	if err := migrate(ctx, s.DB); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	has, err = columnExists(ctx, s.DB, "event", "snapshot_id")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("migrate did not add event.snapshot_id")
	}

	// Second migrate() must be a no-op (idempotent).
	if err := migrate(ctx, s.DB); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	// And InsertEvent with a snapshot_id must now succeed end-to-end.
	_ = s.UpsertProject(ctx, Project{ID: "p1", Name: "demo", RootPath: "/tmp/demo-mig"})
	_ = s.UpsertWorktree(ctx, Worktree{ID: "w1", ProjectID: "p1", Name: "f", Path: "/tmp/demo-mig/f"})
	now := time.Now().UTC()
	if err := s.InsertSnapshot(ctx, Snapshot{ID: "s1", WorktreeID: "w1", SourceFile: "/tmp/demo-mig/f/task_plan.md", Timestamp: now, RawHash: "h", PhasesJSON: "[]"}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertEvent(ctx, Event{ID: "e1", Timestamp: now, Type: "plan_synced", WorktreeID: "w1", SnapshotID: "s1"}); err != nil {
		t.Fatalf("InsertEvent post-migrate: %v", err)
	}
	got, err := s.EventsBySnapshot(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SnapshotID != "s1" {
		t.Errorf("EventsBySnapshot after migrate = %+v", got)
	}
	s.Close()
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

// TestUnarchiveWorktreeRestoresActive covers the path where a user removes
// then recreates a worktree at the same location. UpsertWorktree deliberately
// refuses to un-archive (see its ON CONFLICT clause), so this explicit helper
// is the only way registerAll can restore the row without losing the ID /
// task history attached to it.
func TestUnarchiveWorktreeRestoresActive(t *testing.T) {
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
	if err := s.UpsertWorktree(ctx, Worktree{ID: "w1", ProjectID: "p1", Name: "revived", Path: "/tmp/revived"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveWorktree(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if err := s.UnarchiveWorktree(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	got, err := s.WorktreeByID(ctx, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "active" {
		t.Errorf("status=%q want active", got.Status)
	}
	if got.ArchivedAt != nil {
		t.Errorf("archived_at=%v want nil", got.ArchivedAt)
	}
	// A subsequent UpsertWorktree with Status=active must not re-archive it.
	if err := s.UpsertWorktree(ctx, Worktree{ID: "w1", ProjectID: "p1", Name: "revived", Path: "/tmp/revived", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.WorktreeByID(ctx, "w1")
	if got2.Status != "active" {
		t.Errorf("after upsert status=%q want active", got2.Status)
	}
}
