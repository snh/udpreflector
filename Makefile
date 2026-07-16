GO ?= go
BINARY ?= dist/udpreflector
COMMIT := $(shell git rev-parse --short=7 HEAD 2>/dev/null || printf unknown)
DIRTY := $(shell test -z "$$(git status --porcelain 2>/dev/null)" || printf '%s' -dirty)
BUILD_IDENTIFIER ?= $(shell date -u +%Y%m%d)-$(COMMIT)$(DIRTY)
LDFLAGS := -s -w -X udpreflector/internal/buildinfo.Identifier=$(BUILD_IDENTIFIER)

.PHONY: build vet test test-race debian-packages debian-package-amd64 debian-package-arm64

build:
	mkdir -p "$(dir $(BINARY))"
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$(BINARY)" ./cmd/udpreflector

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

debian-packages: debian-package-amd64 debian-package-arm64

debian-package-amd64 debian-package-arm64:
	BUILD_IDENTIFIER="$(BUILD_IDENTIFIER)" ./contrib/debian/build-package.sh "$(@:debian-package-%=%)"
