// Package ingest turns a parsed plan document into durable state:
// a snapshot row plus task upserts with lineage inference.
//
// This file contains the Stage-2 ingestion path using *exact* matching only
// (same title in same phase → same task). Stage 3 adds a pluggable lineage
// inferrer for rename / split / merge / lost.
package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/chris/thin-observer/internal/gitutil"
	"github.com/chris/thin-observer/internal/lineage"
	"github.com/chris/thin-observer/internal/parser"
	"github.com/chris/thin-observer/internal/store"
	"github.com/oklog/ulid/v2"
)

// Ingester applies a parsed document to the store.
type Ingester struct {
	Store *store.Store
	// Inferrer is optional. When nil, exact-match ingestion is used.
	Inferrer lineage.Inferrer
	// Logger receives non-fatal ingest warnings (e.g., git rev-parse failure).
	// Nil is fine — falls back to slog.Default.
	Logger *slog.Logger
	// headSHA overrides the commit-SHA lookup in tests. Defaults to
	// gitutil.HeadSHA when nil.
	headSHA func(ctx context.Context, worktreePath string) (string, error)
}

func New(s *store.Store) *Ingester {
	return &Ingester{Store: s}
}

func (in *Ingester) logger() *slog.Logger {
	if in.Logger != nil {
		return in.Logger
	}
	return slog.Default()
}

func (in *Ingester) resolveHeadSHA(ctx context.Context, worktreePath string) string {
	fn := in.headSHA
	if fn == nil {
		fn = gitutil.HeadSHA
	}
	sha, err := fn(ctx, worktreePath)
	if err != nil {
		in.logger().Warn("ingest.head_sha_failed", "worktree", worktreePath, "err", err)
		return ""
	}
	return sha
}

// Apply persists a parsed doc against the given worktree and returns the
// inserted snapshot ID and a summary of task changes.
//
// Everything (snapshot insert + task upserts + events) runs inside a single
// transaction so a crash mid-apply cannot leave a snapshot row with a raw_hash
// that blocks future re-ingestion of a partially-applied plan.
func (in *Ingester) Apply(ctx context.Context, worktree store.Worktree, doc *parser.PlanDoc) (*ApplyResult, error) {
	now := time.Now().UTC()
	snapID := newULID(now)

	phasesJSON, err := json.Marshal(doc.Phases)
	if err != nil {
		return nil, fmt.Errorf("marshal phases: %w", err)
	}

	var result ApplyResult
	result.SnapshotID = snapID

	txErr := in.Store.WithTx(ctx, func(tx *store.Store) error {
		// Skip if raw hash is unchanged from the last snapshot of this file.
		latest, _ := tx.LatestSnapshot(ctx, worktree.ID, doc.SourceFile)
		if latest != nil && latest.RawHash == doc.RawHash && latest.PhasesJSON == string(phasesJSON) {
			result.SnapshotID = latest.ID
			result.Unchanged = true
			return nil
		}

		snap := store.Snapshot{
			ID:         snapID,
			WorktreeID: worktree.ID,
			SourceFile: doc.SourceFile,
			Timestamp:  now,
			RawHash:    doc.RawHash,
			PhasesJSON: string(phasesJSON),
			CommitSHA:  in.resolveHeadSHA(ctx, worktree.Path),
		}
		if err := tx.InsertSnapshot(ctx, snap); err != nil {
			return fmt.Errorf("insert snapshot: %w", err)
		}

		return in.applyInTx(ctx, tx, worktree, doc, snapID, now, &result)
	})
	if txErr != nil {
		return nil, txErr
	}
	return &result, nil
}

