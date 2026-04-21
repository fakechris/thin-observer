> ⚠ **ARCHIVED — NOT IMPLEMENTED.** This document describes the rejected Work Graph Protocol proposal. See [`../REASONS-ARCHIVED.md`](../REASONS-ARCHIVED.md) for why thin-observer took a different approach.

# planctl / MCP Contract

## 1. 设计原则

- 任务身份由工具发号
- markdown 允许自由写标题，但不能改 task ref
- 所有高风险变化必须显式命令化
- board 写回也走同一命令总线

---

## 2. CLI 命令

## 2.1 初始化

```bash
planctl init-objective --title "Self-hosted Linear-compatible PM backend"
planctl init-project --objective OBJ-... --title "API Compatibility MVP"
planctl init-plan --project PRJ-... --title "API Compatibility MVP"
```

### 输出建议
```json
{
  "objective_id": "OBJ-20260420-01"
}
```

```json
{
  "project_id": "PRJ-20260420-01"
}
```

```json
{
  "plan_id": "PLN-20260420-01",
  "plan_file": ".workgraph/plans/PLN-20260420-01.md"
}
```

---

## 2.2 任务操作

### add-task
```bash
planctl add-task --plan PLN-... --title "Add authenticated API endpoints"
```

返回：
```json
{
  "task_uid": "tsk_01K0...",
  "task_ref": "T-07"
}
```

### update-task
```bash
planctl update-task --task tsk_01K0... --title "Add authenticated API endpoints"
```

### split-task
```bash
planctl split-task --task tsk_01K0... \
  --child "Create mutation" \
  --child "Add auth middleware"
```

### merge-tasks
```bash
planctl merge-tasks --task tsk_a --task tsk_b --title "Implement auth-compatible endpoints"
```

### supersede-task
```bash
planctl supersede-task --old tsk_old --new tsk_new
```

### drop-task
```bash
planctl drop-task --task tsk_... --reason "Out of scope"
```

---

## 2.3 运行与验证

### attach-run
```bash
planctl attach-run --task tsk_... --agent claude-code --worktree wt/api-compat-3
```

### block
```bash
planctl block --task tsk_... --kind needs_human --title "Decide auth header compatibility rule"
```

### verify
```bash
planctl verify --task tsk_... --artifact reports/api-compat-test.json
```

### done
```bash
planctl done --task tsk_...
```

### reopen
```bash
planctl reopen --task tsk_...
```

---

## 2.4 同步与投影

### sync-plan
```bash
planctl sync-plan .workgraph/plans/PLN-20260420-01.md
```

### render-board
```bash
planctl render-board
```

### export-view
```bash
planctl export-view --view running
```

---

## 3. MCP 方法建议

- `workgraph.init_objective`
- `workgraph.init_project`
- `workgraph.init_plan`
- `workgraph.add_task`
- `workgraph.update_task`
- `workgraph.split_task`
- `workgraph.merge_tasks`
- `workgraph.supersede_task`
- `workgraph.drop_task`
- `workgraph.attach_run`
- `workgraph.block_task`
- `workgraph.verify_task`
- `workgraph.done_task`
- `workgraph.reopen_task`
- `workgraph.sync_plan`
- `workgraph.render_board`

---

## 4. sync-plan 算法

### 输入
- plan markdown
- current registry
- previous plan revision

### 强规则
1. 带 `[T-xx]` 的 task 行直接映射
2. 同一 `task_ref` 指向同一 `task_uid`
3. 若标题变了，更新 `title_current`，旧标题进 `aliases`

### 启发式
- 新行无 `task_ref`：
  - 尝试相似度匹配
  - 若高相似度且无冲突，建议绑定
  - 否则进入 reconcile queue
- 一旧多新：
  - 提示 split candidate
- 多旧一新：
  - 提示 merge candidate

### 缺失规则
- 本 rev 消失：
  - 先 `missing_in_rev = true`
- 连续 2 rev 消失：
  - 标记为 `dropped_candidate`
- 有 active run 的 task：
  - 不自动 drop

---

## 5. 错误类型

### 5.1 `TASK_REF_MISSING`
plan 中存在可执行 task，但未带 `[T-xx]`

### 5.2 `TASK_REF_CONFLICT`
同一 plan 中重复使用 `[T-xx]`

### 5.3 `TASK_UID_NOT_FOUND`
CLI 请求引用不存在的 task_uid

### 5.4 `LINEAGE_REQUIRED`
系统检测到明显 split / merge / supersede 情况，但未显式声明

### 5.5 `VERIFY_REQUIRED`
试图 `done`，但无验证证据

### 5.6 `RECONCILIATION_REQUIRED`
无法可靠判断 continuity，需要人工确认

---

## 6. 建议的返回模式

所有命令返回统一 envelope：

```json
{
  "ok": true,
  "command": "add-task",
  "data": {...},
  "warnings": [],
  "errors": []
}
```

失败时：

```json
{
  "ok": false,
  "command": "sync-plan",
  "data": null,
  "warnings": [],
  "errors": [
    {
      "code": "TASK_REF_MISSING",
      "message": "Actionable task line missing [T-xx] ref",
      "location": {
        "file": ".workgraph/plans/PLN-....md",
        "line": 17
      }
    }
  ]
}
```

---

## 7. board 写回必须如何调用

UI 不可直接改 local state。
必须：

```text
UI Action
→ planctl / MCP command
→ registry / events update
→ re-project board
```

例如：

### 拖到 Ready
```bash
planctl set-status --task tsk_... --status ready
```

### 改标题
```bash
planctl update-task --task tsk_... --title "New Title"
```

### split
```bash
planctl split-task --task tsk_... --child "..." --child "..."
```

### done
```bash
planctl verify --task tsk_... --artifact ...
planctl done --task tsk_...
```
