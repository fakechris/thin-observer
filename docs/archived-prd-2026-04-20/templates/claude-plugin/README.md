> ⚠ **ARCHIVED — NOT IMPLEMENTED.** This document describes the rejected Work Graph Protocol proposal. See [`../../REASONS-ARCHIVED.md`](../../REASONS-ARCHIVED.md) for why thin-observer took a different approach.

# Claude Plugin Skeleton

这是一个最小 Claude Code plugin skeleton，用于团队级共享 Work Graph Protocol。

## 结构

```text
.claude-plugin/plugin.json
skills/
hooks/
```

## 适合何时使用
- 你已经在单 repo 的 `.claude/skills/` 下验证过 workflow
- 你希望团队多个项目复用
- 你希望把 skills + hooks 作为一个版本化单元分发

## 何时不要先上 plugin
如果你还在快速迭代 skill 内容，建议先在：
- `.claude/skills/`
- `CLAUDE.md`
里试验，稳定后再打包成 plugin。

## 注意
hooks.json 中的命令只是一版示意模板。真正实现时，请根据你们的 Claude 事件字段与本地环境变量做校准。
