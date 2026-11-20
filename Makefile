# concord-cli build targets.

VERSION ?= dev
OUT     ?= bin
LDFLAGS := -s -w -X main.version=$(VERSION)

# For `make e2e`: a checkout of github.com/ZahakJ/concord to build the
# headless concord from, or a ready binary.
CONCORD_SRC ?= ../concord
CONCORD_BIN ?=

.PHONY: build test vet fmt fuzz vuln e2e clean help

build: ## build bin/concord-cli
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(OUT)/concord-cli .

test: ## unit tests, with the race detector
	go test -race -count=1 ./...

vet: ## go vet and a gofmt check
	go vet ./... && test -z "$$(gofmt -l .)"

fmt: ## gofmt everything
	gofmt -w .

fuzz: ## fuzz the escape sanitiser for a minute
	go test -run '^$$' -fuzz FuzzLine -fuzztime 60s ./internal/safe

vuln: ## known-vulnerability scan of the code and its dependencies
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

e2e: build ## two real peers on this machine (needs CONCORD_SRC or CONCORD_BIN)
	@bin="$(CONCORD_BIN)"; \
	if [ -z "$$bin" ]; then \
		(cd "$(CONCORD_SRC)" && go build -o "$(abspath $(OUT))/concord" ./cmd/concord) || exit 1; \
		bin="$(abspath $(OUT))/concord"; \
	fi; \
	CONCORD_BIN="$$bin" CONCORD_CLI_BIN="$(abspath $(OUT))/concord-cli" \
		go test -tags e2e -count=1 -v -timeout 10m ./e2e

clean:
	rm -rf $(OUT)

help:
	@grep -E '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'
