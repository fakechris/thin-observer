# Engineering TODO Checklist

## A. Protocol & Storage
- [ ] 设计 registry schema
- [ ] 设计 events schema
- [ ] task_uid 生成器
- [ ] task_ref 分配器
- [ ] plan_rev 管理器
- [ ] sidecar persistence
- [ ] migration helpers

## B. Plan Parser
- [ ] frontmatter parser
- [ ] phase parser
- [ ] checklist parser
- [ ] `[T-xx]` extractor
- [ ] status inference
- [ ] markdown serializer

## C. Reconciliation
- [ ] exact ref match
- [ ] rename detection
- [ ] split candidate detection
- [ ] merge candidate detection
- [ ] missing-in-rev handling
- [ ] low-confidence queue
- [ ] human-confirm flow

## D. planctl
- [ ] CLI arg design
- [ ] JSON output envelope
- [ ] error codes
- [ ] unit tests
- [ ] dry-run mode
- [ ] render-board command
- [ ] export-view command

## E. MCP
- [ ] tool names
- [ ] input / output schema
- [ ] permission model
- [ ] server startup mode
- [ ] agent integration examples

## F. Board Backend
- [ ] projection engine
- [ ] status mapping
- [ ] card view model
- [ ] active run computation
- [ ] timeline feed
- [ ] archive / history projection

## G. Board Frontend
- [ ] board columns
- [ ] detail panel
- [ ] runs tab
- [ ] artifacts tab
- [ ] graph tab
- [ ] drag-and-drop
- [ ] write-back command bus

## H. Agent Integrations
- [ ] AGENTS append helper
- [ ] CLAUDE append helper
- [ ] Cursor rule drop-in
- [ ] Codex skill placement
- [ ] Claude `.claude/skills` placement
- [ ] Cursor `.cursor/skills` placement
- [ ] plugin packaging for Claude

## I. Quality & Safety
- [ ] prevent silent delete
- [ ] prevent task_ref reuse
- [ ] done requires verify
- [ ] active run prevents auto-drop
- [ ] append-mode integration tests
- [ ] board write-back tests

## J. Nice-to-have
- [ ] external tracker mapping
- [ ] cycle support
- [ ] dependency graph view
- [ ] supervisor attach action
- [ ] analytics / metrics
- [ ] plugin marketplace packaging
