---
description: Shows which other coding-agent sessions are active on this machine and which files each one holds. Use when an edit was denied by agent-quorum, or before starting work that touches many files.
---

Run this command and report the result briefly:

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/agent-quorum.sh" status
```

If a file you need is held by another session, do not edit it and do not work
around the claim with shell commands. Pick other work, or ask the user how to proceed.
