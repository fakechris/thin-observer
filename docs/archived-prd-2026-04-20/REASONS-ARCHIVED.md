# Why this PRD package is archived (not implemented)

Date: 2026-04-20

This directory is a verbatim copy of `work-graph-board-package-2026-04-20.zip`. It is **archived, not implemented**. thin-observer is an intentionally smaller project that shares some of the same goals but rejects the core architectural bet.

## The PRD's core bet

The PRD proposes a cross-agent **Work Graph Protocol** where coding agents (Claude Code, Codex, Cursor, ...) cooperate via:

- Stable task refs `[T-xx]` embedded in plan markdown
- A `planctl` CLI (15+ commands: add-task, split-task, merge-tasks, sync-plan, verify, done, ...)
- An equivalent MCP server
- 4 narrow skills distributed per agent
- AGENTS.md / CLAUDE.md / Cursor Rules append templates
- A Linear-like board with write-back

## Why we rejected it

1. **Core assumption is fragile**. The whole architecture rests on "agents will reliably preserve `[T-xx]`, call planctl, and sync-plan after edits." The PRD itself admits (in `spec/superpowers-trigger-analysis.md`) that skill-driven triggering is not deterministic. When the assumption fails — and it will fail often — the system degrades to a slightly prettier markdown parser, which is exactly what thin-observer does directly.

2. **Three competing sources of truth**. `plans/*.md` (agent writes), `.workgraph/*.json` (planctl writes), and board projection cache are all persisted and must be kept consistent via sync-plan + reconcile-queue + similarity heuristics. This is a weakened distributed-consistency problem the PRD never fully acknowledges.

3. **Ecosystem fragility**. The PRD couples the project's maintenance cost to the hook formats of Claude Code, Codex, Cursor, Copilot CLI, Gemini CLI, OpenCode, Factory, Kiro, Manus — all of which move fast and in incompatible ways.

4. **Scope drift from the original research**. The original research note (`source/01-original-research-note-2026-04-20.md`) describes a **passive observer** with zero agent cooperation. The PRD inflated that into a cross-agent protocol stack roughly 100x larger in scope.

## What thin-observer does instead

- Same user-facing features (task entity, kanban board, lineage tracking, archive, drift detection)
- **But inference happens on the observer side**, using Levenshtein + phase-overlap + confidence heuristics — not agent-declared lineage
- Zero agent cooperation required: agents can keep writing free-form plan.md, todo.md, progress.md exactly as they do today
- No planctl, no MCP, no skills, no plugin distribution, no AGENTS.md append templates, no markdown write-back

**Cost of this choice**: lineage inference will be wrong sometimes, so the board surfaces low-confidence inferences with a ⚠ and lets users manually override. Overrides are stored observer-side (in SQLite) and never touch plan files.

**Benefit**: the project can evolve unilaterally without chasing the agent ecosystem.

## When to revisit

If, after using thin-observer's MVP for ≥1 month, real pain points emerge that require agent cooperation (e.g., "observer-side inference fails too often for my workflow"), come back to this directory and selectively adopt:

- `spec/work-graph-protocol-v0.1.md` — object model and event types
- `spec/planctl-mcp-contract.md` — CLI contract
- `spec/board-mapping-and-design.md` — board column semantics
- `templates/skills/` — skill templates
- `templates/claude-plugin/` — plugin scaffolding

Until then, this directory is read-only reference material.
