> ⚠ **ARCHIVED — NOT IMPLEMENTED.** This document describes the rejected Work Graph Protocol proposal. See [`../REASONS-ARCHIVED.md`](../REASONS-ARCHIVED.md) for why thin-observer took a different approach.

# Append-mode Integration Guide

## 1. 原则

如果用户已经有：
- `AGENTS.md`
- `CLAUDE.md`
- Cursor Rules

则必须：

- 追加，不替换
- 保留原有内容
- 不擅自改写用户既有规则
- 用固定哨兵块管理新增内容

---

## 2. 推荐哨兵块

```md
<!-- BEGIN WORK-GRAPH PROTOCOL -->
...本协议追加内容...
<!-- END WORK-GRAPH PROTOCOL -->
```

后续更新时：
- 只替换哨兵块内内容
- 不碰外部内容

---

## 3. 对 `AGENTS.md` 的处理

### 3.1 如果不存在
新建一个最小版，并写入协议区块。

### 3.2 如果存在
不要整体替换。
按以下顺序：

1. 读取原文件
2. 搜索 `BEGIN WORK-GRAPH PROTOCOL`
3. 如果存在：
   - 只更新该区块
4. 如果不存在：
   - 追加到文件末尾
   - 文件末尾前最好保留一个空行再插入

### 3.3 如果原文件中已有相似规则
不要删除旧规则。
做法：
- 保留旧规则
- 在协议区块中注明“若冲突，以原仓库已有明确规则为准”

---

## 4. 对 `CLAUDE.md` 的处理

### 4.1 如果不存在
创建最小版，优先只放：
- `@AGENTS.md`
- Claude-specific 的补充规则

### 4.2 如果存在
同样只追加哨兵块，不覆盖整文件。

### 4.3 如果原文件已经写了自己的复杂工作流
保留原逻辑。
只补充：
- 计划漂移处理
- `[T-xx]` 保留
- sync-plan
- blocker / verify / done 规则

---

## 5. 对 Cursor Rules 的处理

推荐新增一条独立规则文件，而不是合并进已有大文件。

建议路径：

- `.cursor/rules/work-graph-protocol.mdc`

优点：
- 风险最低
- 易于启用 / 禁用
- 不污染其他规则
- 后续升级简单

---

## 6. 对 skill 的处理

### 6.1 Codex
推荐：
- `.agents/skills/`

### 6.2 Claude Code
推荐优先：
- `.claude/skills/`

团队共享时再升格为 plugin。

### 6.3 Cursor
优先：
- `.cursor/skills/`

如果你想保留跨工具共用仓库结构，可：
- 在 `.agents/skills/` 保留主版本
- 在 `.cursor/skills/` 做镜像或复制
- 但当前实现建议先保证 Cursor 自身能稳定发现

---

## 7. 建议的追加顺序

### Step 1
追加 `AGENTS.md` 协议区块

### Step 2
追加 `CLAUDE.md` 协议区块（如果使用 Claude）

### Step 3
新增 Cursor Rule（如果使用 Cursor）

### Step 4
放置 skill 目录

### Step 5
放置 `.workgraph/` skeleton

### Step 6
实现 `planctl` 最小命令

---

## 8. coding agent 的插入算法建议

### 对于 `AGENTS.md`
伪代码：

```python
if not exists("AGENTS.md"):
    create_with_snippet()
else:
    text = read("AGENTS.md")
    if "BEGIN WORK-GRAPH PROTOCOL" in text:
        replace_block()
    else:
        append_block_to_end()
```

### 对于 `CLAUDE.md`
同样逻辑。

### 对于 Cursor
如果不存在规则目录则创建目录，再写单独规则文件。

---

## 9. 冲突处理建议

### 高优先级保留
已有 repo 明确规则 > 新协议区块

示例：
- 原仓库明确要求不用 TDD
- 新 skill 倾向 TDD

处理方式：
- 不删除原规则
- 协议区块里写：
  - “当本协议与仓库既有明确规则冲突时，以仓库既有规则为准”

---

## 10. 推荐目录变更最小集合

```text
AGENTS.md                     # append block only
CLAUDE.md                     # append block only, if used
.cursor/rules/work-graph-protocol.mdc   # new file
.agents/skills/...            # Codex / shared
.claude/skills/...            # Claude standalone
.cursor/skills/...            # Cursor
.workgraph/...                # protocol state
```
