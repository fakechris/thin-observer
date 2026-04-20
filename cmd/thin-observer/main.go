// thin-observer is a passive observer of coding-agent plan files.
// See /docs/archived-prd-2026-04-20/REASONS-ARCHIVED.md for why this is not
// a cross-agent protocol like the PRD in that directory.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/chris/thin-observer/internal/parser"
	"github.com/chris/thin-observer/internal/paths"
	"github.com/chris/thin-observer/internal/store"
	"github.com/spf13/cobra"
)

var version = "0.1.0-dev"

func main() {
	root := &cobra.Command{
		Use:   "thin-observer",
		Short: "Passive observer of coding-agent plan files. Turns markdown into a kanban.",
		Version: version,
	}
	root.AddCommand(parseCmd())
	root.AddCommand(initCmd())
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
			_ = out // reserved for future --output file
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
