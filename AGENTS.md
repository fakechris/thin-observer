# AGENTS.md

This repository is designed to be safe for any coding agent to work in without prior configuration. See [`CLAUDE.md`](CLAUDE.md) for the full brief — the content is agent-neutral and applies equally to Codex, Cursor, Claude Code, or any future tool.

## Quick rules (read before changing code)

1. Don't add a feature that requires the agent (Claude, Codex, Cursor) to do anything special. The project's value is that it works without agent cooperation.
2. Don't write back to `plan.md` / `todo.md` / any watched file. Observer-side state only.
3. Lineage decisions must carry a confidence score. No silent auto-merges.
4. Events are append-only.
5. Closed worktrees go to Archive, not deletion.

See `CLAUDE.md` for the invariants in full and for the architecture map.
