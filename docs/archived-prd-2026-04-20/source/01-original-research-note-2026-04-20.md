> ⚠ **ARCHIVED — NOT IMPLEMENTED.** This document describes the rejected Work Graph Protocol proposal. See [`../REASONS-ARCHIVED.md`](../REASONS-ARCHIVED.md) for why thin-observer took a different approach.

Research note — 2026-04-20
>
问题：能否在 Supervisor 未被唤起时，被动跟踪 coding agent 的
Plan / Todo / Progress 文件变化，并将其映射为可视化看板？
>
Sources:
>
- Entire CLI (entire.io, github.com/entireio/cli)
- planning-with-files skill (github.com/OthmanAdi/planning-with-files)
- Agent Skills open spec (agentskills.io)
- KanVibe, VS Code Agent Kanban, AI Agent Board
- JetBrains Air / Central
- thin-supervisor 现有 session_index / global_registry


Part 1: Plan 文件的来源与标准化现状
1.1 关键发现：Plan 文件不是 Agent 原生行为
Claude Code、Codex、Gemini CLI、Cursor 等 coding agent 原生不产生
task_plan.md、findings.md、progress.md 这类结构化规划文件。
这些文件完全由 planning-with-files Skill（Manus 模式）注入。该 Skill 的
核心原理：```plaintext
Context Window = RAM (volatile, limited)
Filesystem    = Disk (persistent, unlimited)
→ Anything important gets written to disk.
```
三文件模式：
文件
用途
更新时机
task_plan.md
Phase 拆分、进度、决策记录
每个 phase 结束
findings.md
研究发现、参考资料
每次发现后
progress.md
Session log、测试结果
持续更新
1.2 该 Skill 已成为事实标准
planning-with-files 已支持几乎所有主流 agent：
Agent
集成方式
Claude Code
原生 Skill + Plugin + Hooks (PreToolUse 等)
Codex
.codex/hooks.json 全生命周期 hooks
Gemini CLI
.gemini/settings.json hooks
Cursor
.cursor/hooks.json
Copilot CLI
.github/hooks/ scripts
Manus
官方集成 Agent Skills 规范
OpenCode
.opencode/plugins/ TypeScript plugin
Factory AI
.factory/settings.json
Kiro
Agent Skills spec
Anthropic 推出的 Agent Skills 开放规范正在被 Manus 等平台官方采纳，
planning-with-files 是该规范下安装量最大的 Skill 之一。
1.3 即使没有 Skill，文件模式也在收敛
社区中还有大量变体，文件名和结构不完全一致但模式趋同：
todo.md / tasks.md — checklist 式任务追踪
plan.md / session.md — phase 追踪 + session 状态
requirements.md — 详细需求描述
Claude Code 内置的 TodoWrite 工具（但不持久化到文件，context reset 后丢失）
结论：虽然没有强制标准，但社区已经收敛到「markdown + checklist + phase」
这一范式。解析器应该做宽容匹配而非绑定特定文件名。

