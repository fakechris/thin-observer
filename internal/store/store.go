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
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// dbtx is satisfied by both *sql.DB and *sql.Tx, letting the same methods
// work inside or outside a transaction.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store is a thin wrapper around *sql.DB. Inside WithTx, exec is swapped to a
// *sql.Tx so all calls run in the same transaction.
type Store struct {
	DB   *sql.DB
	Path string
	exec dbtx
}

// Open opens or creates a SQLite database at path, applying the embedded
// schema on first use.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	// foreign_keys pragma + WAL for concurrent reads.
	// file: prefix makes modernc.org/sqlite parse the query string as URI pragmas
	// rather than treating "?..." as part of the filename.
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open: %w", err)
	}
	if err := migrate(context.Background(), db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(context.Background(), db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{DB: db, Path: path, exec: db}, nil
}

// migrate applies schema changes CREATE TABLE IF NOT EXISTS cannot make to
// existing tables (adding columns to already-created tables). Each step is
// idempotent so Open() stays cheap on repeat startups.
func migrate(ctx context.Context, db *sql.DB) error {
	steps := []struct {
		table  string
		column string
		ddl    string
	}{
		// event.snapshot_id added in the multi-plan view / time-machine PR to
		// replace the fragile (worktree_id, timestamp) event-to-snapshot match.
		{"event", "snapshot_id", `ALTER TABLE event ADD COLUMN snapshot_id TEXT REFERENCES snapshot(id)`},
		// plan_doc.missing_since flags files that vanished from disk on the
		// last sweep. NULL = present. We keep the row so historical tasks
		// sourced from it stay queryable.
		{"plan_doc", "missing_since", `ALTER TABLE plan_doc ADD COLUMN missing_since TEXT`},
	}
	for _, s := range steps {
		table, err := tableExists(ctx, db, s.table)
		if err != nil {
			return fmt.Errorf("check %s: %w", s.table, err)
		}
		if !table {
			continue
		}
		has, err := columnExists(ctx, db, s.table, s.column)
		if err != nil {
			return fmt.Errorf("check %s.%s: %w", s.table, s.column, err)
		}
		if has {
			continue
		}
		if _, err := db.ExecContext(ctx, s.ddl); err != nil {
			return fmt.Errorf("add %s.%s: %w", s.table, s.column, err)
		}
	}
	return nil
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count)
	return count > 0, err
}

func columnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&count)
	return count > 0, err
}

