// Package store wraps SQLite storage for thin-observer.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Store is a thin wrapper around *sql.DB.
type Store struct {
	DB   *sql.DB
	Path string
}

// Open opens or creates a SQLite database at path, applying the embedded
// schema on first use.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	// foreign_keys pragma + WAL for concurrent reads.
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{DB: db, Path: path}, nil
}

func (s *Store) Close() error {
	if s.DB == nil {
		return nil
	}
	return s.DB.Close()
}

// ---- Project ----

func (s *Store) UpsertProject(ctx context.Context, p Project) error {
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO project (id, name, root_path, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(root_path) DO UPDATE SET name = excluded.name
	`, p.ID, p.Name, p.RootPath, p.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) ProjectByPath(ctx context.Context, path string) (*Project, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, name, root_path, created_at FROM project WHERE root_path = ?`, path)
	var p Project
	var created string
	if err := row.Scan(&p.ID, &p.Name, &p.RootPath, &created); err != nil {
		return nil, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &p, nil
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, root_path, created_at FROM project ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		var created string
		if err := rows.Scan(&p.ID, &p.Name, &p.RootPath, &created); err != nil {
			return nil, err
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- Worktree ----

func (s *Store) UpsertWorktree(ctx context.Context, w Worktree) error {
	now := time.Now().UTC()
	if w.FirstSeenAt.IsZero() {
		w.FirstSeenAt = now
	}
	if w.LastSeenAt.IsZero() {
		w.LastSeenAt = now
	}
	if w.Status == "" {
		w.Status = "active"
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO worktree (id, project_id, name, path, status, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			project_id    = excluded.project_id,
			name          = excluded.name,
			status        = CASE WHEN worktree.status = 'archived' THEN 'archived' ELSE excluded.status END,
			last_seen_at  = excluded.last_seen_at
	`, w.ID, w.ProjectID, w.Name, w.Path, w.Status, w.FirstSeenAt.Format(time.RFC3339Nano), w.LastSeenAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) WorktreeByPath(ctx context.Context, path string) (*Worktree, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, project_id, name, path, status, first_seen_at, last_seen_at, archived_at
		FROM worktree WHERE path = ?`, path)
	return scanWorktree(row)
}

func (s *Store) WorktreeByID(ctx context.Context, id string) (*Worktree, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, project_id, name, path, status, first_seen_at, last_seen_at, archived_at
		FROM worktree WHERE id = ?`, id)
	return scanWorktree(row)
}

func (s *Store) ListWorktrees(ctx context.Context, includeArchived bool) ([]Worktree, error) {
	q := `SELECT id, project_id, name, path, status, first_seen_at, last_seen_at, archived_at FROM worktree`
	if !includeArchived {
		q += ` WHERE status != 'archived'`
	}
	q += ` ORDER BY last_seen_at DESC`
	rows, err := s.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Worktree
	for rows.Next() {
		w, err := scanWorktree(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanWorktree(r scanner) (*Worktree, error) {
	var w Worktree
	var first, last string
	var archived sql.NullString
	if err := r.Scan(&w.ID, &w.ProjectID, &w.Name, &w.Path, &w.Status, &first, &last, &archived); err != nil {
		return nil, err
	}
	w.FirstSeenAt, _ = time.Parse(time.RFC3339Nano, first)
	w.LastSeenAt, _ = time.Parse(time.RFC3339Nano, last)
	if archived.Valid {
		t, _ := time.Parse(time.RFC3339Nano, archived.String)
		w.ArchivedAt = &t
	}
	return &w, nil
}

func (s *Store) ArchiveWorktree(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE worktree SET status = 'archived', archived_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// ---- Snapshot ----

func (s *Store) InsertSnapshot(ctx context.Context, snap Snapshot) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO snapshot (id, worktree_id, source_file, timestamp, raw_hash, phases_json, commit_sha)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, snap.ID, snap.WorktreeID, snap.SourceFile, snap.Timestamp.Format(time.RFC3339Nano),
		snap.RawHash, snap.PhasesJSON, snap.CommitSHA)
	return err
}

func (s *Store) LatestSnapshot(ctx context.Context, worktreeID, sourceFile string) (*Snapshot, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, worktree_id, source_file, timestamp, raw_hash, phases_json, COALESCE(commit_sha, '')
		FROM snapshot
		WHERE worktree_id = ? AND source_file = ?
		ORDER BY timestamp DESC
		LIMIT 1`, worktreeID, sourceFile)
	return scanSnapshot(row)
}

// SnapshotsBefore returns the most recent snapshot at or before t.
func (s *Store) SnapshotsBefore(ctx context.Context, worktreeID, sourceFile string, t time.Time) (*Snapshot, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, worktree_id, source_file, timestamp, raw_hash, phases_json, COALESCE(commit_sha, '')
		FROM snapshot
		WHERE worktree_id = ? AND source_file = ? AND timestamp <= ?
		ORDER BY timestamp DESC
		LIMIT 1`, worktreeID, sourceFile, t.Format(time.RFC3339Nano))
	return scanSnapshot(row)
}

func scanSnapshot(r scanner) (*Snapshot, error) {
	var snap Snapshot
	var ts string
	if err := r.Scan(&snap.ID, &snap.WorktreeID, &snap.SourceFile, &ts, &snap.RawHash, &snap.PhasesJSON, &snap.CommitSHA); err != nil {
		return nil, err
	}
	snap.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
	return &snap, nil
}

// ---- Task ----

func (s *Store) UpsertTask(ctx context.Context, t Task) error {
	aliases, _ := json.Marshal(t.Aliases)
	splitFrom, _ := json.Marshal(t.SplitFrom)
	mergedFrom, _ := json.Marshal(t.MergedFrom)
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO task (
			id, worktree_id, project_id, current_title, aliases_json, phase, status, confidence,
			source_file, source_line, first_seen_at, last_seen_at, last_snapshot_id,
			renamed_from, split_from_json, merged_from_json, supersedes, missing_in_rev)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			current_title    = excluded.current_title,
			aliases_json     = excluded.aliases_json,
			phase            = excluded.phase,
			status           = excluded.status,
			confidence       = excluded.confidence,
			source_file      = excluded.source_file,
			source_line      = excluded.source_line,
			last_seen_at     = excluded.last_seen_at,
			last_snapshot_id = excluded.last_snapshot_id,
			renamed_from     = excluded.renamed_from,
			split_from_json  = excluded.split_from_json,
			merged_from_json = excluded.merged_from_json,
			supersedes       = excluded.supersedes,
			missing_in_rev   = excluded.missing_in_rev
	`, t.ID, t.WorktreeID, t.ProjectID, t.CurrentTitle, string(aliases), t.Phase, t.Status, t.Confidence,
		t.SourceFile, t.SourceLine,
		t.FirstSeenAt.Format(time.RFC3339Nano), t.LastSeenAt.Format(time.RFC3339Nano),
		nilIfEmpty(t.LastSnapshotID),
		nilIfEmpty(t.RenamedFrom), string(splitFrom), string(mergedFrom), nilIfEmpty(t.Supersedes),
		t.MissingInRev)
	return err
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Store) TaskByID(ctx context.Context, id string) (*Task, error) {
	row := s.DB.QueryRowContext(ctx, taskSelectSQL+` WHERE id = ?`, id)
	return scanTask(row)
}

func (s *Store) TasksByWorktree(ctx context.Context, worktreeID string) ([]Task, error) {
	rows, err := s.DB.QueryContext(ctx, taskSelectSQL+` WHERE worktree_id = ? ORDER BY source_line`, worktreeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (s *Store) AllTasks(ctx context.Context) ([]Task, error) {
	rows, err := s.DB.QueryContext(ctx, taskSelectSQL+` ORDER BY last_seen_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

const taskSelectSQL = `SELECT
	id, worktree_id, project_id, current_title, aliases_json, COALESCE(phase, ''), status, confidence,
	COALESCE(source_file, ''), COALESCE(source_line, 0),
	first_seen_at, last_seen_at, COALESCE(last_snapshot_id, ''),
	COALESCE(renamed_from, ''), split_from_json, merged_from_json, COALESCE(supersedes, ''),
	missing_in_rev
FROM task`

func scanTask(r scanner) (*Task, error) {
	var t Task
	var aliasesJSON, splitFromJSON, mergedFromJSON, first, last string
	if err := r.Scan(
		&t.ID, &t.WorktreeID, &t.ProjectID, &t.CurrentTitle, &aliasesJSON, &t.Phase, &t.Status, &t.Confidence,
		&t.SourceFile, &t.SourceLine,
		&first, &last, &t.LastSnapshotID,
		&t.RenamedFrom, &splitFromJSON, &mergedFromJSON, &t.Supersedes,
		&t.MissingInRev); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(aliasesJSON), &t.Aliases)
	_ = json.Unmarshal([]byte(splitFromJSON), &t.SplitFrom)
	_ = json.Unmarshal([]byte(mergedFromJSON), &t.MergedFrom)
	t.FirstSeenAt, _ = time.Parse(time.RFC3339Nano, first)
	t.LastSeenAt, _ = time.Parse(time.RFC3339Nano, last)
	return &t, nil
}

// ---- Event ----

func (s *Store) InsertEvent(ctx context.Context, e Event) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	data, _ := json.Marshal(e.Data)
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO event (id, timestamp, type, task_id, worktree_id, data_json)
		VALUES (?, ?, ?, ?, ?, ?)
	`, e.ID, e.Timestamp.Format(time.RFC3339Nano), e.Type,
		nilIfEmpty(e.TaskID), nilIfEmpty(e.WorktreeID), string(data))
	return err
}

func (s *Store) EventsForTask(ctx context.Context, taskID string) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, timestamp, type, COALESCE(task_id, ''), COALESCE(worktree_id, ''), data_json
		FROM event WHERE task_id = ? ORDER BY timestamp`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var ts, data string
		if err := rows.Scan(&e.ID, &ts, &e.Type, &e.TaskID, &e.WorktreeID, &data); err != nil {
			return nil, err
		}
		e.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		_ = json.Unmarshal([]byte(data), &e.Data)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, timestamp, type, COALESCE(task_id, ''), COALESCE(worktree_id, ''), data_json
		FROM event ORDER BY timestamp DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var ts, data string
		if err := rows.Scan(&e.ID, &ts, &e.Type, &e.TaskID, &e.WorktreeID, &data); err != nil {
			return nil, err
		}
		e.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		_ = json.Unmarshal([]byte(data), &e.Data)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- Override ----

func (s *Store) InsertOverride(ctx context.Context, o Override) error {
	if o.CreatedAt.IsZero() {
		o.CreatedAt = time.Now().UTC()
	}
	data, _ := json.Marshal(o.Data)
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO override (id, task_id, kind, data_json, created_at, note)
		VALUES (?, ?, ?, ?, ?, ?)
	`, o.ID, o.TaskID, o.Kind, string(data), o.CreatedAt.Format(time.RFC3339Nano), o.Note)
	return err
}

func (s *Store) OverridesForTask(ctx context.Context, taskID string) ([]Override, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, task_id, kind, data_json, created_at, COALESCE(note, '')
		FROM override WHERE task_id = ? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Override
	for rows.Next() {
		var o Override
		var created, data string
		if err := rows.Scan(&o.ID, &o.TaskID, &o.Kind, &data, &created, &o.Note); err != nil {
			return nil, err
		}
		o.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		_ = json.Unmarshal([]byte(data), &o.Data)
		out = append(out, o)
	}
	return out, rows.Err()
}
