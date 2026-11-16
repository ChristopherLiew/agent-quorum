VERSION ?= $(shell jq -r .version plugin/.claude-plugin/plugin.json)-dev
BIN     := bin/agent-quorum

.PHONY: build test vet lint check validate-plugin validate-antigravity-plugin snapshot dev release clean

build:            ## build ./bin/agent-quorum for this machine
	go build -trimpath -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/agent-quorum

test:             ## unit + integration tests with the race detector
	go test -race -count=1 ./...

vet:
	go vet ./...
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }

lint:             ## shell + JSON checks (shellcheck is optional locally, required in CI)
	@for f in plugin/scripts/*.sh scripts/*.sh; do sh -n "$$f" || exit 1; done
	@command -v shellcheck >/dev/null && shellcheck plugin/scripts/*.sh scripts/*.sh || echo "shellcheck not installed, skipped"
	@if command -v bun >/dev/null; then bun build --no-bundle plugin/opencode/index.ts >/dev/null; else echo "bun not installed, OpenCode plugin syntax check skipped"; fi
	@jq empty .claude-plugin/marketplace.json plugin/.claude-plugin/plugin.json plugin/.codex-plugin/plugin.json plugin/.antigravity-plugin/plugin.json plugin/hooks/hooks.json plugin/hooks/codex.json plugin/hooks.json
	@jq -e -s '.[0].version == .[1].version and .[0].version == .[2].version' plugin/.claude-plugin/plugin.json plugin/.codex-plugin/plugin.json plugin/.antigravity-plugin/plugin.json >/dev/null

validate-plugin:  ## needs the claude and codex CLIs
	claude plugin validate ./plugin --strict
	claude plugin test ./plugin
	claude plugin validate .
	scripts/validate-codex-plugin.sh

validate-antigravity-plugin:  ## needs the Antigravity CLI (`agy`)
	@command -v agy >/dev/null 2>&1 || { echo "agy CLI not found" >&2; exit 1; }
	# Non-strict validation allows the expected warning for Claude's separate hooks/hooks.json.
	agy plugin validate ./plugin

check: vet lint test   ## what CI runs

snapshot:         ## cross-compile all release archives into ./dist
	scripts/release-build.sh $(VERSION)

dev: build        ## start Claude Code with the local plugin and the local binary
	AGENT_QUORUM_BIN=$(CURDIR)/$(BIN) claude --plugin-dir ./plugin

release:          ## make release V=1.2.3 (or 1.2.3-rc.1): opens the release pull request
	@test -n "$(V)" || { echo "usage: make release V=1.2.3"; exit 1; }
	$(MAKE) check
	scripts/bump-version.sh $(V)

clean:
	rm -rf bin dist