Part 2: Entire 的方式与对比
2.1 Entire 概况
创始人：前 GitHub CEO Thomas Dohmke
融资：$60M seed（开发者工具史上最大种子轮），估值 $300M
团队：15 人，来自 GitHub + Atlassian
开源：CLI 部分 MIT 协议 (github.com/entireio/cli, 4k stars)
2.2 Entire 的工作方式
核心原语：Checkpoint
每次 git commit 时，Entire 自动将 agent session 的完整上下文
（transcript, prompts, file changes, token usage, tool calls）打包为
一个 checkpoint，通过 git trailer 关联到 commit SHA：```plaintext
feat: Add login form validation

Entire-Checkpoint: a3b2c4d5e6f7
Entire-Attribution: 73% agent (146/200 lines)
```
数据存储：```plaintext
entire/checkpoints/v1 (独立 git branch)
├── 0f/
│   └── f8ca6db1c9/              # checkpoint ID
│       ├── metadata.json         # checkpoint 元数据
│       └── 0/                    # session 文件夹
│           ├── full.jsonl        # 完整 transcript
│           ├── context.md        # prompt 的 markdown 版
│           ├── prompt.txt        # 原始 prompt
│           └── metadata.json     # token 统计、attribution
```
两层快照机制：
类型
存储位置
生命周期
推送到远程
Temporary (shadow)
entire/<sessionID>-<worktreeID>
工作中
❌
Committed
entire/checkpoints/v1
永久
✅
Agent Hook 集成方式：
Entire 不修改 LLM 推理过程，而是通过各 agent 自带的 hook config 注入：```plaintext
Claude Code  → .claude/settings.json
Codex        → .codex/hooks.json
Cursor       → .cursor/hooks.json
Gemini CLI   → .gemini/settings.json
Copilot CLI  → .github/hooks/entire.json
OpenCode     → .opencode/plugins/entire.ts
```
回溯能力：```bash
entire rewind          # 选择一个 checkpoint，回退代码到那个状态
entire resume <branch> # 恢复之前的 session 继续工作
entire explain         # 解释某个 commit 背后的推理过程
```
2.3 Entire 的三层愿景（已发布 vs 规划中）
层
状态
描述
Git-compatible DB
规划中
统一 code / intent / constraints / reasoning
Semantic Layer
早期
Context graph，多 agent 协调
Checkpoints CLI
已发布
捕获 agent session，关联到 git history
2.4 Entire 能否解决我们的问题？
需求
Entire 能力
差距
跟踪 plan/todo/progress 变化
部分
只在 commit 点快照，不追踪文件级别实时变化
跨 worktree 聚合
支持
worktree 独立 session，但无统一看板
历史变革追溯
强
append-only audit log 是核心强项
Worktree 消亡后可查
强
metadata 推送到远程后永久保存
映射到看板
❌
只有 session 浏览器，没有看板 UI
Plan 文件变更 diff
❌
只在 commit 点做快照，不解析 plan 结构
主动控制 agent（gate/verify）
❌
纯被动记录，不做决策
核心差异：
Entire 的粒度是 commit，thin-supervisor 的粒度是 checkpoint（agent 的每一步决策）
Entire 是被动记录者（观察 agent 输出并归档）
thin-supervisor 是主动控制者（状态机驱动 agent 行为、gate decision、验证）
两者正交互补：Entire 回答「这个 commit 背后 agent 说了什么」，
  我们要做的回答「现在所有 worktree 的任务进展到哪了」

Part 3: 被动跟踪方案设计
3.1 核心思路
在 Supervisor 未被用户唤起时，一个轻量守护进程持续 watch 文件系统，
解析 plan 文件变化，记录到结构化的 timeline 中。
三条实现路径：
路径
触发方式
粒度
侵入性
A. 文件系统 Watch
fswatch / watchdog
实时
零
B. Git Hook
post-commit
commit
低
C. Agent Hook
PreToolUse 等
工具调用
高
推荐：A + B 组合。路径 A 提供实时感知，路径 B 在 commit 点做结构化快照。
路径 C 侵入性太强（需要用户在每个 agent 里配 hook），作为可选增强。
3.2 宽容解析策略
不绑定特定文件名，而是模式匹配：```python
# 候选文件名（优先级从高到低）
PLAN_FILES = [
    "task_plan.md", "plan.md", "todo.md", "tasks.md",
    "requirements.md",
]
FINDINGS_FILES = ["findings.md", "notes.md", "research.md"]
PROGRESS_FILES = ["progress.md", "session.md", "log.md"]

# 结构化内容识别（即使文件名不在列表中）
PHASE_PATTERN   = r"^##\s+.*\[(complete|in_progress|pending|done|todo)\]"
CHECKLIST_PATTERN = r"^-\s+\[(x| )\]\s+"
```
解析产出的结构化数据：```json
{
  "worktree": "/path/to/feature-auth",
  "timestamp": "2026-04-20T14:30:00Z",
  "source_file": "task_plan.md",
  "phases": [
    {"name": "Research",       "status": "complete",    "tasks": {"done": 3, "total": 3}},
    {"name": "Implementation", "status": "in_progress", "tasks": {"done": 2, "total": 7}},
    {"name": "Testing",        "status": "pending",     "tasks": {"done": 0, "total": 4}}
  ],
  "overall_progress": 0.357,
  "commit_sha": "a3b2c4d"
}
```
3.3 数据模型```plaintext
~/.local/state/thin-observer/
├── config.yaml                    # 全局配置
├── known_projects.json            # 注册的项目列表
└── projects/
    └── <project-hash>/
        ├── timeline.jsonl         # append-only 事件流（所有 worktree）
        ├── worktrees/
        │   ├── <worktree-hash>/
        │   │   ├── current.json   # 最新 plan 状态快照
        │   │   └── snapshots/     # commit 级别的历史快照
        │   └── ...
        └── archive/               # 已消亡 worktree 的最终快照
```