// applyInTx runs the body of Apply against a transaction-scoped Store.
func (in *Ingester) applyInTx(
	ctx context.Context,
	tx *store.Store,
	worktree store.Worktree,
	doc *parser.PlanDoc,
	snapID string,
	now time.Time,
	result *ApplyResult,
) error {
	// Load existing tasks for this worktree + source file.
	all, err := tx.TasksByWorktree(ctx, worktree.ID)
	if err != nil {
		return fmt.Errorf("load tasks: %w", err)
	}
	var existing []store.Task
	for _, t := range all {
		if t.SourceFile == doc.SourceFile {
			existing = append(existing, t)
		}
	}

	var newItems []incoming
	for _, ph := range doc.Phases {
		for _, t := range ph.Tasks {
			newItems = append(newItems, incoming{Phase: ph.Name, Task: t})
		}
	}

	// Decide match strategy: Inferrer if provided, else exact-only.
	var decisions []lineage.Decision
	if in.Inferrer != nil {
		oldTasks := make([]lineage.ExistingTask, 0, len(existing))
		for _, e := range existing {
			oldTasks = append(oldTasks, lineage.ExistingTask{
				ID:      e.ID,
				Title:   e.CurrentTitle,
				Aliases: e.Aliases,
				Phase:   e.Phase,
				Status:  e.Status,
			})
		}
		newInput := make([]lineage.NewItem, 0, len(newItems))
		for _, it := range newItems {
			newInput = append(newInput, lineage.NewItem{
				Phase:  it.Phase,
				Title:  it.Task.Title,
				Status: it.Task.Status,
				Line:   it.Task.Line,
				Ref:    it.Task.Ref,
			})
		}
		decisions = in.Inferrer.Infer(oldTasks, newInput)
	} else {
		decisions = exactMatch(existing, newItems)
	}

	// Track which old tasks were "seen" this round so we can mark missing ones.
	seenOld := map[string]bool{}

	for _, d := range decisions {
		switch d.Kind {
		case lineage.DecisionSameTask:
			old := findByID(existing, d.OldID)
			if old == nil {
				continue
			}
			seenOld[old.ID] = true
			old.MissingInRev = 0
			old.CurrentTitle = newItems[d.NewIndex].Task.Title
			old.Phase = newItems[d.NewIndex].Phase
			// "dropped" is a terminal observer-side decision (usually from a
			// manual override). Never let a later markdown rev un-drop it.
			if old.Status != "dropped" {
				old.Status = newItems[d.NewIndex].Task.Status
			}
			old.SourceLine = newItems[d.NewIndex].Task.Line
			old.LastSeenAt = now
			old.LastSnapshotID = snapID
			if d.Confidence > 0 {
				old.Confidence = d.Confidence
			}
			if err := tx.UpsertTask(ctx, *old); err != nil {
				return err
			}
			if err := tx.InsertEvent(ctx, store.Event{
				ID:         newULID(now),
				Timestamp:  now,
				Type:       "task_updated",
				TaskID:     old.ID,
				WorktreeID: worktree.ID,
			}); err != nil {
				return err
			}
			result.Updated++
		case lineage.DecisionRename:
			old := findByID(existing, d.OldID)
			if old == nil {
				continue
			}
			seenOld[old.ID] = true
			newTitle := newItems[d.NewIndex].Task.Title
			if newTitle != old.CurrentTitle {
				old.Aliases = appendUnique(old.Aliases, old.CurrentTitle)
				old.CurrentTitle = newTitle
			}
			old.Phase = newItems[d.NewIndex].Phase
			if old.Status != "dropped" {
				old.Status = newItems[d.NewIndex].Task.Status
			}
			old.SourceLine = newItems[d.NewIndex].Task.Line
			old.LastSeenAt = now
			old.LastSnapshotID = snapID
			old.Confidence = d.Confidence
			old.MissingInRev = 0
			if err := tx.UpsertTask(ctx, *old); err != nil {
				return err
			}
			if err := tx.InsertEvent(ctx, store.Event{
				ID:         newULID(now),
				Timestamp:  now,
				Type:       "task_renamed",
				TaskID:     old.ID,
				WorktreeID: worktree.ID,
				Data:       map[string]any{"from": old.Aliases, "to": newTitle, "confidence": d.Confidence},
			}); err != nil {
				return err
			}
			result.Renamed++
		case lineage.DecisionNewTask:
			nt := newItems[d.NewIndex]
			t := store.Task{
				ID:             newULID(now),
				WorktreeID:     worktree.ID,
				ProjectID:      worktree.ProjectID,
				CurrentTitle:   nt.Task.Title,
				Phase:          nt.Phase,
				Status:         nt.Task.Status,
				Confidence:     1.0,
				SourceFile:     doc.SourceFile,
				SourceLine:     nt.Task.Line,
				FirstSeenAt:    now,
				LastSeenAt:     now,
				LastSnapshotID: snapID,
			}
			if err := tx.UpsertTask(ctx, t); err != nil {
				return err
			}
			if err := tx.InsertEvent(ctx, store.Event{
				ID:         newULID(now),
				Timestamp:  now,
				Type:       "task_created",
				TaskID:     t.ID,
				WorktreeID: worktree.ID,
				Data:       map[string]any{"title": t.CurrentTitle, "phase": t.Phase},
			}); err != nil {
				return err
			}
			result.Created++
		case lineage.DecisionSplit:
			parent := findByID(existing, d.OldID)
			if parent == nil {
				continue
			}
			seenOld[parent.ID] = true
			// Parent becomes superseded; new children are created carrying SplitFrom.
			var childIDs []string
			for _, idx := range d.ChildNewIndexes {
				nt := newItems[idx]
				child := store.Task{
					ID:             newULID(now),
					WorktreeID:     worktree.ID,
					ProjectID:      worktree.ProjectID,
					CurrentTitle:   nt.Task.Title,
					Phase:          nt.Phase,
					Status:         nt.Task.Status,
					Confidence:     d.Confidence,
					SourceFile:     doc.SourceFile,
					SourceLine:     nt.Task.Line,
					FirstSeenAt:    now,
					LastSeenAt:     now,
					LastSnapshotID: snapID,
					SplitFrom:      []string{parent.ID},
				}
				if err := tx.UpsertTask(ctx, child); err != nil {
					return err
				}
				childIDs = append(childIDs, child.ID)
			}
			// A completed parent stays done: if the agent closed the umbrella
			// and then logged follow-ups as separate items, the umbrella's work
			// actually finished. Only pending/in_progress parents become dropped.
			if parent.Status != "done" {
				parent.Status = "dropped"
			}
			// Point the parent at one of its children for symmetry with merge
			// (where each parent.Supersedes = child.ID). Split is 1→N so we
			// pick the first child as the canonical successor — SplitFrom on
			// each child carries the full relationship.
			if len(childIDs) > 0 {
				parent.Supersedes = childIDs[0]
			}
			parent.LastSeenAt = now
			parent.MissingInRev = 0
			if err := tx.UpsertTask(ctx, *parent); err != nil {
				return err
			}
			if err := tx.InsertEvent(ctx, store.Event{
				ID:         newULID(now),
				Timestamp:  now,
				Type:       "task_split_inferred",
				TaskID:     parent.ID,
				WorktreeID: worktree.ID,
				Data:       map[string]any{"children": childIDs, "confidence": d.Confidence},
			}); err != nil {
				return err
			}
			result.Split++
		case lineage.DecisionMerge:
			// d.OldIDs are merged into one new item.
			nt := newItems[d.NewIndex]
			child := store.Task{
				ID:             newULID(now),
				WorktreeID:     worktree.ID,
				ProjectID:      worktree.ProjectID,
				CurrentTitle:   nt.Task.Title,
				Phase:          nt.Phase,
				Status:         nt.Task.Status,
				Confidence:     d.Confidence,
				SourceFile:     doc.SourceFile,
				SourceLine:     nt.Task.Line,
				FirstSeenAt:    now,
				LastSeenAt:     now,
				LastSnapshotID: snapID,
				MergedFrom:     append([]string(nil), d.OldIDs...),
			}
			if err := tx.UpsertTask(ctx, child); err != nil {
				return err
			}
			for _, oid := range d.OldIDs {
				if p := findByID(existing, oid); p != nil {
					seenOld[p.ID] = true
					// Preserve "done" — a completed parent that got merged into
					// a follow-up still had its own work finished.
					if p.Status != "done" {
						p.Status = "dropped"
					}
					p.Supersedes = child.ID
					p.LastSeenAt = now
					p.MissingInRev = 0
					if err := tx.UpsertTask(ctx, *p); err != nil {
						return err
					}
				}
			}
			if err := tx.InsertEvent(ctx, store.Event{
				ID:         newULID(now),
				Timestamp:  now,
				Type:       "task_merge_inferred",
				TaskID:     child.ID,
				WorktreeID: worktree.ID,
				Data:       map[string]any{"parents": d.OldIDs, "confidence": d.Confidence},
			}); err != nil {
				return err
			}
			result.Merged++
		}
	}

	// Anything not seen gets missing_in_rev bumped. When it hits 2 → lost.
	for _, e := range existing {
		if seenOld[e.ID] {
			continue
		}
		if e.Status == "done" || e.Status == "dropped" || e.Status == "lost" {
			continue
		}
		e.MissingInRev++
		if e.MissingInRev >= 2 && e.Status != "lost" {
			e.Status = "lost"
			if err := tx.InsertEvent(ctx, store.Event{
				ID:         newULID(now),
				Timestamp:  now,
				Type:       "task_lost",
				TaskID:     e.ID,
				WorktreeID: worktree.ID,
			}); err != nil {
				return err
			}
			result.Lost++
		}
		if err := tx.UpsertTask(ctx, e); err != nil {
			return err
		}
	}

	// Event: plan_synced.
	if err := tx.InsertEvent(ctx, store.Event{
		ID:         newULID(now),
		Timestamp:  now,
		Type:       "plan_synced",
		WorktreeID: worktree.ID,
		Data: map[string]any{
			"source_file": doc.SourceFile,
			"snapshot_id": snapID,
			"created":     result.Created,
			"updated":     result.Updated,
			"renamed":     result.Renamed,
			"split":       result.Split,
			"merged":      result.Merged,
			"lost":        result.Lost,
		},
	}); err != nil {
		return err
	}

	// plan_doc + plan_link: upsert the document-level row for this file and
	// replace its outbound links with whatever the parser just extracted.
	// Link resolution (to_plan_id) runs last so newly-ingested plan_docs in
	// the same worktree can be linked immediately.
	if err := syncPlanDocAndLinks(ctx, tx, worktree, doc, snapID, now); err != nil {
		return err
	}
	if err := tx.ResolvePlanLinkTargets(ctx, worktree.ID); err != nil {
		return fmt.Errorf("resolve plan links: %w", err)
	}

	// task_revision: append one row per live task scoped to this snapshot's
	// source file, so the time machine can replay the board at this point.
	// We re-read tasks after upserts so renames / splits / merges are reflected.
	if err := writeTaskRevisions(ctx, tx, worktree, doc.SourceFile, snapID, now); err != nil {
		return fmt.Errorf("write task revisions: %w", err)
	}
	return nil
}

