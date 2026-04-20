// Package recap builds human/agent-readable summaries of task state.
//
// Two audiences:
//   - `thin-observer status` — at-a-glance table for a human
//   - `thin-observer recap`  — plain text meant to be pasted into an agent's
//     context window so it can resume work without forgetting tasks
package recap

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chris/thin-observer/internal/store"
)

// WorktreeSummary is a row in the overview table.
type WorktreeSummary struct {
	Worktree       store.Worktree
	Project        string
	ActiveCount    int
	PendingCount   int
	DoneCount      int
	LostCount      int
	LastActivityAt time.Time
	InProgressTop  []string // up to 3 titles currently in-progress
}

// Overview returns one row per active worktree, plus archived if requested.
func Overview(ctx context.Context, s *store.Store, includeArchived bool) ([]WorktreeSummary, error) {
	wts, err := s.ListWorktrees(ctx, includeArchived)
	if err != nil {
		return nil, err
	}
	projects := map[string]string{}
	projRows, _ := s.ListProjects(ctx)
	for _, p := range projRows {
		projects[p.ID] = p.Name
	}
	var out []WorktreeSummary
	for _, w := range wts {
		ts, err := s.TasksByWorktree(ctx, w.ID)
		if err != nil {
			return nil, err
		}
		sum := WorktreeSummary{Worktree: w, Project: projects[w.ProjectID]}
		for _, t := range ts {
			switch t.Status {
			case "done", "skipped":
				sum.DoneCount++
			case "lost":
				sum.LostCount++
			case "dropped":
				// ignore
			case "in_progress":
				sum.ActiveCount++
				if len(sum.InProgressTop) < 3 {
					sum.InProgressTop = append(sum.InProgressTop, t.CurrentTitle)
				}
			default:
				sum.PendingCount++
			}
			if t.LastSeenAt.After(sum.LastActivityAt) {
				sum.LastActivityAt = t.LastSeenAt
			}
		}
		out = append(out, sum)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastActivityAt.After(out[j].LastActivityAt)
	})
	return out, nil
}

// RenderStatus turns an overview into a plain-text table.
func RenderStatus(rows []WorktreeSummary, now time.Time) string {
	if len(rows) == 0 {
		return "no worktrees registered — add one in ~/.config/thin-observer/config.yaml\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-25s %-20s %7s %7s %7s %7s %12s  %s\n",
		"WORKTREE", "PROJECT", "ACTIVE", "PEND", "DONE", "LOST", "LAST", "IN PROGRESS")
	for _, r := range rows {
		last := "-"
		if !r.LastActivityAt.IsZero() {
			last = humanAge(now.Sub(r.LastActivityAt))
		}
		inProgress := strings.Join(r.InProgressTop, "; ")
		if inProgress == "" {
			inProgress = "-"
		}
		name := truncate(r.Worktree.Name, 25)
		proj := truncate(r.Project, 20)
		if r.Worktree.Status == "archived" {
			name = "[archived] " + name
		}
		fmt.Fprintf(&b, "%-25s %-20s %7d %7d %7d %7d %12s  %s\n",
			name, proj, r.ActiveCount, r.PendingCount, r.DoneCount, r.LostCount,
			last, truncate(inProgress, 80))
	}
	return b.String()
}

