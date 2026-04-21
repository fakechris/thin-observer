// thin-observer is a passive observer of coding-agent plan files.
// See /docs/archived-prd-2026-04-20/REASONS-ARCHIVED.md for why this is not
// a cross-agent protocol like the PRD in that directory.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/chris/thin-observer/internal/discovery"
	"github.com/chris/thin-observer/internal/ingest"
	"github.com/chris/thin-observer/internal/lineage"
	"github.com/chris/thin-observer/internal/parser"
	"github.com/chris/thin-observer/internal/paths"
	"github.com/chris/thin-observer/internal/recap"
	"github.com/chris/thin-observer/internal/store"
	"github.com/chris/thin-observer/internal/web"
	"github.com/chris/thin-observer/internal/watcher"
	"github.com/oklog/ulid/v2"
	"github.com/spf13/cobra"
)

var version = "0.1.0-dev"

func main() {
	root := &cobra.Command{
		Use:     "thin-observer",
		Short:   "Passive observer of coding-agent plan files. Turns markdown into a kanban.",
		Version: version,
	}
	root.AddCommand(parseCmd())
	root.AddCommand(initCmd())
	root.AddCommand(watchCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(recapCmd())
	root.AddCommand(taskCmd())
	root.AddCommand(boardCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func parseCmd() *cobra.Command {
	var out string
	c := &cobra.Command{
		Use:   "parse <file.md>",
		Short: "Parse a plan/todo/progress markdown and dump structured JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			doc, err := parser.ParseFile(args[0])
			if err != nil {
				return err
			}
			w := io.Writer(os.Stdout)
			if out != "" {
				f, err := os.Create(out)
				if err != nil {
					return fmt.Errorf("create %s: %w", out, err)
				}
				defer f.Close()
				w = f
			}
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			return enc.Encode(doc)
		},
	}
	c.Flags().StringVarP(&out, "output", "o", "", "write JSON to file instead of stdout")
	return c
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize thin-observer state directory and SQLite db",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(paths.DBPath())
			if err != nil {
				return err
			}
			defer s.Close()
			ctx := context.Background()
			projects, err := s.ListProjects(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "ok: db at %s (%d projects)\n", paths.DBPath(), len(projects))
			return nil
		},
	}
}

func watchCmd() *cobra.Command {
	var configPath string
	var once bool
	c := &cobra.Command{
		Use:   "watch",
		Short: "Daemon: discover worktrees, watch plan files, ingest into DB",
		RunE: func(cmd *cobra.Command, args []string) error {
			if configPath == "" {
				configPath = paths.ConfigFile()
			}
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

			cfg, err := discovery.LoadConfig(configPath)
			if err != nil {
				return fmt.Errorf("load config %s: %w", configPath, err)
			}
			found, err := discovery.Discover(cfg)
			if err != nil {
				return fmt.Errorf("discover: %w", err)
			}
			logger.Info("discovery", "worktrees", len(found), "config", configPath)

			s, err := store.Open(paths.DBPath())
			if err != nil {
				return err
			}
			defer s.Close()

			ctx, cancel := signalContext()
			defer cancel()

			ing := ingest.New(s)
			ing.Inferrer = lineage.New()
			wch, err := watcher.New(logger)
			if err != nil {
				return err
			}
			defer wch.Close()

			// Register projects + worktrees; add to watcher; do initial ingestion.
			wtByPath, err := registerAll(ctx, s, wch, found, logger, ing)
			if err != nil {
				return err
			}

			if once {
				logger.Info("watch.once_complete")
				return nil
			}

			wch.Start(ctx)

			logger.Info("watch.running", "worktrees", len(wtByPath))
			for {
				select {
				case <-ctx.Done():
					logger.Info("watch.stopped")
					return nil
				case ev, ok := <-wch.Events:
					if !ok {
						return nil
					}
					wt := wtByPath[ev.WorktreePath]
					if wt == nil {
						continue
					}
					doc, err := parser.ParseFile(ev.File)
					if err != nil {
						logger.Warn("parse", "file", ev.File, "err", err)
						continue
					}
					res, err := ing.Apply(ctx, *wt, doc)
					if err != nil {
						logger.Warn("ingest", "file", ev.File, "err", err)
						continue
					}
					if res.Unchanged {
						continue
					}
					logger.Info("ingested",
						"file", ev.File,
						"snap", res.SnapshotID,
						"created", res.Created, "updated", res.Updated,
						"renamed", res.Renamed, "split", res.Split,
						"merged", res.Merged, "lost", res.Lost)
				}
			}
		},
	}
	c.Flags().StringVar(&configPath, "config", "", "path to config.yaml (default ~/.config/thin-observer/config.yaml)")
	c.Flags().BoolVar(&once, "once", false, "run one discovery+ingest pass and exit (no fsnotify loop)")
	return c
}