// writeTaskRevisions records one task_revision per live task in this worktree
// sourced from the just-ingested file. "Live" means the task's current
// source_file matches — a task that was dropped into a different file or
// merged away ends up in a different revision set.
//
// project_id is asserted by join to catch a theoretical worktree/task mismatch
// rather than trust the loose row data.
func writeTaskRevisions(
	ctx context.Context,
	tx *store.Store,
	worktree store.Worktree,
	sourceFile string,
	snapID string,
	now time.Time,
) error {
	tasks, err := tx.TasksByWorktree(ctx, worktree.ID)
	if err != nil {
		return fmt.Errorf("load tasks: %w", err)
	}
	for _, t := range tasks {
		if t.SourceFile != sourceFile {
			continue
		}
		if t.ProjectID != worktree.ProjectID {
			return fmt.Errorf("task %s project %q does not match worktree project %q",
				t.ID, t.ProjectID, worktree.ProjectID)
		}
		rev := store.TaskRevision{
			ID:         newULID(now),
			SnapshotID: snapID,
			TaskID:     t.ID,
			WorktreeID: worktree.ID,
			ProjectID:  t.ProjectID,
			SourceFile: t.SourceFile,
			Title:      t.CurrentTitle,
			Phase:      t.Phase,
			Status:     t.Status,
			Confidence: t.Confidence,
			SourceLine: t.SourceLine,
			RecordedAt: now,
		}
		if err := tx.InsertTaskRevision(ctx, rev); err != nil {
			return fmt.Errorf("insert task_revision: %w", err)
		}
	}
	return nil
}

