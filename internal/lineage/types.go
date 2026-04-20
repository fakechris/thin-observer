// Package lineage infers task identity and split/merge/rename relationships
// between two snapshots of a plan file.
//
// Stage 2 ships only the types (Decision, Inferrer interface, ExistingTask,
// NewItem). A no-op inferrer is also provided. Stage 3 ships the real
// Levenshtein+heuristics inferrer.
package lineage

type DecisionKind int

const (
	DecisionNewTask DecisionKind = iota
	DecisionSameTask
	DecisionRename
	DecisionSplit
	DecisionMerge
)

// Decision is one inferred action against a NewItem (or across several).
type Decision struct {
	Kind             DecisionKind
	OldID            string   // for SameTask / Rename / Split
	OldIDs           []string // for Merge
	NewIndex         int      // index into the NewItem slice
	ChildNewIndexes  []int    // for Split
	Confidence       float64  // 0..1
}

// ExistingTask is the lineage-relevant subset of store.Task.
type ExistingTask struct {
	ID      string
	Title   string
	Aliases []string
	Phase   string
	Status  string
}

// NewItem is the lineage-relevant subset of a parsed task line.
type NewItem struct {
	Phase  string
	Title  string
	Status string
	Line   int
	Ref    string // optional [T-xx] ref
}

// Inferrer decides lineage. Implementations may be pure (no I/O), so they can
// be tested with golden fixtures.
type Inferrer interface {
	Infer(old []ExistingTask, new []NewItem) []Decision
}