Part 4: 展示层设计
4.1 数据的动态展示
4.1.1 展示形式
Plan / Todo / Progress 文件的内容本质是三种数据类型：
数据类型
来自文件
变化频率
适合的展示形式
任务清单
task_plan.md
中
Checklist + 进度条
研究笔记
findings.md
低
时间线 / 折叠卡片
执行日志
progress.md
高
滚动日志 / 状态指示
动态变化的展示需要区分两个时间尺度：
实时视图（Working View）：```plaintext
┌─────────────────────────────────────────────┐
│ feature-auth                    ● ACTIVE     │
│─────────────────────────────────────────────│
│ Phase 2/4: Implementation         [■■■□□□□] │
│                                              │
│  ✅ Create database schema                   │
│  ✅ Define API types                         │
│  🔄 Add API endpoints          ← agent 正在做 │
│  ☐  Write validation logic                   │
│  ☐  Error handling                           │
│  ☐  Add middleware                           │
│  ☐  Integration tests                       │
│                                              │
│ Findings: 3 items    Progress: 12 entries    │
│ Last activity: 2 min ago                     │
└─────────────────────────────────────────────┘
```
核心设计原则：
当前 phase 始终置顶，已完成的 phase 折叠为一行摘要
Checklist 实时反映 agent 的增删改：新增条目高亮，刚完成的条目短暂
  保留 ✅ 动画后折叠
文件级别的 diff 指示：当 task_plan.md 被 agent 修改时，变化的行
  以黄色边栏标注，几秒后淡出
历史视图（Timeline View）：```plaintext
Timeline: feature-auth
──────────────────────────────────────────
14:30  task_plan.md  Phase 2 started
14:32  findings.md   +2 items (API rate limit discovery)
14:35  task_plan.md  "Create database schema" → complete
14:41  progress.md   Test run: 12 pass, 0 fail
14:45  task_plan.md  "Define API types" → complete
14:52  [commit a3b2c4d] "Add auth schema and types"
14:55  task_plan.md  "Add API endpoints" → in_progress
 ...
```
4.1.2 已完成 vs 未完成的视觉区分
采用渐进式淡出而非简单的隐藏/显示：
状态
视觉处理
信息密度
当前进行中
完整展示，高亮边框，实时更新
100% — 全部细节
刚完成
✅ 标记，保留 30 秒后折叠为一行摘要
渐减到 20%
已完成
折叠为摘要行：Phase 1: Research ✓ (3/3)
10% — 仅标题
未开始
灰色文字，只显示 phase 名称
5% — 占位
这样做的好处是：用户的注意力自然聚焦到正在发生的事情上，但完整上下文
始终可以展开查看。
关键交互：点击折叠的已完成 phase 可以展开，展示该 phase 的完整
checklist 和当时的 findings 快照。这回答了「它当时是怎么完成的」这个问题。
4.2 多层级展示逻辑
展示层需要处理三个层级：Project → Worktree → File/Phase。
4.2.1 多 Worktree 并列展示
当同时开启多个 worktree 时，最核心的需求是一眼看到全局状态。
方案：自适应布局```plaintext
┌─ Project: lite-harness-supervisor ─────────────────────────────┐
│                                                                 │
│  ● feature-auth        ● fix-perf           ○ refactor-db      │
│    Phase 2/4             Phase 3/3 DONE        Phase 1/5        │
│    [■■■□□□□] 36%        [■■■■■■■] 100%        [■□□□□□□] 8%     │
│    🔄 3 min ago          ✅ 1 hour ago          💤 idle 20 min   │
│                                                                 │
├─────────────────────────────────────────────────────────────────┤
│  Expanded: feature-auth                                         │
│  ...（展开的详细视图如 4.1.1 所示）                               │
└─────────────────────────────────────────────────────────────────┘
```
设计原则：
顶部摘要栏：所有 worktree 平铺为卡片，一行概览。每张卡片
  只展示 phase 进度条 + 最后活跃时间。