// syncPlanDocAndLinks upserts a plan_doc for the just-ingested file and
// replaces its outbound links. The existing ID is preserved across upserts
// so plan_link foreign keys remain stable.
func syncPlanDocAndLinks(
	ctx context.Context,
	tx *store.Store,
	worktree store.Worktree,
	doc *parser.PlanDoc,
	snapID string,
	now time.Time,
) error {
	existing, _ := tx.PlanDocByWorktreeAndFile(ctx, worktree.ID, doc.SourceFile)
	id := ""
	if existing != nil {
		id = existing.ID
	} else {
		id = newULID(now)
	}
	pd := store.PlanDoc{
		ID:             id,
		WorktreeID:     worktree.ID,
		SourceFile:     doc.SourceFile,
		Title:          planDocTitle(doc),
		Kind:           planDocKind(doc.SourceFile),
		LastSnapshotID: snapID,
		LastSeenAt:     now,
	}
	if err := tx.UpsertPlanDoc(ctx, pd); err != nil {
		return fmt.Errorf("upsert plan_doc: %w", err)
	}

	links := make([]store.PlanLink, 0, len(doc.Links))
	for _, pl := range doc.Links {
		links = append(links, store.PlanLink{
			ID:           newULID(now),
			FromPlanID:   id,
			ToSourceFile: resolveLinkTarget(doc.SourceFile, pl.Target),
			SourceLine:   pl.Line,
			Label:        pl.Label,
		})
	}
	if err := tx.ReplacePlanLinks(ctx, id, links); err != nil {
		return fmt.Errorf("replace plan_links: %w", err)
	}
	return nil
}

