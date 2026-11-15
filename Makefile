VERSION ?= $(shell jq -r .version plugin/.claude-plugin/plugin.json)-dev
BIN     := bin/agent-quorum

.PHONY: build test vet lint check validate-plugin snapshot dev release clean

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
	@jq empty .claude-plugin/marketplace.json plugin/.claude-plugin/plugin.json plugin/.codex-plugin/plugin.json plugin/hooks/hooks.json plugin/hooks/codex.json
	@jq -e -s '.[0].version == .[1].version' plugin/.claude-plugin/plugin.json plugin/.codex-plugin/plugin.json >/dev/null

validate-plugin:  ## needs the claude and codex CLIs
	claude plugin validate ./plugin --strict
	claude plugin validate .
	scripts/validate-codex-plugin.sh

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
