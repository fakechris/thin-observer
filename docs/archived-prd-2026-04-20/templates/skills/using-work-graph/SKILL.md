> ⚠ **ARCHIVED — NOT IMPLEMENTED.** This document describes the rejected Work Graph Protocol proposal. See [`../../../REASONS-ARCHIVED.md`](../../../REASONS-ARCHIVED.md) for why thin-observer took a different approach.

---
name: using-work-graph
description: Use when starting or reframing any multi-step engineering task that will need a tracked plan, stable task refs, blocker handling, validation, or board visibility. Also use before asking clarifying questions if there is a real chance the task will become a tracked workflow.
---

# Using Work Graph

## Goal

Enter the Work Graph Protocol before the task drifts into ad hoc planning.

## Required behavior

1. If the task is multi-step, do not jump straight into code.
2. Check whether an active `.workgraph/plans/*.md` plan already exists for this project.
3. If no active plan exists, initialize one before creating freehand checklist items.
4. If the user already has repo-specific workflow rules, keep them. Do not replace them.
5. If you think a relevant workflow skill may apply, load it now instead of guessing.

## Next step

- If a new plan is needed, use `writing-work-graph-plan`
- If a plan already exists and you are about to execute it, use `executing-work-graph-plan`
- If the current plan and registry seem out of sync, use `reconciling-work-graph`

## Hard rules

- Do not invent task refs manually.
- Do not silently delete or replace tasks.
- Do not treat the board as source of truth.
- Use `planctl` / MCP for identity, lifecycle, blockers, verification, and done transitions.
