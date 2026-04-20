<!-- BEGIN WORK-GRAPH PROTOCOL -->
@AGENTS.md

## Work Graph Protocol — Claude-specific notes

- For multi-step work, use the Work Graph Protocol before touching code.
- Prefer the `using-work-graph` and `writing-work-graph-plan` skills when planning.
- Prefer the `executing-work-graph-plan` skill when implementing an existing plan.
- If task continuity is unclear, use `reconciling-work-graph` instead of guessing.
- After editing `.workgraph/plans/*.md`, immediately run `planctl sync-plan <plan-file>`.
- Do not mark tasks done without validation evidence.
<!-- END WORK-GRAPH PROTOCOL -->
