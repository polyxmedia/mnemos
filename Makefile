BINARY := mnemos
PKG    := github.com/polyxmedia/mnemos
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(PKG)/internal/version.Version=$(VERSION)

.PHONY: build test lint fmt install release clean cover

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

release:
	goreleaser release --clean

clean:
	rm -rf bin/ dist/ coverage.out coverage.html
