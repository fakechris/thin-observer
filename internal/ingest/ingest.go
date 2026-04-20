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
	"time"

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
}

func New(s *store.Store) *Ingester {
	return &Ingester{Store: s}
}

// Apply persists a parsed doc against the given worktree and returns the
// inserted snapshot ID and a summary of task changes.
func (in *Ingester) Apply(ctx context.Context, worktree store.Worktree, doc *parser.PlanDoc) (*ApplyResult, error) {
	now := time.Now().UTC()
	snapID := newULID(now)

	phasesJSON, _ := json.Marshal(doc.Phases)

	// Skip if raw hash is unchanged from the last snapshot of this file.
	latest, _ := in.Store.LatestSnapshot(ctx, worktree.ID, doc.SourceFile)
	if latest != nil && latest.RawHash == doc.RawHash {
		return &ApplyResult{SnapshotID: latest.ID, Unchanged: true}, nil
	}

	snap := store.Snapshot{
		ID:         snapID,
		WorktreeID: worktree.ID,
		SourceFile: doc.SourceFile,
		Timestamp:  now,
		RawHash:    doc.RawHash,
		PhasesJSON: string(phasesJSON),
	}
	if err := in.Store.InsertSnapshot(ctx, snap); err != nil {
		return nil, fmt.Errorf("insert snapshot: %w", err)
	}

	// Load existing tasks for this worktree + source file.
	all, err := in.Store.TasksByWorktree(ctx, worktree.ID)
	if err != nil {
		return nil, fmt.Errorf("load tasks: %w", err)
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

	var result ApplyResult
	result.SnapshotID = snapID

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
			old.Status = newItems[d.NewIndex].Task.Status
			old.SourceLine = newItems[d.NewIndex].Task.Line
			old.LastSeenAt = now
			old.LastSnapshotID = snapID
			if d.Confidence > 0 {
				old.Confidence = d.Confidence
			}
			if err := in.Store.UpsertTask(ctx, *old); err != nil {
				return nil, err
			}
			_ = in.Store.InsertEvent(ctx, store.Event{
				ID:         newULID(now),
				Timestamp:  now,
				Type:       "task_updated",
				TaskID:     old.ID,
				WorktreeID: worktree.ID,
			})
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
			old.Status = newItems[d.NewIndex].Task.Status
			old.SourceLine = newItems[d.NewIndex].Task.Line
			old.LastSeenAt = now
			old.LastSnapshotID = snapID
			old.Confidence = d.Confidence
			old.MissingInRev = 0
			if err := in.Store.UpsertTask(ctx, *old); err != nil {
				return nil, err
			}
			_ = in.Store.InsertEvent(ctx, store.Event{
				ID:         newULID(now),
				Timestamp:  now,
				Type:       "task_renamed",
				TaskID:     old.ID,
				WorktreeID: worktree.ID,
				Data:       map[string]any{"from": old.Aliases, "to": newTitle, "confidence": d.Confidence},
			})
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
			if err := in.Store.UpsertTask(ctx, t); err != nil {
				return nil, err
			}
			_ = in.Store.InsertEvent(ctx, store.Event{
				ID:         newULID(now),
				Timestamp:  now,
				Type:       "task_created",
				TaskID:     t.ID,
				WorktreeID: worktree.ID,
				Data:       map[string]any{"title": t.CurrentTitle, "phase": t.Phase},
			})
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
				if err := in.Store.UpsertTask(ctx, child); err != nil {
					return nil, err
				}
				childIDs = append(childIDs, child.ID)
			}
			parent.Status = "dropped" // umbrella: we mark superseded via supersedes=nil but status=dropped
			parent.LastSeenAt = now
			parent.MissingInRev = 0
			if err := in.Store.UpsertTask(ctx, *parent); err != nil {
				return nil, err
			}
			_ = in.Store.InsertEvent(ctx, store.Event{
				ID:         newULID(now),
				Timestamp:  now,
				Type:       "task_split_inferred",
				TaskID:     parent.ID,
				WorktreeID: worktree.ID,
				Data:       map[string]any{"children": childIDs, "confidence": d.Confidence},
			})
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
			if err := in.Store.UpsertTask(ctx, child); err != nil {
				return nil, err
			}
			for _, oid := range d.OldIDs {
				if p := findByID(existing, oid); p != nil {
					seenOld[p.ID] = true
					p.Status = "dropped"
					p.Supersedes = child.ID
					p.LastSeenAt = now
					p.MissingInRev = 0
					_ = in.Store.UpsertTask(ctx, *p)
				}
			}
			_ = in.Store.InsertEvent(ctx, store.Event{
				ID:         newULID(now),
				Timestamp:  now,
				Type:       "task_merge_inferred",
				TaskID:     child.ID,
				WorktreeID: worktree.ID,
				Data:       map[string]any{"parents": d.OldIDs, "confidence": d.Confidence},
			})
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
			_ = in.Store.InsertEvent(ctx, store.Event{
				ID:         newULID(now),
				Timestamp:  now,
				Type:       "task_lost",
				TaskID:     e.ID,
				WorktreeID: worktree.ID,
			})
			result.Lost++
		}
		if err := in.Store.UpsertTask(ctx, e); err != nil {
			return nil, err
		}
	}

	// Event: plan_synced.
	_ = in.Store.InsertEvent(ctx, store.Event{
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
	})
	return &result, nil
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
