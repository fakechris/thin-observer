---
name: writing-work-graph-plan
description: Use when you need to create or substantially revise a tracked implementation plan for a multi-step task, before touching code. Use this when the work needs stable task refs, phases, lifecycle tracking, or board visibility.
---

# Writing a Work Graph Plan

## Process

1. Confirm or create the active objective / project / plan.
2. If no plan exists, initialize one with `planctl init-plan`.
3. Break the work into phases and bite-sized tasks.
4. Create each task via `planctl add-task` before writing it into the markdown plan.
5. Write the plan to `.workgraph/plans/<plan_id>.md`.
6. Use `[T-xx]` refs exactly as returned by the tool.
7. After editing the plan file, run `planctl sync-plan <plan-file>`.

## Plan requirements

- Must include frontmatter:
  - `protocol`
  - `objective_id`
  - `project_id`
  - `plan_id`
  - `plan_rev`
  - `status`
- Each actionable task line must include `[T-xx]`
- Prefer short, testable, unambiguous tasks

## Do not

- Do not handwrite new `[T-xx]` refs
- Do not pack unrelated subsystems into one giant plan if they can be split cleanly
- Do not assume the board can infer identity later if you skip refs now
