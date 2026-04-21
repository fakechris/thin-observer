# CLAUDE.md — thin-observer

This file is a standing brief for AI coding agents working in this repository. It's not a skill, not a protocol — just the invariants that matter and the things it's easy to get wrong.

## What this project is

A passive observer that watches `plan.md` / `todo.md` / `progress.md` files across worktrees and projects a kanban board out of them. **Observer-side inference only.** No cross-agent protocol.

## The seven invariants

Please do not violate these without first reading `docs/archived-prd-2026-04-20/REASONS-ARCHIVED.md` and discussing. These are why the project exists.

1. **Markdown is the agent's truth layer.** thin-observer never writes it back. The whole point.
2. **Zero agent cooperation.** No `[T-xx]` protocol, no CLI calls from agents, no installed skills. Any PR adding such requirements should be rejected.
3. **Every lineage decision carries a confidence score.** Low-confidence results go to "Needs Attention"; they do not silently merge.
4. **Fail loud, never auto-correct.** Drift and lost tasks warn — never fix themselves.
5. **Append-only events.** No log deletion, not even during rotation.
6. **Worktree death ≠ task death.** When a worktree is removed, its tasks go to Archive (still queryable), not to /dev/null.
7. **Board is read-only** with respect to the plan file. Manual overrides live only in observer state.

## Architecture map

```
cmd/thin-observer/main.go        # cobra CLI: watch, status, recap, task, board, parse, init
internal/parser/                 # tolerant markdown → PlanDoc
internal/store/                  # SQLite schema + CRUD (modernc.org/sqlite, pure Go)
internal/discovery/              # YAML config + git worktree enumeration
internal/watcher/                # fsnotify + debounce
internal/lineage/                # heuristic inferrer (levenshtein + token-jaccard)
internal/ingest/                 # applies lineage decisions to store
internal/recap/                  # status / recap / task text rendering
internal/web/                    # read-only kanban (net/http + html/template + go:embed)
internal/paths/                  # XDG dir resolution
```

## Conventions

- **Go 1.25+**, single binary, pure Go (no cgo). `modernc.org/sqlite` (not `mattn/go-sqlite3`) so cross-compilation is trivial.
- **Test next to the code.** `_test.go` in the same package.
- **Tolerant parsing.** The parser must accept messy real-world agent output. When it can't, log and move on — never crash the daemon.
- **Deterministic tests.** Lineage heuristics especially: every test uses fixed inputs and sorts decisions.
- **Small, surgical commits.** Each stage (1–6) shipped as one commit.

## When you're tempted to add a feature

- Something like `planctl` / MCP / cross-agent coordination → **reject**. That was the PRD we archived. See the invariants.
- Writing back to markdown → **reject**. Not ever. Not even as a "convenience".
- Required agent configuration → **reject**. A feature that depends on the agent doing something is not a feature this project offers.

If unsure, say so in the PR and flag an invariant you might be bending.

## Testing the MVP end-to-end

The definition of "works" is: run it against your own coding workflow for two weeks. The user's original pains:

1. `thin-observer status` — see all worktrees at a glance.
2. `thin-observer recap <wt>` — paste into a new agent session, watch it continue without forgetting.
3. Modify a plan.md to drop tasks → drift detected; tasks marked `lost` after two misses.
4. `thin-observer board` open all day → glanceable "what's going on".
5. `git worktree remove` a done worktree → archive has it.
6. Split one task into two in the plan.md → board shows split inference with confidence.
7. Do all of this with **zero** changes to any agent config.

Any of those failing = stop, investigate. Do not introduce cross-agent coordination to make a test pass.
