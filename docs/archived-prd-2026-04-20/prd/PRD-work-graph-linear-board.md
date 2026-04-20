# PRD — Work Graph Protocol + Linear-like Board

## 1. 背景

当前 coding agent 在多步骤工作中会产出各种临时或半结构化信息：
- plan / todo / checklist
- findings / notes
- progress / session log
- worktree / run / artifact

现有做法通常只能看到：
- 某一个 worktree 当前在做什么
- 某一份 plan 现在长什么样
- 某一次 session 的 commit / transcript

但用户真正关心的是：
- task 的动态性
- 全局可视化看板
- 长期与短期任务共存
- 人与 agent 如何共同维护任务状态
- 一个 task 在重命名、拆分、迁移后如何保持连续性

因此需要一个 protocol layer，把 agent 工作重建成一个可追踪的 work graph，并把它投影成 Linear-like board。

---

## 2. 问题定义

### 现状问题
1. task 来自自由 markdown，身份不稳定
2. task 会 rename / split / merge / disappear
3. board 如果直接绑定 worktree，会混淆执行容器与工作对象
4. 人类无法快速知道：
   - 还有哪些 task
   - 哪些正在被 agent 做
   - 哪些卡住了
   - 哪些已完成但还没验证
5. 如果 agent 改写 plan，旧 task 容易变成 zombie task

### 核心问题
能否建立一套轻协议，让不同 agent：
- 以相同方式维护 plan / task continuity
- 把真相写入可追踪的 registry / event log
- 再统一投影成面向人的 board

---

## 3. 产品目标

### G1. 稳定 task 身份
task 标题可以变化，但 task 身份不丢。

### G2. 跨 agent 兼容
Claude / Codex / Cursor / 其他 agent 都能接到同一协议层。

### G3. 面向人的 Linear-like board
人看到的是清晰 board，而不是一堆 worktree 或自由日志。

### G4. 支持人机共管
长期目标由人维护，短期执行由 agent 维护，系统负责连接二者。

### G5. 可逐步上线
先最小协议 + 最小 board，再逐步加 hooks / plugin / MCP / reconciliation。

---

## 4. 非目标

以下不在第一阶段范围：

- 直接替代 Linear / GitHub Issues
- 做完整 PM SaaS
- 自动解决所有 rename / split / merge 推断
- 在完全无工具配合下，仅靠 prompt 保证一致性
- 自动完成所有人类产品决策

---

## 5. 核心用户

### U1. 单开发者 / 技术负责人
使用 Claude / Codex / Cursor 做多步骤实现，希望掌握全局任务状态。

### U2. 使用 supervisor / worktree 的高级用户
希望观察多个 run / worktree 的并行推进。

### U3. 团队协作者
希望共享 skill / protocol / board，而不是每个人自己手工维持一套 task list。

---

## 6. 核心用户故事

### US1
作为用户，我希望 agent 在创建和修改 plan 时能保留 task 身份，以便 board 不会被计划漂移污染。

### US2
作为用户，我希望看到一个 Linear-like board，知道哪些任务准备好、哪些正在跑、哪些需要我决策、哪些已完成待验证。

### US3
作为用户，我希望原有 `AGENTS.md` 不被覆盖，而是以追加方式接入协议。

### US4
作为用户，我希望 skill 在常见多步骤工作里能稳定触发，但关键路径上也有显式兜底。

### US5
作为用户，我希望旧 task 不会静默消失；如果被废弃或替代，系统能保留谱系。

---

## 7. 产品方案概述

### 7.1 三层结构

#### A. 人类可读层
- `.workgraph/plans/*.md`
- 每个 task 带 `[T-xx]`

#### B. 机器真相层
- registry / sidecar / per-task JSON
- `task_uid` / `plan_id` / `lineage`

#### C. 事件层
- append-only event log
- 支撑 timeline / board projection / history

### 7.2 三类接入层
- `AGENTS.md`
- `CLAUDE.md`
- Cursor Rules / Skills
- 可选 plugin / hooks / MCP

### 7.3 三类 view
- Objective / Project 顶部条
- Task Kanban 主板
- Detail / Timeline / Run / Graph 详情面板

---

## 8. 信息架构

### 长期层
- Objective
- Project

### 执行层
- Plan
- Task

### 运行层
- Run

### 辅助层
- Decision / Blocker
- Artifact
- Event

---

## 9. 主看板设计

## 主列
- Inbox
- Ready
- Running
- Needs Human
- Needs Verification
- Done

