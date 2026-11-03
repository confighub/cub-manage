# Makefile for cub-manage.
#
# The output binary is bin/cub-manage. Installed as a cub plugin it declares the
# command token "manage", so it runs as `cub manage ...`; standalone it runs as
# `cub-manage ...`.

GO     ?= go
BIN    ?= bin/cub-manage
PKG    ?= ./cmd/cub-manage

.PHONY: all build release-build test vet fmt clean

all: build

build: $(BIN)

$(BIN): $(shell find . -name '*.go' -not -path './bin/*') go.mod go.sum
	$(GO) build -o $(BIN) $(PKG)

# Release-style build: cross-compile with version injection. Override GOOS,
# GOARCH, VERSION; OUT defaults to cub-manage-<goos>-<goarch>.
#
# Example: make release-build VERSION=0.1.0 GOOS=darwin GOARCH=arm64
release-build:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	  $(GO) build -trimpath \
	    -ldflags "-s -w -X github.com/confighub/cub-manage/internal/version.Version=$(VERSION)" \
	    -o $(or $(OUT),cub-manage-$(GOOS)-$(GOARCH)) $(PKG)

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

clean:
	rm -f $(BIN)