// WithTx runs fn inside a SQL transaction. The *Store passed to fn shares the
// same *sql.DB but routes all ExecContext/QueryContext calls through the Tx,
// so every mutating method inside fn is part of the same atomic unit.
// If fn returns an error, the tx is rolled back; otherwise committed.
func (s *Store) WithTx(ctx context.Context, fn func(tx *Store) error) (err error) {
	if s.DB == nil {
		return fmt.Errorf("store: nil DB")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	txStore := &Store{DB: s.DB, Path: s.Path, exec: tx}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
			return
		}
		err = tx.Commit()
	}()
	err = fn(txStore)
	return err
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
	_, err := s.exec.ExecContext(ctx, `
		INSERT INTO project (id, name, root_path, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(root_path) DO UPDATE SET name = excluded.name
	`, p.ID, p.Name, p.RootPath, p.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) ProjectByPath(ctx context.Context, path string) (*Project, error) {
	row := s.exec.QueryRowContext(ctx, `SELECT id, name, root_path, created_at FROM project WHERE root_path = ?`, path)
	var p Project
	var created string
	if err := row.Scan(&p.ID, &p.Name, &p.RootPath, &created); err != nil {
		return nil, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &p, nil
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.exec.QueryContext(ctx, `SELECT id, name, root_path, created_at FROM project ORDER BY name`)
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
	_, err := s.exec.ExecContext(ctx, `
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
	row := s.exec.QueryRowContext(ctx, `
		SELECT id, project_id, name, path, status, first_seen_at, last_seen_at, archived_at
		FROM worktree WHERE path = ?`, path)
	return scanWorktree(row)
}

func (s *Store) WorktreeByID(ctx context.Context, id string) (*Worktree, error) {
	row := s.exec.QueryRowContext(ctx, `
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
	rows, err := s.exec.QueryContext(ctx, q)
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
	_, err := s.exec.ExecContext(ctx, `
		UPDATE worktree SET status = 'archived', archived_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// UnarchiveWorktree flips an archived worktree back to active and clears
// archived_at. Needed because UpsertWorktree intentionally refuses to
// un-archive on conflict (so a stray ingest can't silently resurrect an
// archived row), but when registerAll rediscovers a live path that was
// previously auto-archived, we must explicitly restore it.
func (s *Store) UnarchiveWorktree(ctx context.Context, id string) error {
	_, err := s.exec.ExecContext(ctx, `
		UPDATE worktree SET status = 'active', archived_at = NULL WHERE id = ?`,
		id)
	return err
}

// ---- Snapshot ----

func (s *Store) InsertSnapshot(ctx context.Context, snap Snapshot) error {
	_, err := s.exec.ExecContext(ctx, `
		INSERT INTO snapshot (id, worktree_id, source_file, timestamp, raw_hash, phases_json, commit_sha)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, snap.ID, snap.WorktreeID, snap.SourceFile, snap.Timestamp.Format(time.RFC3339Nano),
		snap.RawHash, snap.PhasesJSON, snap.CommitSHA)
	return err
}

func (s *Store) LatestSnapshot(ctx context.Context, worktreeID, sourceFile string) (*Snapshot, error) {
	row := s.exec.QueryRowContext(ctx, `
		SELECT id, worktree_id, source_file, timestamp, raw_hash, phases_json, COALESCE(commit_sha, '')
		FROM snapshot
		WHERE worktree_id = ? AND source_file = ?
		ORDER BY timestamp DESC
		LIMIT 1`, worktreeID, sourceFile)
	return scanSnapshot(row)
}

// SnapshotsBefore returns the most recent snapshot at or before t.
func (s *Store) SnapshotsBefore(ctx context.Context, worktreeID, sourceFile string, t time.Time) (*Snapshot, error) {
	row := s.exec.QueryRowContext(ctx, `
		SELECT id, worktree_id, source_file, timestamp, raw_hash, phases_json, COALESCE(commit_sha, '')
		FROM snapshot
		WHERE worktree_id = ? AND source_file = ? AND timestamp <= ?
		ORDER BY timestamp DESC
		LIMIT 1`, worktreeID, sourceFile, t.Format(time.RFC3339Nano))
	return scanSnapshot(row)
}

// SnapshotByID returns one snapshot by its ULID.
func (s *Store) SnapshotByID(ctx context.Context, id string) (*Snapshot, error) {
	row := s.exec.QueryRowContext(ctx, `
		SELECT id, worktree_id, source_file, timestamp, raw_hash, phases_json, COALESCE(commit_sha, '')
		FROM snapshot WHERE id = ?`, id)
	return scanSnapshot(row)
}

// ListSnapshots returns snapshots in descending time order. When worktreeID
// is empty, spans all worktrees. A non-positive limit returns all rows.
func (s *Store) ListSnapshots(ctx context.Context, worktreeID string, limit int) ([]Snapshot, error) {
	q := `SELECT id, worktree_id, source_file, timestamp, raw_hash, phases_json, COALESCE(commit_sha, '')
		FROM snapshot`
	var args []any
	if worktreeID != "" {
		q += ` WHERE worktree_id = ?`
		args = append(args, worktreeID)
	}
	q += ` ORDER BY timestamp DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.exec.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Snapshot
	for rows.Next() {
		snap, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *snap)
	}
	return out, rows.Err()
}

// EventsBySnapshot returns events produced during the ingest that created the
// given snapshot. Events carry snapshot_id directly (set by the ingester), so
// this is a straightforward FK lookup — no timestamp-collision risk.
func (s *Store) EventsBySnapshot(ctx context.Context, snapshotID string) ([]Event, error) {
	rows, err := s.exec.QueryContext(ctx, `
		SELECT id, timestamp, type, COALESCE(task_id, ''), COALESCE(worktree_id, ''), COALESCE(snapshot_id, ''), data_json
		FROM event
		WHERE snapshot_id = ?
		ORDER BY id`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var ts, data string
		if err := rows.Scan(&e.ID, &ts, &e.Type, &e.TaskID, &e.WorktreeID, &e.SnapshotID, &data); err != nil {
			return nil, err
		}
		e.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		_ = json.Unmarshal([]byte(data), &e.Data)
		out = append(out, e)
	}
	return out, rows.Err()
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
	aliases, err := json.Marshal(t.Aliases)
	if err != nil {
		return fmt.Errorf("marshal aliases: %w", err)
	}
	splitFrom, err := json.Marshal(t.SplitFrom)
	if err != nil {
		return fmt.Errorf("marshal split_from: %w", err)
	}
	mergedFrom, err := json.Marshal(t.MergedFrom)
	if err != nil {
		return fmt.Errorf("marshal merged_from: %w", err)
	}
	_, err = s.exec.ExecContext(ctx, `
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
	row := s.exec.QueryRowContext(ctx, taskSelectSQL+` WHERE id = ?`, id)
	return scanTask(row)
}

func (s *Store) TasksByWorktree(ctx context.Context, worktreeID string) ([]Task, error) {
	rows, err := s.exec.QueryContext(ctx, taskSelectSQL+` WHERE worktree_id = ? ORDER BY source_line`, worktreeID)
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
	rows, err := s.exec.QueryContext(ctx, taskSelectSQL+` ORDER BY last_seen_at DESC`)
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
	if aliasesJSON != "" {
		if err := json.Unmarshal([]byte(aliasesJSON), &t.Aliases); err != nil {
			return nil, fmt.Errorf("decode aliases for task %s: %w", t.ID, err)
		}
	}
	if splitFromJSON != "" {
		if err := json.Unmarshal([]byte(splitFromJSON), &t.SplitFrom); err != nil {
			return nil, fmt.Errorf("decode split_from for task %s: %w", t.ID, err)
		}
	}
	if mergedFromJSON != "" {
		if err := json.Unmarshal([]byte(mergedFromJSON), &t.MergedFrom); err != nil {
			return nil, fmt.Errorf("decode merged_from for task %s: %w", t.ID, err)
		}
	}
	if first != "" {
		ts, err := time.Parse(time.RFC3339Nano, first)
		if err != nil {
			return nil, fmt.Errorf("parse first_seen_at for task %s: %w", t.ID, err)
		}
		t.FirstSeenAt = ts
	}
	if last != "" {
		ts, err := time.Parse(time.RFC3339Nano, last)
		if err != nil {
			return nil, fmt.Errorf("parse last_seen_at for task %s: %w", t.ID, err)
		}
		t.LastSeenAt = ts
	}
	return &t, nil
}

// ---- Event ----

func (s *Store) InsertEvent(ctx context.Context, e Event) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	data, err := json.Marshal(e.Data)
	if err != nil {
		return fmt.Errorf("marshal event data: %w", err)
	}
	_, err = s.exec.ExecContext(ctx, `
		INSERT INTO event (id, timestamp, type, task_id, worktree_id, snapshot_id, data_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, e.ID, e.Timestamp.Format(time.RFC3339Nano), e.Type,
		nilIfEmpty(e.TaskID), nilIfEmpty(e.WorktreeID), nilIfEmpty(e.SnapshotID), string(data))
	return err
}

func (s *Store) EventsForTask(ctx context.Context, taskID string) ([]Event, error) {
	rows, err := s.exec.QueryContext(ctx, `
		SELECT id, timestamp, type, COALESCE(task_id, ''), COALESCE(worktree_id, ''), COALESCE(snapshot_id, ''), data_json
		FROM event WHERE task_id = ? ORDER BY timestamp`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var ts, data string
		if err := rows.Scan(&e.ID, &ts, &e.Type, &e.TaskID, &e.WorktreeID, &e.SnapshotID, &data); err != nil {
			return nil, err
		}
		e.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		_ = json.Unmarshal([]byte(data), &e.Data)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	rows, err := s.exec.QueryContext(ctx, `
		SELECT id, timestamp, type, COALESCE(task_id, ''), COALESCE(worktree_id, ''), COALESCE(snapshot_id, ''), data_json
		FROM event ORDER BY timestamp DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var ts, data string
		if err := rows.Scan(&e.ID, &ts, &e.Type, &e.TaskID, &e.WorktreeID, &e.SnapshotID, &data); err != nil {
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
	data, err := json.Marshal(o.Data)
	if err != nil {
		return fmt.Errorf("marshal override data: %w", err)
	}
	_, err = s.exec.ExecContext(ctx, `
		INSERT INTO override (id, task_id, kind, data_json, created_at, note)
		VALUES (?, ?, ?, ?, ?, ?)
	`, o.ID, o.TaskID, o.Kind, string(data), o.CreatedAt.Format(time.RFC3339Nano), o.Note)
	return err
}

func (s *Store) OverridesForTask(ctx context.Context, taskID string) ([]Override, error) {
	rows, err := s.exec.QueryContext(ctx, `
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

// ---- PlanDoc ----

// UpsertPlanDoc inserts a plan_doc row or updates the existing row for the
// same (worktree_id, source_file). The caller supplies the ID for the insert
// case; on update the existing ID is preserved. Use PlanDocByWorktreeAndFile
// first if you need to know the stable ID.
func (s *Store) UpsertPlanDoc(ctx context.Context, p PlanDoc) error {
	if p.LastSeenAt.IsZero() {
		p.LastSeenAt = time.Now().UTC()
	}
	// An upsert means the file was just observed — any stale missing_since
	// flag must be cleared.
	_, err := s.exec.ExecContext(ctx, `
		INSERT INTO plan_doc (id, worktree_id, source_file, title, kind, last_snapshot_id, last_seen_at, missing_since)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(worktree_id, source_file) DO UPDATE SET
			title            = excluded.title,
			kind             = excluded.kind,
			last_snapshot_id = excluded.last_snapshot_id,
			last_seen_at     = excluded.last_seen_at,
			missing_since    = NULL
	`, p.ID, p.WorktreeID, p.SourceFile, p.Title, p.Kind,
		nilIfEmpty(p.LastSnapshotID), p.LastSeenAt.Format(time.RFC3339Nano))
	return err
}

// MarkPlanDocsMissing reconciles plan_doc rows for a worktree against the set
// of plan files currently on disk. Rows whose source_file is not in
// presentFiles get missing_since set to `at` (if not already set). Rows whose
// file is present get missing_since cleared. The row itself is never deleted
// so downstream tasks retain a stable plan_id.
//
// presentFiles is structurally bounded: the watcher only scans the worktree
// root plus three fixed subdirectories (plans/, docs/plans/, .workgraph/plans/),
// so `len(presentFiles)` is orders of magnitude below SQLite's host-parameter
// limit (~999 / 32766). No chunking needed.
func (s *Store) MarkPlanDocsMissing(ctx context.Context, worktreeID string, presentFiles []string, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	// Build a placeholder list for NOT IN(...). Empty slice means "everything
	// is missing", which is a legitimate (if alarming) outcome.
	if len(presentFiles) == 0 {
		_, err := s.exec.ExecContext(ctx, `
			UPDATE plan_doc
			SET missing_since = ?
			WHERE worktree_id = ? AND missing_since IS NULL
		`, at.Format(time.RFC3339Nano), worktreeID)
		return err
	}
	args := make([]any, 0, len(presentFiles)+3)
	args = append(args, at.Format(time.RFC3339Nano), worktreeID)
	placeholders := make([]string, len(presentFiles))
	for i, f := range presentFiles {
		placeholders[i] = "?"
		args = append(args, f)
	}
	missQuery := `
		UPDATE plan_doc
		SET missing_since = ?
		WHERE worktree_id = ? AND missing_since IS NULL
		  AND source_file NOT IN (` + strings.Join(placeholders, ",") + `)`
	if _, err := s.exec.ExecContext(ctx, missQuery, args...); err != nil {
		return fmt.Errorf("flag missing: %w", err)
	}
	// Clear missing_since for any file that's now present again.
	clearArgs := make([]any, 0, len(presentFiles)+1)
	clearArgs = append(clearArgs, worktreeID)
	for _, f := range presentFiles {
		clearArgs = append(clearArgs, f)
	}
	clearQuery := `
		UPDATE plan_doc
		SET missing_since = NULL
		WHERE worktree_id = ? AND missing_since IS NOT NULL
		  AND source_file IN (` + strings.Join(placeholders, ",") + `)`
	if _, err := s.exec.ExecContext(ctx, clearQuery, clearArgs...); err != nil {
		return fmt.Errorf("clear missing: %w", err)
	}
	return nil
}

func (s *Store) PlanDocByWorktreeAndFile(ctx context.Context, worktreeID, sourceFile string) (*PlanDoc, error) {
	row := s.exec.QueryRowContext(ctx, `
		SELECT id, worktree_id, source_file, title, kind, COALESCE(last_snapshot_id, ''), last_seen_at, missing_since
		FROM plan_doc WHERE worktree_id = ? AND source_file = ?`, worktreeID, sourceFile)
	return scanPlanDoc(row)
}

func (s *Store) PlanDocByID(ctx context.Context, id string) (*PlanDoc, error) {
	row := s.exec.QueryRowContext(ctx, `
		SELECT id, worktree_id, source_file, title, kind, COALESCE(last_snapshot_id, ''), last_seen_at, missing_since
		FROM plan_doc WHERE id = ?`, id)
	return scanPlanDoc(row)
}

func (s *Store) PlanDocsByWorktree(ctx context.Context, worktreeID string) ([]PlanDoc, error) {
	rows, err := s.exec.QueryContext(ctx, `
		SELECT id, worktree_id, source_file, title, kind, COALESCE(last_snapshot_id, ''), last_seen_at, missing_since
		FROM plan_doc WHERE worktree_id = ? ORDER BY source_file`, worktreeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlanDoc
	for rows.Next() {
		p, err := scanPlanDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func scanPlanDoc(r scanner) (*PlanDoc, error) {
	var p PlanDoc
	var seen string
	var missing sql.NullString
	if err := r.Scan(&p.ID, &p.WorktreeID, &p.SourceFile, &p.Title, &p.Kind, &p.LastSnapshotID, &seen, &missing); err != nil {
		return nil, err
	}
	p.LastSeenAt, _ = time.Parse(time.RFC3339Nano, seen)
	if missing.Valid && missing.String != "" {
		if t, err := time.Parse(time.RFC3339Nano, missing.String); err == nil {
			p.MissingSince = &t
		}
	}
	return &p, nil
}

// TaskCountByPlanDoc returns the current task count for a plan_doc, derived
// from task.source_file. There is no stored count column.
func (s *Store) TaskCountByPlanDoc(ctx context.Context, planDocID string) (int, error) {
	var n int
	err := s.exec.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM task
		JOIN plan_doc ON plan_doc.worktree_id = task.worktree_id AND plan_doc.source_file = task.source_file
		WHERE plan_doc.id = ?`, planDocID).Scan(&n)
	return n, err
}

// ---- PlanLink ----

// ReplacePlanLinks deletes all existing links rooted at fromPlanID and
// inserts the provided set. Callers pass the already-resolved absolute
// to_source_file path so the resolver can match directly.
func (s *Store) ReplacePlanLinks(ctx context.Context, fromPlanID string, links []PlanLink) error {
	if _, err := s.exec.ExecContext(ctx, `DELETE FROM plan_link WHERE from_plan_id = ?`, fromPlanID); err != nil {
		return fmt.Errorf("delete plan_link: %w", err)
	}
	for _, l := range links {
		if _, err := s.exec.ExecContext(ctx, `
			INSERT INTO plan_link (id, from_plan_id, to_source_file, to_plan_id, source_line, label)
			VALUES (?, ?, ?, ?, ?, ?)
		`, l.ID, fromPlanID, l.ToSourceFile, nilIfEmpty(l.ToPlanID), l.SourceLine, l.Label); err != nil {
			return fmt.Errorf("insert plan_link: %w", err)
		}
	}
	return nil
}

func (s *Store) LinksFrom(ctx context.Context, fromPlanID string) ([]PlanLink, error) {
	rows, err := s.exec.QueryContext(ctx, `
		SELECT id, from_plan_id, to_source_file, COALESCE(to_plan_id, ''), source_line, label
		FROM plan_link WHERE from_plan_id = ? ORDER BY source_line`, fromPlanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlanLink
	for rows.Next() {
		var l PlanLink
		if err := rows.Scan(&l.ID, &l.FromPlanID, &l.ToSourceFile, &l.ToPlanID, &l.SourceLine, &l.Label); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ResolvePlanLinkTargets sets plan_link.to_plan_id for any links whose
// to_source_file matches a plan_doc.source_file within the same worktree.
// Links with no matching plan_doc remain unresolved (to_plan_id NULL).
func (s *Store) ResolvePlanLinkTargets(ctx context.Context, worktreeID string) error {
	_, err := s.exec.ExecContext(ctx, `
		UPDATE plan_link
		SET to_plan_id = (
			SELECT pd.id FROM plan_doc pd
			WHERE pd.worktree_id = ? AND pd.source_file = plan_link.to_source_file
			LIMIT 1
		)
		WHERE from_plan_id IN (SELECT id FROM plan_doc WHERE worktree_id = ?)
	`, worktreeID, worktreeID)
	return err
}

// ---- TaskRevision ----

// InsertTaskRevision appends one task_revision row. Never updates — callers
// that want a new state for the same task write another row with a new
// snapshot_id. Defaults RecordedAt to now if zero.
func (s *Store) InsertTaskRevision(ctx context.Context, r TaskRevision) error {
	if r.RecordedAt.IsZero() {
		r.RecordedAt = time.Now().UTC()
	}
	if r.Confidence == 0 {
		r.Confidence = 1.0
	}
	// Normalize nil slices to [] so schema defaults and JSON round-trip are
	// stable — a nil slice marshals to "null" which breaks unmarshal into []string.
	if r.Aliases == nil {
		r.Aliases = []string{}
	}
	if r.SplitFrom == nil {
		r.SplitFrom = []string{}
	}
	if r.MergedFrom == nil {
		r.MergedFrom = []string{}
	}
	aliases, err := json.Marshal(r.Aliases)
	if err != nil {
		return fmt.Errorf("marshal aliases: %w", err)
	}
	splitFrom, err := json.Marshal(r.SplitFrom)
	if err != nil {
		return fmt.Errorf("marshal split_from: %w", err)
	}
	mergedFrom, err := json.Marshal(r.MergedFrom)
	if err != nil {
		return fmt.Errorf("marshal merged_from: %w", err)
	}
	_, err = s.exec.ExecContext(ctx, `
		INSERT INTO task_revision (
			id, snapshot_id, task_id, worktree_id, project_id,
			source_file, title, phase, status, confidence, source_line,
			aliases_json, renamed_from, split_from_json, merged_from_json, supersedes,
			recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.ID, r.SnapshotID, r.TaskID, r.WorktreeID, r.ProjectID,
		r.SourceFile, r.Title, nilIfEmpty(r.Phase), r.Status, r.Confidence, r.SourceLine,
		string(aliases), nilIfEmpty(r.RenamedFrom), string(splitFrom), string(mergedFrom), nilIfEmpty(r.Supersedes),
		r.RecordedAt.Format(time.RFC3339Nano))
	return err
}

const taskRevisionSelectSQL = `SELECT
	id, snapshot_id, task_id, worktree_id, project_id,
	source_file, title, COALESCE(phase, ''), status, confidence, source_line,
	aliases_json, COALESCE(renamed_from, ''), split_from_json, merged_from_json, COALESCE(supersedes, ''),
	recorded_at
FROM task_revision`

func scanTaskRevision(r scanner) (*TaskRevision, error) {
	var tr TaskRevision
	var aliases, splitFrom, mergedFrom, recorded string
	if err := r.Scan(
		&tr.ID, &tr.SnapshotID, &tr.TaskID, &tr.WorktreeID, &tr.ProjectID,
		&tr.SourceFile, &tr.Title, &tr.Phase, &tr.Status, &tr.Confidence, &tr.SourceLine,
		&aliases, &tr.RenamedFrom, &splitFrom, &mergedFrom, &tr.Supersedes,
		&recorded); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(aliases), &tr.Aliases); err != nil {
		return nil, fmt.Errorf("unmarshal aliases: %w", err)
	}
	if err := json.Unmarshal([]byte(splitFrom), &tr.SplitFrom); err != nil {
		return nil, fmt.Errorf("unmarshal split_from: %w", err)
	}
	if err := json.Unmarshal([]byte(mergedFrom), &tr.MergedFrom); err != nil {
		return nil, fmt.Errorf("unmarshal merged_from: %w", err)
	}
	tr.RecordedAt, _ = time.Parse(time.RFC3339Nano, recorded)
	return &tr, nil
}

func (s *Store) TaskRevisionsBySnapshot(ctx context.Context, snapshotID string) ([]TaskRevision, error) {
	rows, err := s.exec.QueryContext(ctx, taskRevisionSelectSQL+` WHERE snapshot_id = ? ORDER BY source_line, title`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskRevision
	for rows.Next() {
		tr, err := scanTaskRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *tr)
	}
	return out, rows.Err()
}

// TaskHistoryByTask returns all revisions of one task in recorded order, oldest
// first. Used by the time-machine UI and by future task-detail pages.
func (s *Store) TaskHistoryByTask(ctx context.Context, taskID string) ([]TaskRevision, error) {
	rows, err := s.exec.QueryContext(ctx, taskRevisionSelectSQL+` WHERE task_id = ? ORDER BY recorded_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskRevision
	for rows.Next() {
		tr, err := scanTaskRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *tr)
	}
	return out, rows.Err()
}
