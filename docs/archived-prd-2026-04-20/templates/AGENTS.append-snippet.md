<!-- BEGIN WORK-GRAPH PROTOCOL -->
## Work Graph Protocol

For any multi-step task, feature, refactor, investigation, or tracked fix, use the Work Graph Protocol.

### Required
- Preserve existing task refs like `[T-07]` exactly.
- Do not invent, rename, or recycle task refs manually.
- Before adding a new task, call `planctl add-task`.
- Before splitting or merging tasks, call `planctl split-task` / `planctl merge-tasks`.
- Do not silently delete tasks from plans.
- After editing `.workgraph/plans/*.md`, immediately run `planctl sync-plan <plan-file>`.
- If human input is needed, record it with `planctl block`.
- Before marking work done, attach validation evidence with `planctl verify`, then call `planctl done`.

### Plan format
- Plans live in `.workgraph/plans/`
- Each plan must include `protocol`, `objective_id`, `project_id`, `plan_id`, `plan_rev`, and `status` in frontmatter
- Every actionable task line must include a `[T-xx]` ref

### Conflict rule
If this protocol conflicts with existing repo-specific instructions, keep the existing repo-specific instructions and adapt the Work Graph Protocol to them instead of replacing them.
<!-- END WORK-GRAPH PROTOCOL -->
