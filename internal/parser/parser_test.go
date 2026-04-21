package parser

import (
	"path/filepath"
	"testing"
)

func TestPlanningWithFiles(t *testing.T) {
	doc, err := ParseFile(filepath.Join("..", "..", "testdata", "plans", "planning-with-files.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(doc.Phases), 4; got != want { // H1 + 3 phases
		t.Fatalf("phases = %d, want %d", got, want)
	}
	stats := doc.Stats()
	if stats.TotalTasks != 14 {
		t.Fatalf("total tasks = %d, want 14", stats.TotalTasks)
	}
	if stats.DoneTasks != 5 {
		t.Fatalf("done tasks = %d, want 5", stats.DoneTasks)
	}
	// Phase 1 should be rolled up as done
	var p1 *Phase
	for i := range doc.Phases {
		if doc.Phases[i].Name == "Phase 1: Research" {
			p1 = &doc.Phases[i]
		}
	}
	if p1 == nil {
		t.Fatal("Phase 1 not found")
	}
	if p1.Status != "done" {
		t.Fatalf("Phase 1 status = %q, want done", p1.Status)
	}
}

func TestFrontmatterAndRefs(t *testing.T) {
	doc, err := ParseFile(filepath.Join("..", "..", "testdata", "plans", "with-frontmatter-and-refs.md"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Frontmatter.PlanID != "PLN-20260420-01" {
		t.Fatalf("plan_id = %q", doc.Frontmatter.PlanID)
	}
	if doc.Frontmatter.PlanRev != 4 {
		t.Fatalf("plan_rev = %d", doc.Frontmatter.PlanRev)
	}
	// Titles should have [T-xx] stripped
	found := false
	for _, ph := range doc.Phases {
		for _, task := range ph.Tasks {
			if task.Ref == "T-07" {
				found = true
				if got, want := task.Title, "Add authenticated API endpoints"; got != want {
					t.Fatalf("title = %q, want %q", got, want)
				}
			}
		}
	}
	if !found {
		t.Fatal("T-07 task not found")
	}
}

func TestPlainTodo(t *testing.T) {
	doc, err := ParseFile(filepath.Join("..", "..", "testdata", "plans", "plain-todo.md"))
	if err != nil {
		t.Fatal(err)
	}
	stats := doc.Stats()
	if stats.TotalTasks != 6 {
		t.Fatalf("tasks = %d, want 6", stats.TotalTasks)
	}
	// Statuses include in_progress (/) and skipped (~)
	var hasIP, hasSkipped bool
	for _, ph := range doc.Phases {
		for _, tk := range ph.Tasks {
			if tk.Status == "in_progress" {
				hasIP = true
			}
			if tk.Status == "skipped" {
				hasSkipped = true
			}
		}
	}
	if !hasIP {
		t.Fatal("expected an in_progress task from [/]")
	}
	if !hasSkipped {
		t.Fatal("expected a skipped task from [~]")
	}
}

func TestPlainListItemsInTaskSections(t *testing.T) {
	doc := Parse(`# Task Plan

## Current TODO

1. **Docs closeout**
   - Mark Phase 26 complete everywhere.
   - Point active next reference to Phase 27.

2. **Phase 27 Task 2: Worker runtime visibility**
   - Make action worker state visible beside pipeline runtime state.

## Decisions

1. Do not treat this as a task.
`)

	var todo *Phase
	for i := range doc.Phases {
		if doc.Phases[i].Name == "Current TODO" {
			todo = &doc.Phases[i]
		}
	}
	if todo == nil {
		t.Fatal("Current TODO phase not found")
	}
	if got, want := len(todo.Tasks), 2; got != want {
		t.Fatalf("Current TODO tasks = %d, want %d", got, want)
	}
	if got, want := todo.Tasks[0].Title, "Docs closeout"; got != want {
		t.Fatalf("first title = %q, want %q", got, want)
	}
	if got, want := todo.Tasks[1].Title, "Phase 27 Task 2: Worker runtime visibility"; got != want {
		t.Fatalf("second title = %q, want %q", got, want)
	}
	for _, task := range todo.Tasks {
		if task.Status != "pending" {
			t.Fatalf("task %q status = %q, want pending", task.Title, task.Status)
		}
	}
	for _, ph := range doc.Phases {
		if ph.Name == "Decisions" && len(ph.Tasks) != 0 {
			t.Fatalf("Decisions tasks = %d, want 0", len(ph.Tasks))
		}
	}
}

func TestPlainListSubBulletsAreNotTasks(t *testing.T) {
	doc := Parse(`## Next Steps

- Top-level follow-up
  - nested explanation
  - another nested explanation
- Second follow-up
`)

	if got, want := len(doc.Phases), 1; got != want {
		t.Fatalf("phases = %d, want %d", got, want)
	}
	if got, want := len(doc.Phases[0].Tasks), 2; got != want {
		t.Fatalf("tasks = %d, want %d", got, want)
	}
	if got, want := doc.Phases[0].Tasks[0].Title, "Top-level follow-up"; got != want {
		t.Fatalf("first task = %q, want %q", got, want)
	}
	if got, want := doc.Phases[0].Tasks[1].Title, "Second follow-up"; got != want {
		t.Fatalf("second task = %q, want %q", got, want)
	}
}

func TestProgressLog(t *testing.T) {
	doc, err := ParseFile(filepath.Join("..", "..", "testdata", "plans", "progress-log.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Should parse two "Session" headings as phases
	sessionPhases := 0
	for _, ph := range doc.Phases {
		if ph.Name == "Session 1" || ph.Name == "Session 2" {
			sessionPhases++
		}
	}
	if sessionPhases != 2 {
		t.Fatalf("session phases = %d, want 2", sessionPhases)
	}
}

func TestMessyMix(t *testing.T) {
	doc, err := ParseFile(filepath.Join("..", "..", "testdata", "plans", "messy-mix.md"))
	if err != nil {
		t.Fatal(err)
	}
	stats := doc.Stats()
	if stats.TotalTasks != 7 {
		t.Fatalf("tasks = %d, want 7", stats.TotalTasks)
	}
	// Should accept three different bullet styles (-, *, +)
	var workItems *Phase
	for i := range doc.Phases {
		if doc.Phases[i].Name == "Work Items" {
			workItems = &doc.Phases[i]
		}
	}
	if workItems == nil {
		t.Fatal("Work Items phase missing")
	}
	if len(workItems.Tasks) != 3 {
		t.Fatalf("Work Items tasks = %d, want 3", len(workItems.Tasks))
	}
}

func TestOrphanTasksNoHeading(t *testing.T) {
	// Tasks before any heading should land in (ungrouped).
	content := "- [ ] first\n- [x] second\n## Later\n- [ ] third\n"
	doc := Parse(content)
	var hasUngrouped bool
	for _, ph := range doc.Phases {
		if ph.Name == "(ungrouped)" {
			hasUngrouped = true
		}
	}
	if !hasUngrouped {
		t.Fatal("expected (ungrouped) phase for orphan tasks")
	}
}

func TestExtractPlanLinks_MarkdownLink(t *testing.T) {
	doc := Parse("See [Phase 27 plan](docs/plans/2026-04-21-phase27.md) for details.\n")
	if len(doc.Links) != 1 {
		t.Fatalf("links = %d, want 1 (%#v)", len(doc.Links), doc.Links)
	}
	l := doc.Links[0]
	if l.Target != "docs/plans/2026-04-21-phase27.md" {
		t.Errorf("target = %q", l.Target)
	}
	if l.Label != "Phase 27 plan" {
		t.Errorf("label = %q", l.Label)
	}
	if l.Line != 1 {
		t.Errorf("line = %d, want 1", l.Line)
	}
}

func TestExtractPlanLinks_BarePath(t *testing.T) {
	doc := Parse("Intro paragraph.\n\ndocs/plans/2026-04-21-phase27.md\n")
	var found *PlanLink
	for i := range doc.Links {
		if doc.Links[i].Target == "docs/plans/2026-04-21-phase27.md" {
			found = &doc.Links[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("bare path not extracted; got %#v", doc.Links)
	}
	if found.Label != "" {
		t.Errorf("bare path label = %q, want empty", found.Label)
	}
	if found.Line != 3 {
		t.Errorf("bare path line = %d, want 3", found.Line)
	}
}

func TestExtractPlanLinks_IgnoresHTTP(t *testing.T) {
	doc := Parse("External: [ignore](https://example.com/foo.md) and http://bar.com/baz.md here.\n")
	for _, l := range doc.Links {
		t.Errorf("unexpectedly extracted HTTP link: %#v", l)
	}
}

func TestExtractPlanLinks_DedupesMarkdownLinkTarget(t *testing.T) {
	// A markdown link that points at docs/plans/foo.md should produce exactly
	// one PlanLink, not one from the md-link matcher plus a second from the
	// bare-path matcher catching the same target inside the parens.
	doc := Parse("[foo](docs/plans/foo.md)\n")
	if len(doc.Links) != 1 {
		t.Fatalf("links = %d, want 1 (%#v)", len(doc.Links), doc.Links)
	}
	if doc.Links[0].Label != "foo" {
		t.Errorf("label = %q, want %q", doc.Links[0].Label, "foo")
	}
}

func TestExtractPlanLinks_MultiplePerFile(t *testing.T) {
	content := `## Notes

[A](docs/plans/a.md) and [B](plans/b.md).

plans/c.md

[ignored](https://x.com/nope.md)
`
	doc := Parse(content)
	targets := map[string]bool{}
	for _, l := range doc.Links {
		targets[l.Target] = true
	}
	for _, want := range []string{"docs/plans/a.md", "plans/b.md", "plans/c.md"} {
		if !targets[want] {
			t.Errorf("missing target %q; got %v", want, targets)
		}
	}
	if targets["https://x.com/nope.md"] {
		t.Error("https link must be excluded")
	}
}

func TestLooksLikePlanFile(t *testing.T) {
	cases := map[string]bool{
		"task_plan.md": true,
		"TODO.md":      true,
		"progress.md":  true,
		"random.md":    false,
		"README.md":    false,
	}
	for in, want := range cases {
		if got := LooksLikePlanFile(in); got != want {
			t.Errorf("LooksLikePlanFile(%q) = %v, want %v", in, got, want)
		}
	}
}