// Worktree builds a plain-text recap of one worktree's tasks, grouped by phase.
// This output is intended for agent consumption (no ANSI, no color, deterministic).
func Worktree(ctx context.Context, s *store.Store, w store.Worktree) (string, error) {
	tasks, err := s.TasksByWorktree(ctx, w.ID)
	if err != nil {
		return "", err
	}
	// Group by phase preserving source_line order.
	type grp struct {
		phase string
		tasks []store.Task
	}
	order := []string{}
	byPhase := map[string]*grp{}
	for _, t := range tasks {
		if _, ok := byPhase[t.Phase]; !ok {
			byPhase[t.Phase] = &grp{phase: t.Phase}
			order = append(order, t.Phase)
		}
		byPhase[t.Phase].tasks = append(byPhase[t.Phase].tasks, t)
	}
	for _, g := range byPhase {
		sort.Slice(g.tasks, func(i, j int) bool {
			return g.tasks[i].SourceLine < g.tasks[j].SourceLine
		})
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Recap: %s\n", w.Name)
	fmt.Fprintf(&b, "# path: %s\n\n", w.Path)
	for _, ph := range order {
		g := byPhase[ph]
		if ph == "" {
			fmt.Fprintln(&b, "## (ungrouped)")
		} else {
			fmt.Fprintf(&b, "## %s\n", ph)
		}
		for _, t := range g.tasks {
			marker := statusMarker(t.Status)
			extra := ""
			if t.Status == "lost" {
				extra = "  [lost — missing from latest plan]"
			} else if t.Confidence > 0 && t.Confidence < 0.7 {
				extra = fmt.Sprintf("  [low confidence %.2f]", t.Confidence)
			}
			if len(t.SplitFrom) > 0 {
				extra += fmt.Sprintf("  [split from %s]", strings.Join(t.SplitFrom, ","))
			}
			if len(t.MergedFrom) > 0 {
				extra += fmt.Sprintf("  [merged from %s]", strings.Join(t.MergedFrom, ","))
			}
			fmt.Fprintf(&b, "%s %s%s\n", marker, t.CurrentTitle, extra)
		}
		fmt.Fprintln(&b)
	}

	// Brief stats footer (for the agent to read quickly).
	var active, pending, done, lost int
	for _, t := range tasks {
		switch t.Status {
		case "in_progress":
			active++
		case "done", "skipped":
			done++
		case "lost":
			lost++
		case "dropped":
			// skip
		default:
			pending++
		}
	}
	fmt.Fprintf(&b, "# stats: active=%d pending=%d done=%d lost=%d total=%d\n",
		active, pending, done, lost, len(tasks))
	return b.String(), nil
}

// TaskDetail renders a single task's full history.
func TaskDetail(ctx context.Context, s *store.Store, id string) (string, error) {
	t, err := s.TaskByID(ctx, id)
	if err != nil {
		return "", err
	}
	events, _ := s.EventsForTask(ctx, id)
	overrides, _ := s.OverridesForTask(ctx, id)
	var b strings.Builder
	fmt.Fprintf(&b, "# Task %s\n", t.ID)
	fmt.Fprintf(&b, "title      : %s\n", t.CurrentTitle)
	if len(t.Aliases) > 0 {
		fmt.Fprintf(&b, "aliases    : %s\n", strings.Join(t.Aliases, " | "))
	}
	fmt.Fprintf(&b, "phase      : %s\n", t.Phase)
	fmt.Fprintf(&b, "status     : %s\n", t.Status)
	fmt.Fprintf(&b, "confidence : %.2f\n", t.Confidence)
	fmt.Fprintf(&b, "source     : %s:%d\n", t.SourceFile, t.SourceLine)
	fmt.Fprintf(&b, "first_seen : %s\n", t.FirstSeenAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "last_seen  : %s\n", t.LastSeenAt.Format(time.RFC3339))
	if t.RenamedFrom != "" {
		fmt.Fprintf(&b, "renamed_from: %s\n", t.RenamedFrom)
	}
	if len(t.SplitFrom) > 0 {
		fmt.Fprintf(&b, "split_from : %s\n", strings.Join(t.SplitFrom, ", "))
	}
	if len(t.MergedFrom) > 0 {
		fmt.Fprintf(&b, "merged_from: %s\n", strings.Join(t.MergedFrom, ", "))
	}
	fmt.Fprintln(&b, "\n## events")
	for _, e := range events {
		fmt.Fprintf(&b, "  %s  %s\n", e.Timestamp.Format(time.RFC3339), e.Type)
	}
	if len(overrides) > 0 {
		fmt.Fprintln(&b, "\n## overrides")
		for _, o := range overrides {
			fmt.Fprintf(&b, "  %s  %s  %s\n", o.CreatedAt.Format(time.RFC3339), o.Kind, o.Note)
		}
	}
	return b.String(), nil
}

func statusMarker(s string) string {
	switch s {
	case "done":
		return "[x]"
	case "skipped":
		return "[~]"
	case "in_progress":
		return "[/]"
	case "lost":
		return "[?]"
	case "dropped":
		return "[-]"
	default:
		return "[ ]"
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max < 4 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

func humanAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
