# Why Superpowers Triggers So Reliably

## 1. 先说结论

Superpowers 看起来“很稳”，不是因为它有某个神秘 keyword trick，而是因为它把“触发”设计成了一个完整的系统：

1. 很强的 bootstrap 层
2. 很窄的 skill 拆分
3. 非常激进、非常明确的 `Use when...` 描述
4. first-class plugin / slash / skill tool integration
5. 显式 fallback
6. workflow chaining

但它依旧不是纯 deterministic。
自动技能触发本质上仍是模型驱动的，只是它把触发成功率推得很高。

---

## 2. 它公开暴露出来的关键做法

### 2.1 一个非常强势的元技能
`using-superpowers` 不是普通功能 skill，而更像“进入方法论”的入口。

它的公开描述大意是：
- starting any conversation
- before any response including clarifying questions
- if there is even a 1% chance a skill applies, you must invoke it

这说明它不是等用户明确说“请用 skill”，而是先把“检查技能”变成默认行为。

### 2.2 root instructions + skill 组合
Superpowers README 明确写到：
- 它不仅有 skills
- 还有 initial instructions，确保 agent 使用这些 skills
- “The agent checks for relevant skills before any task”
- “Mandatory workflows, not suggestions”

这就是为什么它不像普通“把 skill 放在目录里，等模型偶然看到”。

### 2.3 技能拆分非常窄
它不是一个胖技能，而是：
- brainstorming
- writing-plans
- executing-plans
- requesting-code-review
- finishing-a-development-branch
- ...

每个技能只命中一个非常清晰的用户意图。

### 2.4 description 非常贴近“用户会怎么说”
例如：
- Use when you have a spec or requirements for a multi-step task, before touching code
- Use when you have a written implementation plan to execute in a separate session with review checkpoints

这类 description 正好符合 Agent Skills 对 description 的最佳实践：
- imperative
- user-intent-oriented
- concise but specific

### 2.5 不是只靠自动触发
在 Claude plugin 场景下：
- 技能是 first-class plugin component
- 自动发现
- 自动上下文触发
- 同时也有 slash-command 路径做显式调用

也就是说，自动只是首选，显式不是缺席。

### 2.6 workflow chaining
`using-superpowers` → `writing-plans` → `executing-plans` → `finishing-a-development-branch`

这类链式设计让 agent 不需要每一步都重新猜“下一步该用哪个 skill”，而是 skill 自己带出下一步。

---

## 3. 它“稳”的根本原因

### 3.1 触发问题被前置成“入口问题”
大多数 skill 作者只写：
- 一个 description
- 一段 body

然后等模型“自己触发”。

Superpowers 不一样，它先确保：
- session 一开始就进入“先查技能”的思维模式

### 3.2 它把 skill 变成 methodology，而不是知识片段
普通 skill 往往像：
- 一个小技巧
- 一个 reference
- 一个 prompt 片段

Superpowers 更像：
- 一套工作方法
- 一套顺序
- 一套必须走的检查点

### 3.3 它尽量减少模糊边界
skill 越胖、越泛、越像“万能工作流”，越容易触发不稳。
Superpowers 通过：
- 明确边界
- 任务分段
- 明确阶段性输出
使匹配更清晰。

---

## 4. 它不是 100% 确定性的证据

自动触发依旧不是绝对保证，理由有三：

### 4.1 Agent Skills 生态本来就是模型驱动
公开 Agent Skills 文档明确建议：
- 由模型根据 skill `description` 决定何时加载 skill
- 大多数实现不做 harness-side keyword router

### 4.2 tool-use reliability varies
公开 docs 直接承认：
- 不同模型对 skill 的遵循和 tool 调用稳定性不一样
- 有的模型会试图自己回答，而不是正确按 skill 执行

### 4.3 显式 slash / mention 仍然存在
如果自动触发已经 100% 可靠，就没必要保留这类显式手段。

---

## 5. 我们能不能做得像它一样稳

能，但要接受一个现实：

### 目标应该是
- 自动触发尽量稳定
- 关键路径有显式兜底
- 真正一致性靠工具层

### 不应该追求
- 只凭 description 实现 100% 自动强制

---

## 6. 我们该怎么学它

## 6.1 先做一个元技能 / 元规则
建议：
- `using-work-graph`

作用不是直接改 plan，而是：
- 告诉 agent 多步骤任务必须进入协议
- 要先检查 skill
- 要先用 `planctl`

## 6.2 skill 要拆窄
建议：
- `using-work-graph`
- `writing-work-graph-plan`
- `executing-work-graph-plan`
- `reconciling-work-graph`

## 6.3 description 要更 aggressive
普通写法：
- This skill handles plan IDs and task tracking

更好的写法：
- Use when starting or updating any multi-step engineering task that must preserve stable task refs, stay aligned with the board, or avoid task drift

## 6.4 root instructions 要明确
在：
- `AGENTS.md`
- `CLAUDE.md`
- Cursor Rule

里明确：
- complex work must enter Work Graph Protocol
- do not invent task refs
- sync plan after edits

## 6.5 工具 authority 不能缺
即使 skill 触发了，也不能只靠文字。
必须让：
- add-task
- split-task
- merge-task
- sync-plan
- verify
- done

都走 `planctl` / MCP。

## 6.6 要有显式 fallback
例如：
- “first use using-work-graph”
- slash command
- direct skill mention
- “before touching code, run writing-work-graph-plan”

---

## 7. 最后的工程判断

如果你想要“像 Superpowers 那样稳”，真正的配方不是：

> 写一个很长很厉害的 skill

而是：

> bootstrap + narrow skills + explicit descriptions + authority tool + explicit fallback + event-driven sync

这也是为什么这个包不只给了一个 skill，而给了：
- 规则层
- 4 个 skill
- 协议层
- planctl / MCP 设计
- board 投影
