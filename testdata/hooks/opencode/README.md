These fixtures are bridge events: the JSON plugin/opencode/index.js writes to
`agent-quorum hook --agent opencode`. The bridge schema is owned by
internal/adapter/opencode, so these are not raw OpenCode payloads. The golden
test in internal/adapter/opencode/opencode_test.go enforces bidirectional
coverage against every fixture here.
