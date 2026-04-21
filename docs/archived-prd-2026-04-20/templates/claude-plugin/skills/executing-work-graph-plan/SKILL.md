> ⚠ **ARCHIVED — NOT IMPLEMENTED.** This document describes the rejected Work Graph Protocol proposal. See [`../../../../REASONS-ARCHIVED.md`](../../../../REASONS-ARCHIVED.md) for why thin-observer took a different approach.

---
name: executing-work-graph-plan
description: Use when you already have a written Work Graph plan and need to execute it while preserving task continuity, blockers, validation, and board alignment.
---

# Executing a Work Graph Plan

## Process

1. Read the active plan from `.workgraph/plans/`.
2. Review it critically before changing code.
3. Attach or create a run for the current task with `planctl attach-run`.
4. Mark task status through the protocol rather than freehand notes.
5. If a task title changes but the identity is the same, update the task via `planctl update-task`.
6. If one task becomes several, use `planctl split-task`.
7. If several tasks collapse into one, use `planctl merge-tasks`.
8. If you need human input, use `planctl block`.
9. After finishing implementation, attach validation with `planctl verify`.
10. Only then call `planctl done`.

## Hard rules

- Never silently drop a task from the plan
- Never mark a task done without validation evidence
- If code state has drifted ahead of the plan, update the plan first, then sync it

## If continuity becomes unclear

Stop guessing and switch to `reconciling-work-graph`.