点击展开：选中某个 worktree 后在下方展开详细视图。
状态指示器：
● 绿色 = agent 正在活跃写文件
● 蓝色 = 已完成
○ 灰色 = 空闲（超过 N 分钟无文件变化）
⚠ 黄色 = plan 文件存在但 agent 可能已中断（worktree 存在但无进程）
排序策略：默认按最后活跃时间排序。正在活跃的 worktree 始终排在前面，
已完成的排在后面，idle 的排在最后。
4.2.2 Project 级别的时间历史
从更高层级来看，一个 project 的历史是多个 worktree 生命周期的叠加。
方案：泳道时间线（Swimlane Timeline）```plaintext
Project Timeline: lite-harness-supervisor
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Apr 15        Apr 16        Apr 17        Apr 18        Apr 19        Apr 20
──────────────────────────────────────────────────────────────────────────────
feature-auth  ░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░████████████████▓▓▓▓▓▓▓▓▓▓
                                                   P1   P2→→→→→→→→→

fix-perf                    ░░░░░░░░████████████████████■■■■
                            P1  P2   P3  DONE ✓

refactor-db                                              ░░░░░░░░░░░░░░░░░░░
                                                         P1→→→→→

add-logging   ░░░░████■■                   (archived — completed Apr 16)

Legend: ░ planning  █ executing  ▓ active now  ■ completed
```
泳道时间线的价值：
看到 worktree 之间的时间重叠（是否有并行开发）
看到每个 worktree 的生命周期长度（是否有 worktree 拖了太久）
已消亡的 worktree 保留为归档泳道，不会丢失
每条泳道可以展开为该 worktree 内部的 phase 时间线
聚合指标（Project Dashboard 顶部）：```plaintext
┌──────────────────────────────────────────────────────┐
│  lite-harness-supervisor                              │
│  Active: 2 worktrees  │  Completed: 2  │  Total: 4   │
│  Overall progress: 48%                                │
│  Avg worktree lifespan: 2.3 days                      │
│  Most active today: feature-auth (45 plan updates)    │
└──────────────────────────────────────────────────────┘
```
4.3 交互方式
4.3.1 核心立场：默认只读，可选写入
被动跟踪系统的首要原则是不干预 agent 的工作。如果用户修改了 plan 文件
而 agent 正在基于旧版本工作，会导致状态冲突。
推荐的分层交互模型：
层级
交互类型
说明
L0: 纯观察
只读
浏览、搜索、展开/折叠、时间线导航
L1: 标注
旁路写入
给 task/phase 加标签、备注，不修改源文件
L2: 轻量操作
有保护写入
手动标记 task 完成/跳过，写回 plan 文件
L3: 调度
需要 Supervisor
暂停 agent、切换 phase、触发验证
大部分用户在大部分时间应该停留在 L0-L1。L2 需要明确的确认（因为
会修改 agent 可能正在读的文件）。L3 是 thin-supervisor 已有的能力。
4.3.2 各层级的具体交互
L0: 纯观察```plaintext
操作                          实现方式
──────────────────────────────────────────────
展开/折叠 phase               UI 交互（不涉及文件）
查看某个时间点的 plan 快照    从 timeline.jsonl 回放
搜索 findings 内容            全文索引 findings.md 历史
对比两个时间点的 plan diff    timeline 中的两个 snapshot diff
查看已消亡 worktree 的历史    从 archive/ 读取
```
L1: 标注（旁路写入）
用户可以给 task 或 phase 加标签和备注，但这些标注不写入 plan 文件本身，
而是写入 observer 自己的 sidecar 文件：```json
// ~/.local/state/thin-observer/projects/<hash>/annotations.jsonl
{
  "worktree": "feature-auth",
  "target": "Phase 2 / Add API endpoints",
  "type": "label",
  "value": "blocked-by-design-review",
  "author": "chris",
  "timestamp": "2026-04-20T15:00:00Z"
}
```
标注在 UI 上以 badge/tag 形式附着在对应的 task 旁边，但 agent 看不到
这些标注（不在 plan 文件里），所以不会影响 agent 行为。
如果需要让 agent 也看到：提供一个「推送到 plan」按钮，将标注
追加到 task_plan.md 的对应条目后面。这是 L2 操作，需要确认。
L2: 轻量操作（有保护写入）```plaintext
操作                         保护机制
──────────────────────────────────────────────
手动标记 task 完成           1. 检查 agent 是否正在活跃
                             2. 如果活跃，提示「agent 正在工作，
                                确定要修改吗？」
                             3. 写入前做 plan 文件的 backup snapshot
                             4. 原子写入（rename）

手动添加新 task              追加到 phase 的 checklist 末尾
                             （不修改已有内容，冲突风险低）

手动跳过某个 task            标记为 `- [~] skipped` 而非删除

重新排序 task                高风险，不建议在 agent 活跃时操作
```
L3: 调度（需要 Supervisor）
这就是 thin-supervisor 已有的能力。Observer 不做调度，但可以提供
一个「升级到 Supervisor」的入口：```plaintext
用户在 Observer UI 看到某个 worktree 的进度停滞
→ 点击「Diagnose」按钮
→ Observer 自动执行 `thin-supervisor attach <worktree>`
→ Supervisor 接管，进行 checkpoint 验证和决策
```
4.3.3 终端(TUI) vs Web vs 编辑器
模式
适合场景
交互层级
TUI
开发者日常使用，命令行
L0 + L1
Web
团队共享、回顾、项目管理
L0 + L1 + L2
编辑器插件
开发时不离开 IDE
L0（侧边栏）
TUI 是最适合的第一个实现目标。原因：
thin-supervisor 已经有 TUI 基础设施（operator dashboard）
开发者的工作流在终端中，不需要切换窗口
可以复用 collect_sessions() 的跨 worktree 发现机制
TUI 的极简形态：```bash
$ thin-observer status

lite-harness-supervisor
├── ● feature-auth    Phase 2/4  [■■■□□□□] 36%   🔄 3 min ago
├── ● fix-perf        Phase 3/3  [■■■■■■■] DONE  ✅ 1 hour ago
├── ○ refactor-db     Phase 1/5  [■□□□□□□]  8%   💤 20 min
└── (archived) add-logging       DONE ✓           Apr 16

$ thin-observer timeline feature-auth --last 1h

14:30  task_plan.md   Phase 2 started
14:35  task_plan.md   "Create database schema" → ✅
14:41  progress.md    Test run: 12 pass, 0 fail
14:45  task_plan.md   "Define API types" → ✅
14:52  [commit a3b2c4d]
14:55  task_plan.md   "Add API endpoints" → 🔄
```

