# Work Graph Protocol v0.1

## 1. 设计目标

协议层负责：
- 稳定身份
- 谱系
- 状态
- 事件
- board 投影输入

它不负责：
- 替代 Git
- 替代 issue tracker
- 自动替代人类决策

---

## 2. 核心对象

## 2.1 Objective
长期目标。

```yaml
objective_id: OBJ-20260420-01
title: Self-hosted Linear-compatible PM backend
status: active
external_ref: LIN-123   # optional
```

## 2.2 Project
可交付项目。

```yaml
project_id: PRJ-20260420-01
objective_id: OBJ-20260420-01
title: API Compatibility MVP
status: active
```

## 2.3 Plan
某一执行计划的身份与修订号。

```yaml
plan_id: PLN-20260420-01
project_id: PRJ-20260420-01
objective_id: OBJ-20260420-01
plan_rev: 4
status: active
```

## 2.4 Task
board 主对象。

```yaml
task_uid: tsk_01K0...
task_ref: T-07
plan_id: PLN-20260420-01
project_id: PRJ-20260420-01
objective_id: OBJ-20260420-01
title_current: Add authenticated API endpoints
aliases:
  - Add API endpoints
status: running
owner_kind: agent
priority: medium
last_seen_rev: 4
last_activity_at: 2026-04-20T20:10:00Z
```

## 2.5 Run
一次 agent 执行实例。

```yaml
run_id: RUN-20260420-03
task_uid: tsk_01K0...
agent_kind: claude-code
worktree_ref: wt/api-compat-3
session_ref: session-abc
status: active
started_at: 2026-04-20T19:00:00Z
last_activity_at: 2026-04-20T20:10:00Z
```

## 2.6 Decision / Blocker
人类输入或外部阻塞。

```yaml
decision_id: DEC-20260420-02
task_uid: tsk_01K0...
kind: needs_human
title: Decide auth header compatibility rule
status: open
```

## 2.7 Artifact
任务产物。

```yaml
artifact_id: ART-20260420-11
task_uid: tsk_01K0...
kind: test_report
ref: reports/api-compat-test.json
```

## 2.8 Event
一切变化都落 event。

```json
{
  "event_id": "EVT-20260420-101",
  "timestamp": "2026-04-20T20:12:00Z",
  "type": "task_split",
  "task_uid": "tsk_01K0...",
  "children": ["tsk_01K1...", "tsk_01K2..."]
}
```

---

## 3. 身份规则

### 3.1 机器真身份
- `objective_id`
- `project_id`
- `plan_id`
- `task_uid`
- `run_id`

创建后不可变。

### 3.2 人类短引用
- `task_ref`

示例：
- `T-01`
- `T-07`
- `T-28`

用于：
- markdown
- board
- human discussion

`task_ref` 应在同一 `plan_id` 内唯一。
长期版本建议依旧通过 `task_uid` 做真正关联。

### 3.3 修订号
- `plan_rev`

每次 plan 结构变化后递增。

---

## 4. 谱系规则

Task 必须显式支持：

- `split_from`
- `merged_from`
- `supersedes`
- `renamed_from`（可体现在 aliases）
- `blocked_by`
- `validated_by`

### 4.1 任务不能静默删除
只允许以下终态：
- done
- dropped
- superseded
- archived

### 4.2 rename 不产生新 task_uid
仅更新：
- `title_current`
- `aliases`

### 4.3 split 产生新 task_uid
原 task 可：
- 变成 umbrella task
- 或变成 superseded

### 4.4 merge 产生新 task_uid
旧任务进入：
- superseded

---

## 5. 状态语义

## 5.1 Task Status
- inbox
- ready
- running
- needs_human
- needs_verification
- done
- dropped
- superseded
- archived

## 5.2 Run Status
- active
- idle
- blocked
- finished
- stale

## 5.3 Decision Status
- open
- resolved
- dropped

---

## 6. 目录结构

推荐：

```text
.workgraph/
  registry.json
  events.jsonl
  plans/
    PLN-20260420-01.md
    PLN-20260420-01.json
  tasks/
    tsk_01K0....json
  runs/
    RUN-20260420-03.json
  views/
    board_cache.json
```

---

## 7. Plan Markdown 规则

每个 plan 文件：

- 位置：`.workgraph/plans/`
- 带固定 frontmatter
- 每个 task 行带 `[T-xx]`

示例：

```md
---
protocol: work-graph/v0.1
objective_id: OBJ-20260420-01
project_id: PRJ-20260420-01
plan_id: PLN-20260420-01
plan_rev: 4
status: active
---

# API Compatibility MVP

## Phase 1 [done]
- [x] [T-01] Establish GraphQL schema subset
- [x] [T-02] Define compatibility tests

## Phase 2 [running]
- [ ] [T-07] Add authenticated API endpoints
- [ ] [T-08] Write integration tests

## Phase 3 [pending]
- [ ] [T-09] Verify mutation parity
```

---

## 8. 强规则

1. 已存在 `[T-xx]` 必须原样保留
2. 新 task 必须先通过工具创建
3. split / merge / supersede 必须显式调用工具
4. 编辑 plan 后必须 `sync-plan`
5. done 前必须先 verify
6. needs_human 必须记录为 blocker / decision

---

## 9. 弱规则 / reconcile

当 markdown 与 registry 不一致时：

### 第一次发现 task 缺失
- 标记 `missing_in_rev = true`
- 保留 task
- 不自动删除

### 连续两次 rev 缺失
- 若无 active run 且无显式 lineage
- 进入 `needs_human` 或 `dropped_candidate`

### 新 task 无 `[T-xx]`
- 尝试相似度匹配
- 若置信度低，进入 reconcile queue

---

## 10. 事件模型

推荐 event type：

- objective_created
- project_created
- plan_created
- plan_synced
- task_created
- task_updated
- task_renamed
- task_split
- task_merged
- task_superseded
- task_dropped
- task_status_changed
- run_attached
- run_status_changed
- blocker_opened
- blocker_resolved
- artifact_attached
- task_verified
- task_done
- task_reopened

---

## 11. Source of Truth

协议层真相来源：

1. `.workgraph/plans/*.md`
2. `.workgraph/plans/*.json`
3. `.workgraph/tasks/*.json`
4. `.workgraph/events.jsonl`
5. `planctl` / MCP commands

Board 不是真相来源。
