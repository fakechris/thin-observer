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
	Data       map[string]any
}

type Override struct {
	ID        string
	TaskID    string
	Kind      string // same_as | split_from | merged_from | drop | reopen | rename
	Data      map[string]any
	CreatedAt time.Time
	Note      string
}