Part 5: 与现有生态的关系
5.1 定位图```plaintext
                     被动记录 ←───────→ 主动控制

  Commit 级别     │   Entire          │
                  │   (session 归档)  │
                  │                   │
  Plan 文件级别   │   thin-observer   │   thin-supervisor
                  │   (plan 跟踪)    │   (执行控制)
                  │                   │
  Agent 内部级别  │                   │   (checkpoint 协议)
```
Entire：commit 级别的被动记录。回答「这个 commit 背后 agent 说了什么」
thin-observer（新项目）：plan 文件级别的被动跟踪。回答「所有 worktree 的任务进展到哪了」
thin-supervisor：agent 级别的主动控制。回答「下一步该做什么，要不要暂停」
5.2 数据流```plaintext
Agent 工作
  │
  ├─→ 写 task_plan.md / findings.md / progress.md
  │     │
  │     └─→ thin-observer (fswatch) ──→ timeline.jsonl ──→ TUI / Web
  │
  ├─→ git commit
  │     │
  │     ├─→ thin-observer (post-commit hook) ──→ snapshot
  │     └─→ Entire (post-commit hook) ──→ entire/checkpoints/v1
  │
  └─→ stdout (checkpoint 协议)
        │
        └─→ thin-supervisor (tmux sidecar) ──→ gate decision
```
5.3 是否要起新项目
建议：新项目。
理由：
关注点分离：Observer 是被动的、零侵入的、永远在线的；
   Supervisor 是主动的、需要唤起的、有控制权的。混在一起会污染两边的设计。
独立价值：Observer 即使没有 Supervisor 也有用。用户只想看进度，
   不需要完整的 Clarify → Plan → Execute 流程。
复用基础设施：可以复用 thin-supervisor 的 global_registry（worktree 发现）
   和 session_index（跨 worktree 聚合），但作为 library 依赖而非代码耦合。
部署独立：Observer 作为 daemon 始终运行，Supervisor 按需启动。
   不同的生命周期管理模型。
项目名建议：thin-observer 或 plan-watcher。