// Package watcher monitors discovered worktrees for plan-file changes.
//
// It fires ChangeEvent entries to a channel. The ingest layer is responsible
// for reading the file, parsing it, and updating task state.
package watcher

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chris/thin-observer/internal/parser"
	"github.com/fsnotify/fsnotify"
)

// ChangeEvent signals that a plan-family file has changed (or may have).
type ChangeEvent struct {
	WorktreePath string
	File         string
	At           time.Time
}

// Watcher is the fsnotify-backed watcher.
type Watcher struct {
	logger   *slog.Logger
	fs       *fsnotify.Watcher
	Events   chan ChangeEvent
	debounce map[string]time.Time
	mu       sync.Mutex
	period   time.Duration
	// worktrees tracks what we're watching; key is worktreePath, value is dirs added
	worktrees map[string]map[string]struct{}
}

func New(logger *slog.Logger) (*Watcher, error) {
	fs, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		logger:    logger,
		fs:        fs,
		Events:    make(chan ChangeEvent, 256),
		debounce:  map[string]time.Time{},
		period:    500 * time.Millisecond,
		worktrees: map[string]map[string]struct{}{},
	}, nil
}

// Close shuts down the watcher and its channel.
func (w *Watcher) Close() error {
	err := w.fs.Close()
	close(w.Events)
	return err
}

// AddWorktree registers a worktree directory (and its immediate subdirs) for
// watching. fsnotify doesn't recurse, but for Phase 1 we only scan the root
// and `.workgraph/plans/` so coverage is cheap.
func (w *Watcher) AddWorktree(path string) error {
	dirs := []string{path}
	for _, sub := range []string{".workgraph/plans", "docs", "plans"} {
		p := filepath.Join(path, sub)
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			dirs = append(dirs, p)
		}
	}
	set := map[string]struct{}{}
	for _, d := range dirs {
		if err := w.fs.Add(d); err != nil {
			w.logger.Warn("watcher.add failed", "dir", d, "err", err)
			continue
		}
		set[d] = struct{}{}
	}
	w.mu.Lock()
	w.worktrees[path] = set
	w.mu.Unlock()
	return nil
}

// Run dispatches fsnotify events through debouncing into w.Events. Returns
// when ctx is done or the underlying fsnotify watcher is closed.
func (w *Watcher) Run(ctx context.Context) {
	tick := time.NewTicker(w.period)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			base := filepath.Base(ev.Name)
			if !parser.LooksLikePlanFile(base) && !strings.HasSuffix(base, ".md") {
				continue
			}
			if !parser.LooksLikePlanFile(base) {
				// Generic .md in the root might still be a plan. Accept only
				// if the parent dir hints at it (e.g., docs/plans/*.md).
				parent := strings.ToLower(filepath.Base(filepath.Dir(ev.Name)))
				if parent != "plans" && parent != ".workgraph" {
					continue
				}
			}
			w.mu.Lock()
			w.debounce[ev.Name] = time.Now()
			w.mu.Unlock()
		case err, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			w.logger.Warn("watcher.error", "err", err)
		case now := <-tick.C:
			w.flush(now)
		}
	}
}

func (w *Watcher) flush(now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for name, ts := range w.debounce {
		if now.Sub(ts) < w.period {
			continue
		}
		wt := w.worktreeFor(name)
		if wt == "" {
			delete(w.debounce, name)
			continue
		}
		select {
		case w.Events <- ChangeEvent{WorktreePath: wt, File: name, At: now}:
		default:
			w.logger.Warn("watcher.events_full")
		}
		delete(w.debounce, name)
	}
}

func (w *Watcher) worktreeFor(file string) string {
	dir := filepath.Dir(file)
	for wt := range w.worktrees {
		if dir == wt || strings.HasPrefix(dir, wt+string(filepath.Separator)) {
			return wt
		}
	}
	return ""
}

// ListKnownPlanFiles returns all currently-existing plan-family files inside
// a worktree. Useful for initial ingestion before fsnotify events start.
func ListKnownPlanFiles(worktree string) []string {
	var out []string
	dirs := []string{worktree}
	for _, sub := range []string{".workgraph/plans", "docs", "plans"} {
		p := filepath.Join(worktree, sub)
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			dirs = append(dirs, p)
		}
	}
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if parser.LooksLikePlanFile(name) {
				out = append(out, filepath.Join(d, name))
				continue
			}
			// Accept any .md under docs/plans or .workgraph/plans.
			parent := strings.ToLower(filepath.Base(d))
			if (parent == "plans" || parent == ".workgraph") && strings.HasSuffix(name, ".md") {
				out = append(out, filepath.Join(d, name))
			}
		}
	}
	return out
}
