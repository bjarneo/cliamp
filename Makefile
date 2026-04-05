VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BINARY  ?= cliamp
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet lint fmt check clean install

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) .

test:
	go test ./...

vet:
	go vet ./...

lint: vet
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; else echo "staticcheck not installed — skipping (go install honnef.co/go/tools/cmd/staticcheck@latest)"; fi

fmt:
	gofmt -l -w .

check: fmt vet test

clean:
	rm -f $(BINARY)

install: build
	install -d $(HOME)/.local/bin
	install -m 755 $(BINARY) $(HOME)/.local/bin/$(BINARY)
