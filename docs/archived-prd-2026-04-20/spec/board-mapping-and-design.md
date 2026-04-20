# Board Mapping and Design

## 1. 基本原则

一句话：

**graph-backed, board-first**

- graph / registry / events 是真相
- board 是人机交互主界面
- worktree / run 只是 task 的执行上下文，不是主 card

---

## 2. 对象映射

### Protocol → Board

- `Objective` → 顶部目标条（类似 Initiative）
- `Project` → 项目条 / 过滤器
- `Task` → 看板主卡片
- `Run` → 卡片执行 badge
- `Decision / Blocker` → 卡片状态 / badge
- `Artifact` → 卡片详情中的附件
- `Plan` → 项目执行计划，不直接当主卡
- `Event` → Timeline / Activity feed

---

## 3. 主看板列

建议固定 6 列：

- Inbox
- Ready
- Running
- Needs Human
- Needs Verification
- Done

### 3.1 Inbox
适用于：
- 新发现的 task
- 低置信度 sync 结果
- 尚未被 triage 的 task

### 3.2 Ready
适用于：
- task 已定义
- 无 blocker
- 可交给 agent

### 3.3 Running
适用于：
- task 被显式标记 `running`
- 或有 active run
- 或最近活跃

### 3.4 Needs Human
适用于：
- decision / blocker 未解决
- reconcile 需要人工判断
- agent 明确请求用户拍板

### 3.5 Needs Verification
适用于：
- 主体工作基本完成
- 但验证未完成或结果未写入协议层

### 3.6 Done
适用于：
- status = done
- 已有验证证据
- 无 open blocker
- 无 active run

---

## 4. 不进主板的状态

以下进入 Archive / History：

- dropped
- superseded
- archived

---

## 5. 卡片字段

每张 task card 最少展示：

- `task_ref`
- `title_current`
- `project`
- `owner_kind`
- `status`
- `active_run`
- `last_activity_at`
- `badges`

### 推荐 card 样式

```text
[T-07] Add authenticated API endpoints
project: API Compatibility MVP
owner: agent
run: wt/api-compat-3
last activity: 4m
⚠ needs human
```

### 建议 badge
- blocked
- stale
- missing
- verified
- split
- merged
- superseded candidate

---

## 6. 页面布局

## 左侧导航
- All Tasks
- Active Runs
- Needs Human
- Needs Verification
- Reconciliation
- Timeline
- Archive

## 顶部条
- Objective selector
- Project filter
- Cycle filter（可选）
- owner / agent / health / stale filter
- search

## 主区域
- Kanban board

## 右侧详情
- Overview
- Timeline
- Runs
- Artifacts
- Graph

---

## 7. 详情页

### Overview
展示：
- task_uid
- task_ref
- title_current
- status
- plan_id
- project
- owner_kind
- active_run
- blockers summary
- verification summary

### Timeline
展示事件：
- created
- renamed
- split
- merged
- blocker opened
- run attached
- progress logged
- verify
- done
- reopen

### Runs
展示：
- run_id
- agent_kind
- worktree_ref
- session_ref
- status
- started_at / ended_at

### Artifacts
展示：
- commit
- PR
- test report
- spec
- review note

### Graph
展示：
- split_from
- merged_from
- supersedes
- blocked_by
- validated_by

---

## 8. 主视图之外的几个必要视图

### 8.1 Active Runs
目标：
- 看现在哪些 agent 在跑
- 哪些 run stale
- run 分别绑定到哪张卡

### 8.2 Needs Human
把人要处理的事情单独拉出来。

### 8.3 Reconciliation
处理低置信度 continuity 的专门视图。

### 8.4 Timeline
看“task 是怎么变成现在这样的”。

### 8.5 Archive
看 dropped / superseded / archived 的历史。

---

## 9. board 写回规则

board 不直接改真相。
只能：
- 发 command
- 更新 registry / events
- 重算 projection

### 9.1 拖卡规则

#### Inbox → Ready
```bash
planctl set-status --task tsk_... --status ready
```

#### Ready → Running
```bash
planctl set-status --task tsk_... --status running
```
或
```bash
planctl attach-run --task tsk_... --agent codex --worktree wt/...
```

#### Running → Needs Human
```bash
planctl block --task tsk_... --kind needs_human --title "..."
```

#### Running → Needs Verification
```bash
planctl set-status --task tsk_... --status needs_verification
```

#### Needs Verification → Done
```bash
planctl verify --task tsk_... --artifact ...
planctl done --task tsk_...
```

### 9.2 改标题
不能直接编辑卡片标题写回本地 state。
必须：

```bash
planctl update-task --task tsk_... --title "..."
```

### 9.3 split
必须：

```bash
planctl split-task --task tsk_parent --child "..." --child "..."
```

### 9.4 merge
必须：

```bash
planctl merge-tasks --task tsk_a --task tsk_b --title "..."
```

---

## 10. “plan 里消失”的处理

### 第一次消失
- 不删
- 标记 `missing_in_rev = true`
- card 可显示 “Missing” badge

### 连续两次消失
- 若无 active run、无显式 lineage
- 进入 `Needs Human` 或 `Dropped Candidate`

### 已显式 lineage
若 agent / 人已明确：
- split
- merge
- supersede
- drop

则按显式事件处理，不再悬挂。

---

## 11. ASCII 线框图

```text
┌──────────────────────────────────────────────────────────────────────┐
│ Objective: Self-hosted Linear-compatible PM backend   Cycle: C-12   │
│ Project: [API Compat MVP] [Import/Migration] [Kanban UI]            │
│ Filters: owner / agent / health / stale / search                    │
├───────────────┬──────────────────────────────────────────────────────┤
│ All Tasks     │ Inbox   Ready   Running   Needs Human   Verify Done │
│ Active Runs   │                                                      │
│ Needs Human   │ [T-11]  [T-07]  [T-03]   [T-09]       [T-05] [T-01] │
│ Reconcile     │ ...      ...     ...       ...          ...    ...   │
│ Timeline      │                                                      │
│ Archive       │                                                      │
├───────────────┼──────────────────────────────────────────────────────┤
│               │ Right Panel                                          │
│               │ [Overview] [Timeline] [Runs] [Artifacts] [Graph]     │
│               │ T-07 Add authenticated API endpoints                 │
│               │ run: wt/api-compat-3                                 │
│               │ blockers: Decide auth header compatibility rule      │
│               │ lineage: split_from T-02                             │
└───────────────┴──────────────────────────────────────────────────────┘
```

---

## 12. 设计取舍

### 为什么不直接用 worktree 当 card
因为：
- 一个 task 可跨多个 worktree
- 一个 worktree 可经历多个阶段
- worktree 是执行容器，不是业务对象

### 为什么不只做 timeline
因为用户日常最需要的是“现在还有什么、谁在跑、谁卡住了”，不是历史回放。

### 为什么 board 不当真相
因为一旦 board 自己维护真相，会很快和：
- markdown
- registry
- events
- artifacts
脱节。
