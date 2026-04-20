# Skills Included

本包建议拆成 4 个 skill，而不是一个大 skill。

## Included
- `using-work-graph`
- `writing-work-graph-plan`
- `executing-work-graph-plan`
- `reconciling-work-graph`

## Why split
- 提升自动匹配稳定性
- 降低误触发
- 更容易做显式调用
- 更接近 Superpowers 的可复用方法

## Where to place

### Codex
- `.agents/skills/<skill-name>/SKILL.md`

### Claude Code
- `.claude/skills/<skill-name>/SKILL.md`
- 稳定后再打包成 plugin

### Cursor
- `.cursor/skills/<skill-name>/SKILL.md`
- 同时配合 `.cursor/rules/work-graph-protocol.mdc`
