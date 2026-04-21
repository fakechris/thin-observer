> ⚠ **ARCHIVED — NOT IMPLEMENTED.** This document describes the rejected Work Graph Protocol proposal. See [`../REASONS-ARCHIVED.md`](../REASONS-ARCHIVED.md) for why thin-observer took a different approach.

# Apply This Package To An Existing Repo

## Goal
在不破坏已有 repo 规则的前提下，把 Work Graph Protocol 接进来。

## Steps

1. 先备份原有：
   - `AGENTS.md`
   - `CLAUDE.md`
   - `.cursor/rules/`
2. 把：
   - `templates/AGENTS.append-snippet.md`
   - `templates/CLAUDE.append-snippet.md`
   - `templates/cursor-work-graph-rule.mdc`
   以追加方式接入
3. 放置 skills：
   - Codex → `.agents/skills/`
   - Claude → `.claude/skills/`
   - Cursor → `.cursor/skills/`
4. 初始化：
   - `.workgraph/`
5. 实现最小 `planctl`
6. 用 `templates/workgraph/example-*` 跑一轮同步与 board projection

## Insert policy

- Never replace the whole file unless the repo had no file at all.
- Use sentinel blocks.
- If sentinel already exists, update only that block.
- Keep existing repo-specific rules higher priority.
