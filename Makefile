VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BINARY  ?= cliamp
LDFLAGS := -s -w -X main.version=$(VERSION)

# CI installs these versions through make tools. Bump STATICCHECK_VERSION
# together with the go line in go.mod and mise.toml.
STATICCHECK_VERSION ?= v0.6.1
GOVULNCHECK_VERSION ?= v1.1.4

.PHONY: build test vet lint staticcheck tools fmt fmt-check tidy-check coverage security ci check clean install

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) .

test:
	go test ./...

vet:
	go vet ./...

lint: vet
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; else echo "staticcheck is not installed, so lint skips it. Run make tools to install $(STATICCHECK_VERSION)."; fi

staticcheck:
	@command -v staticcheck >/dev/null 2>&1 || { echo "staticcheck is required. Run make tools."; exit 1; }
	staticcheck ./...

tools:
	go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

# GOFILES lists the Go files that git tracks or would track. git leaves
# out nested checkouts such as .claude/worktrees. The test drops a tracked
# file that the worktree deleted.
GOFILES = $$(git ls-files --cached --others --exclude-standard '*.go' | while read -r f; do if [ -f "$$f" ]; then echo "$$f"; fi; done)

fmt:
	gofmt -l -w $(GOFILES)

fmt-check:
	@test -z "$$(gofmt -l $(GOFILES))" || { gofmt -l $(GOFILES); exit 1; }

tidy-check:
	go mod tidy -diff

coverage:
	go test -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

security:
	@command -v govulncheck >/dev/null 2>&1 || { echo "govulncheck is required. Run make tools."; exit 1; }
	govulncheck ./...

# TESTFLAGS adds flags to the test run of make ci. CI passes -v to list
# the skipped tests from the same run.
TESTFLAGS ?=

ci: fmt-check tidy-check vet staticcheck security
	go test -count=1 -race $(TESTFLAGS) -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
	shellcheck site/install.sh
	git diff --exit-code

check: fmt vet test

clean:
	rm -f $(BINARY)

install: build
	install -d $(HOME)/.local/bin
	install -m 755 $(BINARY) $(HOME)/.local/bin/$(BINARY)
