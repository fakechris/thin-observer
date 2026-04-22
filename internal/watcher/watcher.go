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
// Kind is "write" for create/write/rename events and "removed" when the file
// has disappeared from disk. Consumers use Kind to route between re-ingest
// and plan-doc-missing reconciliation without rescanning the worktree.
type ChangeEvent struct {
	WorktreePath string
	File         string
	Kind         string
	At           time.Time
}

// Watcher is the fsnotify-backed watcher.
type Watcher struct {
	logger   *slog.Logger
	fs       *fsnotify.Watcher
	Events   chan ChangeEvent
	debounce map[string]*debounceEntry
	mu       sync.Mutex
	// period is the "quiet window": flush a file when no activity seen for
	// period milliseconds. Streaming agents write continuously though, so we
	// also cap total delay at maxDelay regardless of activity.
	period   time.Duration
	maxDelay time.Duration
	// worktrees tracks what we're watching; key is worktreePath, value is dirs added
	worktrees map[string]map[string]struct{}
	// wg tracks the Run goroutine so Close can wait for it to exit before
	// closing Events, preventing send-on-closed-channel panics.
	wg sync.WaitGroup
}

// debounceEntry records the first and last activity time for one file.
// `first` bounds total delay (streaming writers); `last` drives the quiet
// window (batch writers). `removed` flips true on fsnotify.Remove and back
// to false on any subsequent Write/Create — atomic-save flows that fire
// Remove→Create would otherwise emit a spurious "removed" event.
type debounceEntry struct {
	first   time.Time
	last    time.Time
	removed bool
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
		debounce:  map[string]*debounceEntry{},
		period:    500 * time.Millisecond,
		maxDelay:  5 * time.Second,
		worktrees: map[string]map[string]struct{}{},
	}, nil
}

// Start runs the event loop in a tracked goroutine. Close blocks on it.
// Prefer this over calling Run directly, so Close can safely close Events.
func (w *Watcher) Start(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.Run(ctx)
	}()
}

// Close shuts down the watcher. It closes the underlying fsnotify watcher —
// which makes Run exit on the next loop iteration — then waits for any Run
// goroutine started via Start() to finish.
//
// Note: we deliberately do NOT close w.Events here. Closing a send channel
// from anywhere other than the sender risks send-on-closed panics if a
// concurrent flush is still in-flight. Receivers should select on
// ctx.Done() (or another external signal) to know when to stop reading.
// Once Run has returned, no further values will be sent on Events.
func (w *Watcher) Close() error {
	err := w.fs.Close()
	w.wg.Wait()
	return err
}

// planDirs returns the existing directories under worktree that may contain
// plan-family files: the worktree root plus known plan subdirectories. fsnotify
// does not recurse, so each of these directories must be registered separately
// for runtime watch. The same list drives initial ListKnownPlanFiles scans.
//
// The set is kept deliberately narrow — do not add broad recursive `docs/`
// watching here. If a new plan directory convention emerges, add it explicitly.
var planSubdirs = []string{".workgraph/plans", "docs/plans", "plans"}

func planDirs(worktree string) []string {
	dirs := []string{worktree}
	for _, sub := range planSubdirs {
		p := filepath.Join(worktree, sub)
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			dirs = append(dirs, p)
		}
	}
	return dirs
}

// AddWorktree registers a worktree directory (and its immediate plan subdirs)
// for watching. fsnotify doesn't recurse, so each plan dir is added separately.
func (w *Watcher) AddWorktree(path string) error {
	set := map[string]struct{}{}
	for _, d := range planDirs(path) {
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
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
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
			now := time.Now()
			isRemove := ev.Op&fsnotify.Remove != 0
			w.mu.Lock()
			e, ok := w.debounce[ev.Name]
			if !ok {
				e = &debounceEntry{first: now}
				w.debounce[ev.Name] = e
			}
			e.last = now
			// Atomic writes (rename-over) can fire Remove→Create on the same
			// path; a Write/Create after a Remove means the file is back, so
			// clear the flag. A fresh Remove sets it.
			if isRemove {
				e.removed = true
			} else {
				e.removed = false
			}
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
	for name, e := range w.debounce {
		// Emit when either (a) the file has been quiet for at least one
		// period, or (b) activity has been ongoing for maxDelay — whichever
		// fires first. (b) is the escape hatch for streaming writers that
		// would otherwise starve the quiet window forever.
		quiet := now.Sub(e.last) >= w.period
		tooOld := w.maxDelay > 0 && now.Sub(e.first) >= w.maxDelay
		if !quiet && !tooOld {
			continue
		}
		wt := w.worktreeFor(name)
		if wt == "" {
			delete(w.debounce, name)
			continue
		}
		kind := "write"
		if e.removed {
			kind = "removed"
		}
		select {
		case w.Events <- ChangeEvent{WorktreePath: wt, File: name, Kind: kind, At: now}:
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
	for _, d := range planDirs(worktree) {
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
			// Accept any .md under a plan subdirectory (plans / docs/plans
			// / .workgraph/plans). Base name alone is enough because planDirs
			// already limits which directories we scan.
			parent := strings.ToLower(filepath.Base(d))
			if parent == "plans" && strings.HasSuffix(name, ".md") {
				out = append(out, filepath.Join(d, name))
			}
		}
	}
	return out
}
