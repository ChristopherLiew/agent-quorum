These fixtures follow the shapes in the Claude Code hooks reference (read 2026-11-15).
They were not captured from a live session. Replace them in milestone M0:

    AGENT_QUORUM_CAPTURE=/tmp/cap.jsonl make dev
    jq -c .payload /tmp/cap.jsonl