// registerAll upserts project+worktree rows, adds each worktree to the watcher,
// and does an initial ingestion of every plan-family file present. Returns a
// map from worktree path to Worktree row.
func registerAll(
	ctx context.Context,
	s *store.Store,
	wch *watcher.Watcher,
	found []discovery.Found,
	logger *slog.Logger,
	ing *ingest.Ingester,
) (map[string]*store.Worktree, error) {
	out := map[string]*store.Worktree{}
	projects := map[string]string{} // root -> project ID

	for _, f := range found {
		projID, ok := projects[f.ProjectRoot]
		if !ok {
			projID = newULID()
			if err := s.UpsertProject(ctx, store.Project{
				ID:       projID,
				Name:     f.ProjectName,
				RootPath: f.ProjectRoot,
			}); err != nil {
				return nil, fmt.Errorf("upsert project %s: %w", f.ProjectRoot, err)
			}
			if p, err := s.ProjectByPath(ctx, f.ProjectRoot); err == nil {
				projID = p.ID
			}
			projects[f.ProjectRoot] = projID
		}

		// Upsert worktree. If already present, reuse its ID.
		existing, _ := s.WorktreeByPath(ctx, f.WorktreePath)
		var wtID string
		if existing != nil {
			wtID = existing.ID
		} else {
			wtID = newULID()
		}
		wt := store.Worktree{
			ID:        wtID,
			ProjectID: projID,
			Name:      f.WorktreeName,
			Path:      f.WorktreePath,
			Status:    "active",
		}
		if err := s.UpsertWorktree(ctx, wt); err != nil {
			return nil, fmt.Errorf("upsert worktree %s: %w", f.WorktreePath, err)
		}
		stored, err := s.WorktreeByPath(ctx, f.WorktreePath)
		if err != nil {
			return nil, err
		}
		out[f.WorktreePath] = stored

		if err := wch.AddWorktree(f.WorktreePath); err != nil {
			logger.Warn("watcher.add_worktree", "path", f.WorktreePath, "err", err)
		}

		// Initial ingestion: scan for known plan files and parse them.
		for _, plan := range watcher.ListKnownPlanFiles(f.WorktreePath) {
			doc, err := parser.ParseFile(plan)
			if err != nil {
				logger.Warn("parse_initial", "file", plan, "err", err)
				continue
			}
			res, err := ing.Apply(ctx, *stored, doc)
			if err != nil {
				logger.Warn("ingest_initial", "file", plan, "err", err)
				continue
			}
			if !res.Unchanged {
				logger.Info("ingested_initial",
					"file", filepath.Base(plan),
					"wt", f.WorktreeName,
					"created", res.Created, "updated", res.Updated)
			}
		}
	}
	return out, nil
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		cancel()
	}()
	return ctx, cancel
}

func statusCmd() *cobra.Command {
	var includeArchived bool
	c := &cobra.Command{
		Use:   "status",
		Short: "Print a one-screen overview of all tracked worktrees",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(paths.DBPath())
			if err != nil {
				return err
			}
			defer s.Close()
			rows, err := recap.Overview(cmd.Context(), s, includeArchived)
			if err != nil {
				return err
			}
			fmt.Print(recap.RenderStatus(rows, time.Now().UTC()))
			return nil
		},
	}
	c.Flags().BoolVar(&includeArchived, "archived", false, "include archived worktrees")
	return c
}

func recapCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "recap <worktree-name-or-path>",
		Short: "Print a plain-text recap of one worktree's tasks (safe to pipe to an agent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(paths.DBPath())
			if err != nil {
				return err
			}
			defer s.Close()
			w, err := resolveWorktree(cmd.Context(), s, args[0])
			if err != nil {
				return err
			}
			out, err := recap.Worktree(cmd.Context(), s, *w)
			if err != nil {
				return err
			}
			fmt.Print(out)
			return nil
		},
	}
}

func taskCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "task <id>",
		Short: "Show one task's full history, events, and overrides",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(paths.DBPath())
			if err != nil {
				return err
			}
			defer s.Close()
			out, err := recap.TaskDetail(cmd.Context(), s, args[0])
			if err != nil {
				return fmt.Errorf("task %s: %w", args[0], err)
			}
			fmt.Print(out)
			return nil
		},
	}
}

func boardCmd() *cobra.Command {
	var addr string
	c := &cobra.Command{
		Use:   "board",
		Short: "Start the web board (read-only kanban + lineage + archive)",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
			s, err := store.Open(paths.DBPath())
			if err != nil {
				return err
			}
			defer s.Close()
			srv, err := web.New(s, logger)
			if err != nil {
				return err
			}
			logger.Info("board.listening", "addr", addr)
			return srv.ListenAndServe(addr)
		},
	}
	c.Flags().StringVar(&addr, "addr", "127.0.0.1:7777", "bind address")
	return c
}

// resolveWorktree finds a worktree by (in order): exact path, path suffix, or name.
func resolveWorktree(ctx context.Context, s *store.Store, key string) (*store.Worktree, error) {
	if w, err := s.WorktreeByPath(ctx, key); err == nil && w != nil {
		return w, nil
	}
	all, err := s.ListWorktrees(ctx, true)
	if err != nil {
		return nil, err
	}
	// Match by basename or name.
	for i := range all {
		w := &all[i]
		if w.Name == key || filepath.Base(w.Path) == key {
			return w, nil
		}
	}
	// Match by path suffix.
	for i := range all {
		w := &all[i]
		if len(key) > 0 && len(w.Path) >= len(key) && w.Path[len(w.Path)-len(key):] == key {
			return w, nil
		}
	}
	return nil, fmt.Errorf("worktree not found: %s", key)
}

func newULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), ulid.DefaultEntropy()).String()
}