### 为什么不是 Todo / Doing / Done
因为 agent 协作里最重要的问题是：
- 这个 task 能否交给 agent
- 现在是否正在执行
- 是否需要人类决策
- 是否只是“agent 说做完了”，但还没验证

---

## 10. 关键功能需求

### F1. Protocol Registry
系统必须保存：
- task_uid
- task_ref
- plan_id
- plan_rev
- lineage
- status
- active run
- artifacts
- blockers

### F2. Plan Sync
系统必须支持：
- 解析 plan markdown
- 对齐 `[T-xx]`
- 检测 missing / split / merge candidate
- 低置信度项目进入 reconcile queue

### F3. Task Lifecycle
系统必须支持：
- create
- update
- split
- merge
- supersede
- drop
- archive
- done
- reopen

### F4. Run Tracking
系统必须把 agent run / worktree 关联到 task。

### F5. Board Projection
系统必须从 registry + events 投影 board，而不是 board 自己维护真相。

### F6. Board Write-back
board 上的修改必须转成 protocol operation，再重算 projection。

### F7. Append-mode integration
若 repo 已有 `AGENTS.md` / `CLAUDE.md` / Rules，只能 append。

### F8. Skill-trigger support
系统必须提供：
- 自动触发路径
- 显式触发路径
- CLI / MCP 硬兜底

---

## 11. 非功能需求

### NFR1. 一致性
不能静默丢 task。
task 消失必须变成可解释的状态转移。

### NFR2. 可解释性
用户必须能回答：
- 这张卡是从哪来的
- 为什么它在这个状态
- 它和哪个 run 绑定
- 它什么时候被 split / merge / supersede

### NFR3. 低侵入
可从“只加少量规则 + 少量 skill + 最小 CLI”开始。

### NFR4. 可共享
能从单 repo 起步，后续演化成团队级 plugin / marketplace。

### NFR5. 可扩展
以后可接：
- supervisor
- subagents
- multi-run
- external issue tracker
- MCP-based validations

---

## 12. 核心流程

### Flow A：新建计划
1. 用户提出复杂任务
2. agent 触发 `using-work-graph` / `writing-work-graph-plan`
3. `planctl init-plan`
4. `planctl add-task ...`
5. 产出 `.workgraph/plans/<plan_id>.md`
6. `planctl sync-plan`

### Flow B：执行计划
1. `planctl attach-run`
2. agent 修改代码
3. 记录 progress / artifacts
4. 如有拆分 / 合并，走对应命令
5. 验证通过后 `planctl done`

### Flow C：计划漂移
1. agent 或人修改 plan markdown
2. `planctl sync-plan`
3. 若发现低置信度差异，进入 reconcile queue
4. 人或 agent 显式 split / merge / supersede / drop

### Flow D：board 写回
1. 用户在 board 上拖卡或改标题
2. 前端转成 protocol command
3. registry / events 更新
4. board 重新投影

---

## 13. 成功指标

### 协议指标
- 95%+ 的 task 在正常编辑中保持稳定 continuity（前提是 `[T-xx]` 保留）
- 0 个 silent delete
- 所有 dropped / superseded task 都有明确事件记录

### board 指标
- 用户可在 30 秒内回答：
  - 当前最重要的 3 个 running task 是什么
  - 哪些卡住需要人
  - 哪些完成但没验证

### 接入指标
- 现有 `AGENTS.md` 仓库可在不破坏原规则的情况下完成接入
- Codex / Claude / Cursor 至少两者能稳定复用同一协议层

---

## 14. 风险与应对

### R1. skill 自动触发不稳定
应对：
- skill description 更窄更明确
- root rules 强化 bootstrap
- 提供显式 slash / mention fallback
- 真一致性放到 `planctl`，不要只押宝 skill

### R2. markdown 自由度太高
应对：
- 主路径要求 `[T-xx]`
- frontmatter 固定
- 低置信度进入 reconcile queue

### R3. 现有 AGENTS 规则冲突
应对：
- append 而不是 replace
- 保留冲突说明
- 显式声明原规则优先

### R4. board 反客为主
应对：
- 强制所有写回都走 protocol operation
- board 只做 projection，不做真相源

---

## 15. 里程碑

### M1. Protocol MVP
- object model
- `.workgraph/`
- `planctl` 最小命令
- repo-level AGENTS/CLAUDE/Rules 接入

### M2. Read-only Board
- Task cards
- main kanban
- detail timeline
- run badges

### M3. Write-back Board
- drag to status
- rename
- split / merge
- attach artifact

### M4. Team Distribution
- Claude plugin skeleton
- Cursor / Codex team skill distribution
- optional marketplace packaging
