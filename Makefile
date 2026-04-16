BINARY  := mnemos
PKG     := github.com/polyxmedia/mnemos
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(PKG)/internal/version.Version=$(VERSION)

.PHONY: build test lint fmt install clean cover release release-local release-dry

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/mnemos

test:
	go test ./... -race -count=1

cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

lint:
	golangci-lint run ./...

fmt:
	gofmt -s -w .
	go mod tidy

install: build
	install -m 0755 bin/$(BINARY) $$(go env GOPATH)/bin/$(BINARY)

clean:
	rm -rf bin/ dist/ coverage.out coverage.html

# ---- release -------------------------------------------------------------
# Primary release flow: tag + push → GH Actions runs goreleaser.
#
#   make release V=v0.1.0        # tag + push (tests first)
#   make release-local V=v0.1.0  # run goreleaser locally without publishing
#   make release-dry V=v0.1.0    # goreleaser dry-run (snapshot, no git needed)

release:
	@test -n "$(V)" || (echo "usage: make release V=vX.Y.Z"; exit 1)
	@git diff-index --quiet HEAD -- || (echo "working tree is dirty; commit first"; exit 1)
	@git fetch --tags >/dev/null 2>&1
	@if git rev-parse "$(V)" >/dev/null 2>&1; then echo "tag $(V) already exists"; exit 1; fi
	@echo "→ running tests"
	@go test ./... -race -count=1
	@echo "→ tagging $(V)"
	@git tag -a "$(V)" -m "Release $(V)"
	@echo "→ pushing main + tag"
	@git push origin main
	@git push origin "$(V)"
	@echo ""
	@echo "✓ tag pushed — release workflow running at:"
	@echo "  https://github.com/polyxmedia/mnemos/actions"

release-local:
	@test -n "$(V)" || (echo "usage: make release-local V=vX.Y.Z"; exit 1)
	GORELEASER_CURRENT_TAG=$(V) goreleaser release --clean --skip=publish

release-dry:
	goreleaser release --snapshot --clean --skip=publish
