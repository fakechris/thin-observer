package lineage

import (
	"sort"
	"testing"
)

func mkOld(id, title, phase string, aliases ...string) ExistingTask {
	return ExistingTask{ID: id, Title: title, Phase: phase, Aliases: aliases, Status: "pending"}
}
func mkNew(title, phase string) NewItem {
	return NewItem{Title: title, Phase: phase, Status: "pending"}
}

func TestExactMatch(t *testing.T) {
	h := New()
	old := []ExistingTask{mkOld("A", "Install deps", "Stage 1: Setup")}
	now := []NewItem{mkNew("Install deps", "Stage 1: Setup")}
	ds := h.Infer(old, now)
	if len(ds) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(ds))
	}
	if ds[0].Kind != DecisionSameTask || ds[0].OldID != "A" || ds[0].Confidence != 1.0 {
		t.Fatalf("unexpected decision: %+v", ds[0])
	}
}

func TestRenameHighSimilarity(t *testing.T) {
	h := New()
	old := []ExistingTask{mkOld("A", "Install project dependencies", "Stage 1")}
	now := []NewItem{mkNew("Install project dependency", "Stage 1")} // s→""
	ds := h.Infer(old, now)
	if len(ds) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(ds))
	}
	if ds[0].Kind != DecisionRename {
		t.Fatalf("expected rename, got %v", ds[0])
	}
	if ds[0].Confidence < 0.80 || ds[0].Confidence >= 1.0 {
		t.Fatalf("bad confidence: %.2f", ds[0].Confidence)
	}
}

func TestAliasMatch(t *testing.T) {
	h := New()
	old := []ExistingTask{mkOld("A", "Configure CI pipelines", "Stage 1", "Set up CI")}
	now := []NewItem{mkNew("Set up CI", "Stage 1")} // matches alias exactly
	ds := h.Infer(old, now)
	if len(ds) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(ds))
	}
	if ds[0].Kind != DecisionSameTask || ds[0].OldID != "A" {
		t.Fatalf("expected alias SameTask, got %+v", ds[0])
	}
	if ds[0].Confidence != 0.95 {
		t.Fatalf("expected alias confidence 0.95, got %.2f", ds[0].Confidence)
	}
}

func TestNewTaskUnmatched(t *testing.T) {
	h := New()
	old := []ExistingTask{mkOld("A", "Setup database", "Stage 1")}
	now := []NewItem{mkNew("Invent new framework", "Stage 2")}
	ds := h.Infer(old, now)
	if len(ds) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(ds))
	}
	if ds[0].Kind != DecisionNewTask {
		t.Fatalf("expected NewTask, got %+v", ds[0])
	}
}

func TestSplit_OneIntoTwo(t *testing.T) {
	h := New()
	old := []ExistingTask{mkOld("A", "Implement authentication and authorization", "Auth")}
	now := []NewItem{
		mkNew("Implement authentication", "Auth"),
		mkNew("Implement authorization", "Auth"),
	}
	ds := h.Infer(old, now)
	// Expected: one split decision listing both children.
	var splits int
	for _, d := range ds {
		if d.Kind == DecisionSplit {
			splits++
			if len(d.ChildNewIndexes) != 2 {
				t.Fatalf("expected 2 children in split, got %d", len(d.ChildNewIndexes))
			}
			if d.OldID != "A" {
				t.Fatalf("split parent mismatch: %s", d.OldID)
			}
			if d.Confidence <= 0 || d.Confidence > 1 {
				t.Fatalf("bad split confidence: %f", d.Confidence)
			}
		}
	}
	if splits != 1 {
		t.Fatalf("expected exactly 1 split decision, got %d (all: %+v)", splits, ds)
	}
}

func TestMerge_TwoIntoOne(t *testing.T) {
	h := New()
	old := []ExistingTask{
		mkOld("A", "Provision staging", "Ops"),
		mkOld("B", "Provision production", "Ops"),
	}
	now := []NewItem{mkNew("Provision staging and production environments", "Ops")}
	ds := h.Infer(old, now)
	var merges int
	for _, d := range ds {
		if d.Kind == DecisionMerge {
			merges++
			sort.Strings(d.OldIDs)
			if len(d.OldIDs) != 2 || d.OldIDs[0] != "A" || d.OldIDs[1] != "B" {
				t.Fatalf("bad merge parents: %v", d.OldIDs)
			}
		}
	}
	if merges != 1 {
		t.Fatalf("expected exactly 1 merge decision, got %d (all: %+v)", merges, ds)
	}
}

