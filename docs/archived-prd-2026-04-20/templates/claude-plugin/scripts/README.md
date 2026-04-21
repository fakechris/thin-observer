> ⚠ **ARCHIVED — NOT IMPLEMENTED.** This document describes the rejected Work Graph Protocol proposal. See [`../../../REASONS-ARCHIVED.md`](../../../REASONS-ARCHIVED.md) for why thin-observer took a different approach.

# Optional helper scripts

`planctl maybe-sync` is a suggested wrapper command, not a standard shell builtin.

Recommended behavior:
- detect whether a `.workgraph/plans/*.md` file changed in this turn
- if yes, run `planctl sync-plan <changed-plan>`
- otherwise exit 0

Reason:
Claude hooks can reliably trigger after write/edit events, but the exact changed-file plumbing may vary by host and by how you implement your hook command. A tiny wrapper script keeps hook config simple and pushes file-detection logic into your own code.
