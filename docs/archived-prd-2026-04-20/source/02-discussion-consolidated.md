> ⚠ **ARCHIVED — NOT IMPLEMENTED.** This document describes the rejected Work Graph Protocol proposal. See [`../REASONS-ARCHIVED.md`](../REASONS-ARCHIVED.md) for why thin-observer took a different approach.

# Discussion Consolidated

## 1. 原始 research 的问题，不在方向错，而在抽象层太低

原始 note 更像：
- watcher 计划文件
- parse checklist / phase
- render worktree dashboard

但真正要研究的不是“计划文件如何展示”，而是：

- task 如何随时间演化
- 一个 task 如何在重命名 / 拆分 / 合并 / worktree 迁移后保持连续性
- 人如何从全局视角理解 agent 正在干什么
- 人和 agent 如何分别维护长期目标与短期执行
- board 应该如何投影这些真相，而不是掩盖它们

所以问题应该从：

> 被动跟踪 Plan / Todo / Progress 文件变化，并将其映射为可视化看板

重定义成：

> 从 agent 的 plan / findings / progress / worktree / run / artifact 中持续重建一个 versioned work graph，并把它投影成面向人的 Linear-like board。

---

## 2. Task 不是 markdown 的一行，而是 versioned entity

一行 checklist 只是某个时刻的一段证据，不是真正的 task 身份。

agent 工作里会发生：
- rename
- split
- merge
- scope expand
- scope shrink
- reopen
- abandon
- migrate to another worktree
- move from “research note” to “real task”
- move from “real task” to “needs human decision”

所以需要一个中间层：

- `Task`
- `Plan`
- `Run`
- `Decision / Blocker`
- `Artifact`
- `Event`

真正要追的是 task 的生命线，而不是某一行文本。

---

## 3. 全局 board 的主对象不应该是 worktree

worktree 是执行容器，不是 board card。

正确分层应该是：

### 长期层
- Objective
- Project

### 执行层
- Task（主看板 card）

### 运行层
- Run（worktree / session / agent）

所以：
- card 不是 worktree
- 同一个 task 可以前后挂多个 run
- 同一个 run 也不一定只对应一个长寿命 task

---

## 4. 最适合的 board 形态：graph-backed, board-first

底层是真正的 work graph，前台主要看起来像 Linear：

### 顶部
- Objective / Project 条
- Filter / search / health / cycle

### 主看板
- Inbox
- Ready
- Running
- Needs Human
- Needs Verification
- Done

### 右侧详情
- Overview
- Timeline
- Runs
- Artifacts
- Graph / Lineage

---

## 5. 三类 markdown 文件能不能拆进去

### `task_plan.md`
最适合做 board 主输入。
通常能稳定提取：
- phase
- checklist
- done / running / pending
- progress count
- next steps

### `findings.md`
不适合直接变成大量 board card。
更适合挂到 task 详情页，作为：
- decision
- blocker
- note
- evidence

### `progress.md`
不适合直接变成 task。
更适合形成：
- activity timeline
- active / idle / stale
- test / verification entries
- run execution feed

结论：
- 可以拆
- 但不应强求“正文 100% 还原为 board”
- 正确做法是“结构化一部分，保留证据层一部分”

---

## 6. 真正难的不是 parse，而是 continuity

最难的不是识别：
- `- [ ] foo`

而是识别：
- 这是不是同一个 task 的改名？
- 这是 split 还是新增？
- 旧 task 消失了，是 dropped 还是 superseded？
- 这个 finding 是 note 还是 blocker？
- 这个 progress entry 是完成、失败、重试还是仅仅一段叙述？

所以要引入：
- stable ID
- lineage
- confidence
- reconcile queue

---

## 7. 只加 `plan_id` 不够，必须有 task 身份

需要至少三层：

- `objective_id`
- `project_id`
- `plan_id`
- `plan_rev`
- `task_uid`
- `task_ref`

其中：
- `task_uid`：全局不可变，机器真相
- `task_ref`：人类可读短引用，例如 `[T-07]`

如果只有 `plan_id`，无法解决：
- 旧 task 悬挂
- 新 task 与旧 task 高相似但断链
- rename / split / merge 混淆

---

## 8. ID 不该主要靠模型自由生成

最稳的做法是：

- prompt / rules / skill：要求 agent 保留和引用 ID
- `planctl` / MCP：负责发号、谱系、状态变更、同步、验证

也就是：

### 模型负责
- 读取
- 保留
- 调用工具
- 更新 plan markdown

### 工具负责
- 真身份
- 谱系
- sync / reconcile
- source of truth

---

## 9. 为什么不直接照搬 `claude-task-master`

它很值得参考，因为：
- 它已经把任务身份从自由文本里拿出来
- 它支持 add / update / move / status / subtask
- 它说明“任务真相应该由工具托管”

但它更像一个“完整任务系统”，不是一个“给任意自由 plan 文件补协议层”的轻方案。

主要差异：
- 它的 ID 更像列表地址，不完全是全局不可变 UID
- 它没有把 `plan_id / plan_rev` 当一等对象
- 它默认 agent 接受 Task Master 的整套工作方式

所以应借鉴其思想，不应直接照抄其具体 ID 形态与系统边界。

---

## 10. 已有 `AGENTS.md` 时必须追加，不可替换

如果用户已有：
- `AGENTS.md`
- `CLAUDE.md`
- Cursor Rules

那么我们只能：
- append
- 用固定哨兵区块包住
- 后续只 patch 那个区块

不能整文件覆盖。因为：
- 用户已有 repo 规则可能更高优先级
- 破坏原文件会引入大量无关副作用
- Superpowers 自己也明确承认“用户显式指令高于技能”

---

## 11. 为什么 Superpowers 看起来“稳定触发”

不是玄学。

它的公开特征大致是：

### A. 元技能 / 初始指令很强
`using-superpowers` 的描述非常激进：
- starting any conversation
- before any response including clarifying questions
- if there is even a 1% chance a skill applies, must invoke

这不是普通 skill description，而是一个很强的 bootstrap layer。

### B. 技能拆分非常窄
例如：
- brainstorming
- writing-plans
- executing-plans
- requesting-code-review
- finishing-a-development-branch

每个 skill 都只覆盖一个很清晰的用户意图。

### C. description 对齐用户意图，不讲内部机制
比如：
- “Use when you have a spec or requirements for a multi-step task, before touching code”
- “Use when you have a written implementation plan to execute...”

这正符合 Agent Skills 对 description 的最佳实践。

### D. first-class integration
Superpowers 在不同 agent 里不是单纯“放个 markdown 文件”，而是：
- root instructions
- skills
- plugin integration
- some platforms have slash command or tool-level activation

### E. 显式 fallback
即便自动触发不发生，仍可显式调用技能。

---

## 12. 我们也能做得很稳，但别追求“纯自动 100%”

正确目标是：

### 自动高概率触发
靠：
- 窄 skill
- aggressive description
- root bootstrap instructions
- repo rules
- plugin / local skill placement

### 显式 deterministic fallback
靠：
- slash command
- direct skill mention
- explicit “first use using-work-graph”
- planctl / MCP hard authority

所以我们要做的是：
- 提高自动触发率
- 降低误触发率
- 在关键路径上提供显式兜底
- 把真正的状态一致性放到工具层，而不是只押宝 skill activation
