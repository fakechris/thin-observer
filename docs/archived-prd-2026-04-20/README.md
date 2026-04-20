# Work Graph Protocol + Linear-like Board Implementation Package

这个包是给 coding agent 直接落地实现用的，不是单纯的讨论纪要。

## 这包里有什么

### 1. Source
- `source/01-original-research-note-2026-04-20.md`
  - 原始 research note 备份
- `source/02-discussion-consolidated.md`
  - 本轮讨论的浓缩版：问题重定义、对象模型、ID 协议、board 思路、md 解析边界、Task Master 取舍、Superpowers 触发分析结论

### 2. PRD
- `prd/PRD-work-graph-linear-board.md`
  - 产品需求文档。适合 coding agent、架构师、前后端一起看。

### 3. Spec
- `spec/work-graph-protocol-v0.1.md`
  - 协议层对象、状态、目录、规则、谱系、事件模型
- `spec/planctl-mcp-contract.md`
  - `planctl` / MCP 接口、同步算法、异常分支
- `spec/board-mapping-and-design.md`
  - protocol → board 的投影规则、状态列、写回规则、UI 设计
- `spec/append-mode-integration-guide.md`
  - 已有 `AGENTS.md` / `CLAUDE.md` / Cursor Rules 时如何“追加而不是替换”
- `spec/skill-installation-and-triggering.md`
  - skill 放哪、怎么装、怎么提高触发稳定性
- `spec/superpowers-trigger-analysis.md`
  - 为什么 Superpowers 看起来“很稳”，以及我们怎么复用它的思路

### 4. Templates
- `templates/AGENTS.append-snippet.md`
- `templates/CLAUDE.append-snippet.md`
- `templates/cursor-work-graph-rule.mdc`
- `templates/skills/...`
  - 建议的四个 skill：
    - `using-work-graph`
    - `writing-work-graph-plan`
    - `executing-work-graph-plan`
    - `reconciling-work-graph`
- `templates/workgraph/...`
  - example plan / sidecar / task / run
- `templates/claude-plugin/...`
  - Claude Code plugin skeleton，用于团队级稳定分发

### 5. TODO
- `todo/implementation-roadmap.md`
- `todo/engineering-todo-checklist.md`

### 6. References
- `references/public-sources.md`
  - 公开资料来源与说明

---

## 推荐阅读顺序

### 给产品/架构先看
1. `prd/PRD-work-graph-linear-board.md`
2. `spec/board-mapping-and-design.md`
3. `spec/superpowers-trigger-analysis.md`

### 给协议/后端先看
1. `spec/work-graph-protocol-v0.1.md`
2. `spec/planctl-mcp-contract.md`
3. `todo/engineering-todo-checklist.md`

### 给接入层先看
1. `spec/append-mode-integration-guide.md`
2. `spec/skill-installation-and-triggering.md`
3. `templates/AGENTS.append-snippet.md`
4. `templates/CLAUDE.append-snippet.md`
5. `templates/cursor-work-graph-rule.mdc`

---

## 关键落地原则

### 1. 追加，不替换
如果仓库已经有 `AGENTS.md` / `CLAUDE.md` / Cursor Rules：
- 不要整文件覆盖
- 不要擅自删已有规则
- 用带哨兵标记的区块“追加”
- 以后只更新这段区块

### 2. board 不是 source of truth
source of truth 在：
- `.workgraph/` 协议状态层
- `planctl` / MCP 操作
- event log
- artifacts

board 只是 projection。

### 3. task 身份必须由工具托管
不要把 task 真身份寄托在自由文本或模型记忆上。
- `task_uid`：机器真身份
- `task_ref`：短引用，例如 `T-07`
- `plan_id` / `plan_rev`：计划身份和修订号

### 4. 先做轻协议，再做厚 UI
建议先把这些做出来：
- `planctl add-task`
- `planctl split-task`
- `planctl merge-tasks`
- `planctl sync-plan`
- `planctl verify`
- `planctl done`

---

## 推荐最小实施顺序

### Phase 1
- 追加接入 `AGENTS.md` / `CLAUDE.md` / Cursor Rule
- 落地 `.workgraph/` 目录与最小 registry
- 落地 4 个 skill

### Phase 2
- 实现 `planctl` 最小命令
- 支持 example plan 的 sync / reconcile

### Phase 3
- 做只读 board
- 列：Ready / Running / Needs Human / Needs Verification / Done

### Phase 4
- 做写回式 board 操作
- split / merge / rename / attach artifact / drag-to-status

---

## 关于 Superpowers 的结论，先说一句

这个包里专门写了一份分析。核心不是“它有神秘 keyword trick”，而是：

- 有一个强势的元技能 / 初始指令层，要求开始任何任务前先检查技能
- skill 拆得很窄，description 对用户意图的覆盖很强
- 支持 first-class plugin / slash command / explicit fallback
- 可把 hooks / monitors / MCP 也一起打包
- 但自动触发本质仍是模型驱动，不是 100% 确定性

所以我们完全可以学它的方法论，但不要假设“只写个 SKILL.md 就一定稳定触发”。