func TestExactMatchAcrossPhases(t *testing.T) {
	// Same title, phase moved. Agents routinely reorganize phases without
	// renaming tasks — we must bind them as SameTask with a penalty rather
	// than let the old task fall into "lost".
	h := New()
	old := []ExistingTask{mkOld("A", "Install dependencies", "Stage 1")}
	now := []NewItem{mkNew("Install dependencies", "Stage 2")}
	ds := h.Infer(old, now)
	if len(ds) != 1 {
		t.Fatalf("expected 1 decision, got %d (%+v)", len(ds), ds)
	}
	if ds[0].Kind != DecisionSameTask || ds[0].OldID != "A" {
		t.Fatalf("expected cross-phase SameTask, got %+v", ds[0])
	}
	// Confidence should reflect the phase penalty.
	if ds[0].Confidence >= 1.0 || ds[0].Confidence < 0.7 {
		t.Fatalf("expected cross-phase conf in [0.7,1.0), got %.2f", ds[0].Confidence)
	}
}

func TestSamePhaseBeatsCrossPhase(t *testing.T) {
	// Two old tasks with the same title exist in different phases. The new
	// task is in one of those phases — we must pick the same-phase candidate,
	// not the cross-phase one.
	h := New()
	old := []ExistingTask{
		mkOld("A", "Install dependencies", "Stage 1"),
		mkOld("B", "Install dependencies", "Stage 2"),
	}
	now := []NewItem{mkNew("Install dependencies", "Stage 2")}
	ds := h.Infer(old, now)
	if len(ds) != 1 || ds[0].Kind != DecisionSameTask {
		t.Fatalf("unexpected decisions: %+v", ds)
	}
	if ds[0].OldID != "B" {
		t.Fatalf("expected same-phase B to win over cross-phase A, got OldID=%q", ds[0].OldID)
	}
	if ds[0].Confidence != 1.0 {
		t.Fatalf("expected same-phase conf 1.0, got %.2f", ds[0].Confidence)
	}
}

func TestMixedScenario(t *testing.T) {
	h := New()
	old := []ExistingTask{
		mkOld("A", "Install deps", "Setup"),                  // unchanged
		mkOld("B", "Configure environment", "Setup"),         // renamed (plural)
		mkOld("C", "Run tests and coverage", "Verify"),       // split into 2
		mkOld("D", "Deprecated background job", "Cleanup"),   // lost → NOT reported here, ingester bumps
	}
	now := []NewItem{
		mkNew("Install deps", "Setup"),
		mkNew("Configure environments", "Setup"),
		mkNew("Run tests", "Verify"),
		mkNew("Run coverage report", "Verify"),
		mkNew("New thing nobody asked for", "Cleanup"),
	}
	ds := h.Infer(old, now)

	var sameCount, renameCount, splitCount, newCount int
	for _, d := range ds {
		switch d.Kind {
		case DecisionSameTask:
			sameCount++
		case DecisionRename:
			renameCount++
		case DecisionSplit:
			splitCount++
		case DecisionNewTask:
			newCount++
		}
	}
	if sameCount != 1 {
		t.Errorf("want 1 same, got %d", sameCount)
	}
	if renameCount != 1 {
		t.Errorf("want 1 rename, got %d", renameCount)
	}
	if splitCount != 1 {
		t.Errorf("want 1 split, got %d", splitCount)
	}
	if newCount != 1 {
		t.Errorf("want 1 new, got %d; decisions=%+v", newCount, ds)
	}
}

func TestRatio(t *testing.T) {
	if r := ratio("hello", "hello"); r != 1.0 {
		t.Fatalf("identical -> 1.0, got %f", r)
	}
	if r := ratio("hello", "helo"); !(r > 0.75 && r < 0.85) {
		t.Fatalf("hello/helo ratio unexpected: %f", r)
	}
	if r := ratio("", ""); r != 1.0 {
		t.Fatalf("both empty -> 1.0, got %f", r)
	}
	if r := ratio("a", ""); r != 0 {
		t.Fatalf("one empty -> 0, got %f", r)
	}
}

func TestJaccard(t *testing.T) {
	a := map[string]struct{}{"x": {}, "y": {}, "z": {}}
	b := map[string]struct{}{"y": {}, "z": {}, "w": {}}
	j := jaccard(a, b)
	if !(j > 0.49 && j < 0.51) {
		t.Fatalf("expected ~0.5, got %f", j)
	}
}

func TestTokenSet(t *testing.T) {
	s := tokenSet("Install the project dependencies (v2)")
	// stopwords "the", short "v2" (digits, len 2) dropped — keep install, project, dependencies.
	want := []string{"install", "project", "dependencies"}
	for _, w := range want {
		if _, ok := s[w]; !ok {
			t.Errorf("missing token %q in %v", w, s)
		}
	}
	if _, ok := s["the"]; ok {
		t.Errorf("stopword 'the' not filtered: %v", s)
	}
}
