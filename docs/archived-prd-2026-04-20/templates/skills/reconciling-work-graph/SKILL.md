> ⚠ **ARCHIVED — NOT IMPLEMENTED.** This document describes the rejected Work Graph Protocol proposal. See [`../../../REASONS-ARCHIVED.md`](../../../REASONS-ARCHIVED.md) for why thin-observer took a different approach.

---
name: reconciling-work-graph
description: Use when a plan, task list, registry, or board appears out of sync; when tasks may have been renamed, split, merged, superseded, or dropped; or when markdown drift makes continuity uncertain.
---

# Reconciling Work Graph State

## Goal

Resolve task continuity explicitly instead of letting the board or sync layer guess wrong.

## Process

1. Compare the current plan markdown with the registry / sidecar state.
2. Check whether missing tasks are:
   - renamed
   - split
   - merged
   - superseded
   - actually dropped
3. Use the correct protocol command:
   - `planctl update-task`
   - `planctl split-task`
   - `planctl merge-tasks`
   - `planctl supersede-task`
   - `planctl drop-task`
4. Re-run `planctl sync-plan` after edits.
5. If confidence remains low, create a blocker or reconciliation note instead of guessing.

## Hard rules

- Do not silently delete tasks to make the plan “look clean”
- Do not create duplicate tasks just because the wording changed slightly
- Do not assume text similarity alone is enough for irreversible decisions
