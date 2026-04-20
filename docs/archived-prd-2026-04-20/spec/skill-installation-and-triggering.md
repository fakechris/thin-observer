# Skill Installation and Triggering

## 1. 目标

这一节解决两个问题：

1. skill 放哪、怎么装
2. skill 怎么尽量稳定触发

结论先说：

- 不同 agent 的“入口层”不一样
- 但可以让它们都接到同一个 `planctl` / MCP 协议层
- 自动触发可以做到“很稳”，但不是 100% 确定性
- 关键路径必须有显式 fallback

---

## 2. 推荐 skill 结构

不建议只做一个巨型 skill。
建议拆成 4 个窄 skill：

1. `using-work-graph`
2. `writing-work-graph-plan`
3. `executing-work-graph-plan`
4. `reconciling-work-graph`

原因：
- 描述更清晰
- 边界更稳定
- 匹配更稳
- 更接近 Superpowers 的成功模式

---

## 3. Codex 安装

### 3.1 推荐位置
仓库内：

```text
.agents/skills/<skill-name>/SKILL.md
```

### 3.2 为什么
Codex 官方支持 repo-local skills，且会从当前目录向上扫描 `.agents/skills`。

### 3.3 触发方式
- 隐式：根据 `description`
- 显式：在 CLI / IDE 中通过 skill picker 或直接点名 skill

### 3.4 建议
- 把 shared version 放在 `.agents/skills`
- 对多模块 repo，可在子目录就近放 skill

---

## 4. Claude Code 安装

### 4.1 项目级快速试验
放在：

```text
.claude/skills/<skill-name>/SKILL.md
```

适合：
- 单项目
- 快速迭代
- 先验证 workflow

### 4.2 团队级共享
等 skill 稳定后，再打包成 plugin：

```text
my-plugin/
  .claude-plugin/plugin.json
  skills/
    using-work-graph/SKILL.md
    writing-work-graph-plan/SKILL.md
    ...
```

### 4.3 何时用 standalone，何时用 plugin
- standalone：快、短名、适合试验
- plugin：可分发、可版本化、可附 hooks / MCP / monitors

---

## 5. Cursor 安装

### 5.1 推荐位置
优先使用：

```text
.cursor/skills/<skill-name>/SKILL.md
```

### 5.2 兼容建议
如果你也想兼容 Codex 的 `.agents/skills/`：
- 可以保留 shared source 在 `.agents/skills/`
- 但 Cursor 最终最好镜像到 `.cursor/skills/`

### 5.3 规则层也要加
仅放 skill 不够。
还要放 project rule：
- `.cursor/rules/work-graph-protocol.mdc`

因为：
- Rules 负责持续 bootstrap
- Skills 负责具体 workflow

---

## 6. skill 触发稳定性的核心因素

### 6.1 强 bootstrap
在：
- `AGENTS.md`
- `CLAUDE.md`
- Cursor Rule

里明确写：
- 多步骤任务必须进入 Work Graph Protocol
- 编辑 plan 后必须 sync
- 不得手工发号

没有这层，skill 更容易“应该触发但没触发”。

### 6.2 description 要写“什么时候用”，不是“这个 skill 是什么”
好 description 应该像：
- Use when starting any multi-step engineering task that will need a tracked plan
- Use when updating a plan whose tasks may have drifted
- Use when executing a written plan with stable task refs

而不是：
- This skill manages tasks and plan IDs

### 6.3 窄 skill 胜过胖 skill
一个 skill 只做一件清晰的事。
不要把：
- planning
- execution
- reconcile
- verification
全塞进一个 skill。

### 6.4 body 要短而硬
太长会导致：
- context dilution
- agent不聚焦
- 匹配后仍然执行不稳

应把真正稳定性放在：
- description
- root bootstrap
- `planctl`

### 6.5 关键动作要有显式 fallback
例如：
- 明确要求 agent “first use using-work-graph”
- 在 prompt 中直接点 skill 名
- 或直接调用 `planctl`

---

## 7. 推荐触发策略

### 自动路径
- bootstrap instructions
- implicit skill match
- run / plan context naturally引出 skill

### 显式路径
- 直接要求“先用 using-work-graph”
- 直接要求“按 writing-work-graph-plan 建 plan”
- 直接要求“先执行 sync-plan”
- 直接通过 slash / mention / picker 触发

### 工具兜底
- 即便 skill 没触发，只要 agent 遇到 `.workgraph/` 和 `planctl` 规则，也应被拉回协议路径

---

## 8. 推荐安装方案

### 8.1 单 repo MVP
- `AGENTS.md` append
- `CLAUDE.md` append（如果用 Claude）
- `.cursor/rules/work-graph-protocol.mdc`（如果用 Cursor）
- `.agents/skills/*`
- `.claude/skills/*`
- `.cursor/skills/*`
- `.workgraph/`

### 8.2 团队版
- 保留 repo-local 接入
- 再做 Claude plugin
- 再考虑 marketplace / internal distribution

---

## 9. 提升稳定性的额外手段

### 9.1 Claude
- 先用 standalone 快速迭代
- 稳定后再做 plugin
- plugin 可把 skills + hooks + MCP 一起发
- namespaced slash command 可提供显式兜底

### 9.2 Codex
- repo-local skills 最方便
- 必要时可显式 mention skill
- `AGENTS.md` 本身就能形成强前置约束

### 9.3 Cursor
- Rule + Skill 一起上
- 对项目级 workflow，Rule 比 Skill 更适合做持续启动条件
- Skill 专注在窄工作流

---

## 10. 我们自己的“稳定触发”设计建议

### 推荐组合
- 一个强 bootstrap section
- 四个窄 skill
- 一个统一 authority：`planctl`
- 一个 reconcile queue
- 一个显式 slash / mention fallback

### 不建议
- 只写一个大 skill
- 只靠 prompt 记忆任务身份
- 让 board 自己维护真相
