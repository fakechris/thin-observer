> ⚠ **ARCHIVED — NOT IMPLEMENTED.** This document describes the rejected Work Graph Protocol proposal. See [`../REASONS-ARCHIVED.md`](../REASONS-ARCHIVED.md) for why thin-observer took a different approach.

# Implementation Roadmap

## Phase 0 — Prep
- [ ] 审阅现有 `AGENTS.md` / `CLAUDE.md` / Cursor rules
- [ ] 决定 repo-local 还是 team-shared
- [ ] 决定 `planctl` 是 CLI 优先还是 MCP 优先
- [ ] 建立 `.workgraph/` skeleton
- [ ] 约定 `task_uid` 格式（建议 ULID / KSUID）

---

## Phase 1 — Protocol MVP
- [ ] append `AGENTS.md` 协议块
- [ ] append `CLAUDE.md` 协议块（如用 Claude）
- [ ] 新增 Cursor Rule（如用 Cursor）
- [ ] 放置 4 个 skills
- [ ] 实现：
  - [ ] `init-plan`
  - [ ] `add-task`
  - [ ] `update-task`
  - [ ] `split-task`
  - [ ] `merge-tasks`
  - [ ] `supersede-task`
  - [ ] `drop-task`
  - [ ] `sync-plan`

目标：
- 人工能创建 plan
- agent 能保留 `[T-xx]`
- registry 能保持 continuity

---

## Phase 2 — Execution + Verification
- [ ] `attach-run`
- [ ] `block`
- [ ] `verify`
- [ ] `done`
- [ ] `reopen`
- [ ] artifact persistence
- [ ] events.jsonl

目标：
- 任务生命周期完整闭环
- done 不再只是口头完成

---

## Phase 3 — Read-only Board
- [ ] board projection
- [ ] active runs view
- [ ] timeline view
- [ ] needs human view
- [ ] archive view
- [ ] reconcile view

目标：
- 用户能一眼看到当前全局状态

---

## Phase 4 — Write-back Board
- [ ] drag-to-status
- [ ] rename task
- [ ] split task
- [ ] merge task
- [ ] attach artifact
- [ ] manual block / unblock

目标：
- board 成为主交互面，但不破坏协议真相层

---

## Phase 5 — Reliability Hardening
- [ ] Claude standalone skill install guide验证
- [ ] Claude plugin packaging
- [ ] Codex repo-local skill验证
- [ ] Cursor `.cursor/skills` + rule验证
- [ ] sync-plan reconcile metrics
- [ ] plugin / hook logging
- [ ] migration tests on existing repos

---

## Phase 6 — Supervisor / Multi-run Integration
- [ ] run / worktree discovery adapter
- [ ] stale run detection
- [ ] supervisor attach action
- [ ] multi-run same-task view
