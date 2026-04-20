// thin-observer is a passive observer of coding-agent plan files.
// See /docs/archived-prd-2026-04-20/REASONS-ARCHIVED.md for why this is not
// a cross-agent protocol like the PRD in that directory.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/chris/thin-observer/internal/discovery"
	"github.com/chris/thin-observer/internal/ingest"
	"github.com/chris/thin-observer/internal/parser"
	"github.com/chris/thin-observer/internal/paths"
	"github.com/chris/thin-observer/internal/store"
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
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = out
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

			go wch.Run(ctx)

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

func newULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), ulid.DefaultEntropy()).String()
}
