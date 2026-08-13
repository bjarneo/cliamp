VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BINARY  ?= cliamp
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build gui-build test vet lint staticcheck fmt fmt-check coverage security ci check clean install

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) .

gui-build:
	cmake -S gui -B gui/build -DCMAKE_BUILD_TYPE=Release
	cmake --build gui/build --parallel

test:
	go test ./...

vet:
	go vet ./...

lint: vet
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; else echo "staticcheck not installed — skipping (go install honnef.co/go/tools/cmd/staticcheck@latest)"; fi

staticcheck:
	@command -v staticcheck >/dev/null 2>&1 || { echo "staticcheck is required"; exit 1; }
	staticcheck ./...

fmt:
	gofmt -l -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }

coverage:
	go test -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

security:
	@command -v govulncheck >/dev/null 2>&1 || { echo "govulncheck is required"; exit 1; }
	govulncheck ./...

ci: fmt-check vet staticcheck test security

check: fmt vet test

clean:
	rm -f $(BINARY)

install: build
	install -d $(HOME)/.local/bin
	install -m 755 $(BINARY) $(HOME)/.local/bin/$(BINARY)
