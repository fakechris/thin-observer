package store

import "time"

type Project struct {
	ID        string
	Name      string
	RootPath  string
	CreatedAt time.Time
}

type Worktree struct {
	ID          string
	ProjectID   string
	Name        string
	Path        string
	Status      string // active | archived
	FirstSeenAt time.Time
	LastSeenAt  time.Time
	ArchivedAt  *time.Time
}

type Snapshot struct {
	ID          string
	WorktreeID  string
	SourceFile  string
	Timestamp   time.Time
	RawHash     string
	PhasesJSON  string // serialized parser.Phase slice
	CommitSHA   string
}

type Task struct {
	ID             string
	WorktreeID     string
	ProjectID      string
	CurrentTitle   string
	Aliases        []string
	Phase          string
	Status         string  // pending | in_progress | done | skipped | lost | dropped
	Confidence     float64 // 1.0 = exact; <0.4 = low-confidence inference
	SourceFile     string
	SourceLine     int
	FirstSeenAt    time.Time
	LastSeenAt     time.Time
	LastSnapshotID string
	RenamedFrom    string
	SplitFrom      []string
	MergedFrom     []string
	Supersedes     string
	MissingInRev   int // consecutive snapshots where task was absent
}

type Event struct {
	ID         string
	Timestamp  time.Time
	Type       string
	TaskID     string
	WorktreeID string
	SnapshotID string
	Data       map[string]any
}

// PlanDoc is one row per watched markdown file. "Kind" is an observer-side
// classification (task_plan / progress / findings / detailed_plan / unknown).
// MissingSince is set when a full worktree sweep cannot find the source file
// on disk; the row itself stays so we never lose history of tasks that lived
// in the deleted plan. Cleared the next time the file reappears.
type PlanDoc struct {
	ID             string
	WorktreeID     string
	SourceFile     string
	Title          string
	Kind           string
	LastSnapshotID string
	LastSeenAt     time.Time
	MissingSince   *time.Time
}

// PlanLink is a cross-plan reference extracted from markdown. ToSourceFile is
// resolved to an absolute path at ingest time. ToPlanID is set by the resolver
// when a matching plan_doc exists in the same worktree.
type PlanLink struct {
	ID           string
	FromPlanID   string
	ToSourceFile string
	ToPlanID     string
	SourceLine   int
	Label        string
}

// TaskRevision is an append-only record of a task's observed state at a
// given snapshot. Unlike Task, TaskRevision rows are never mutated — the
// time-machine UI reads them to reconstruct a prior board. project_id is
// denormalized (matches the worktree's project at record time) so a
// project-scoped timeline can be served without a join.
type TaskRevision struct {
	ID          string
	SnapshotID  string
	TaskID      string
	WorktreeID  string
	ProjectID   string
	SourceFile  string
	Title       string
	Phase       string
	Status      string
	Confidence  float64
	SourceLine  int
	Aliases     []string
	RenamedFrom string
	SplitFrom   []string
	MergedFrom  []string
	Supersedes  string
	RecordedAt  time.Time
}

type Override struct {
	ID        string
	TaskID    string
	Kind      string // same_as | split_from | merged_from | drop | reopen | rename
	Data      map[string]any
	CreatedAt time.Time
	Note      string
}