// planDocTitle picks frontmatter.title, else the first phase heading, else
// the file basename. Matches the rules in the dev brief.
func planDocTitle(doc *parser.PlanDoc) string {
	if t := strings.TrimSpace(doc.Frontmatter.Title); t != "" {
		return t
	}
	for _, ph := range doc.Phases {
		if name := strings.TrimSpace(ph.Name); name != "" {
			return name
		}
	}
	return filepath.Base(doc.SourceFile)
}

// planDocKind classifies a plan file by path/basename. Order matters: the
// basename rules win over the directory rule so a literal task_plan.md inside
// docs/plans/ still reads as task_plan.
func planDocKind(sourceFile string) string {
	base := strings.ToLower(filepath.Base(sourceFile))
	switch base {
	case "task_plan.md", "plan.md", "todo.md", "tasks.md":
		return "task_plan"
	case "progress.md", "session.md", "log.md":
		return "progress"
	case "findings.md", "research.md", "notes.md":
		return "findings"
	}
	// Use forward slashes for matching regardless of OS path separator.
	p := filepath.ToSlash(strings.ToLower(sourceFile))
	if strings.Contains(p, "/docs/plans/") || strings.Contains(p, "/plans/") {
		return "detailed_plan"
	}
	return "unknown"
}

// resolveLinkTarget converts a possibly-relative link target to an absolute
// path, anchored against the containing source file. Absolute targets pass
// through unchanged (after filepath.Clean).
func resolveLinkTarget(sourceFile, target string) string {
	// Strip any #anchor before resolving — the anchor is UI noise here.
	clean := target
	if i := strings.Index(clean, "#"); i >= 0 {
		clean = clean[:i]
	}
	if filepath.IsAbs(clean) {
		return filepath.Clean(clean)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), clean))
}

// ApplyResult is a summary returned by Apply.
type ApplyResult struct {
	SnapshotID string
	Unchanged  bool
	Created    int
	Updated    int
	Renamed    int
	Split      int
	Merged     int
	Lost       int
}

// incoming is a flattened (phase, task) pair.
type incoming struct {
	Phase string
	Task  parser.TaskItem
}

// exactMatch: only equal title in equal phase counts as SameTask; everything
// else becomes NewTask. This is a baseline until lineage inferrer is enabled.
func exactMatch(existing []store.Task, newItems []incoming) []lineage.Decision {
	used := map[string]bool{}
	var out []lineage.Decision
	for i, ni := range newItems {
		var match *store.Task
		for j := range existing {
			e := &existing[j]
			if used[e.ID] {
				continue
			}
			if e.CurrentTitle == ni.Task.Title && e.Phase == ni.Phase {
				match = e
				break
			}
		}
		if match != nil {
			used[match.ID] = true
			out = append(out, lineage.Decision{Kind: lineage.DecisionSameTask, OldID: match.ID, NewIndex: i, Confidence: 1.0})
		} else {
			out = append(out, lineage.Decision{Kind: lineage.DecisionNewTask, NewIndex: i, Confidence: 1.0})
		}
	}
	return out
}

func findByID(xs []store.Task, id string) *store.Task {
	for i := range xs {
		if xs[i].ID == id {
			return &xs[i]
		}
	}
	return nil
}

func appendUnique(xs []string, s string) []string {
	for _, x := range xs {
		if x == s {
			return xs
		}
	}
	return append(xs, s)
}

func newULID(t time.Time) string {
	return ulid.MustNew(ulid.Timestamp(t), ulid.DefaultEntropy()).String()
}
