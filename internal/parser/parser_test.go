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
