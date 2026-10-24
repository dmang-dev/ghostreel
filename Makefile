# SPDX-License-Identifier: Apache-2.0
#
# Targets: build, test, lint, proto, viewer, desktop.
# The Go core needs no cgo; the viewer needs Node; desktop needs the Wails CLI.

GO      ?= go
BIN     := bin
EXE     := $(if $(filter Windows_NT,$(OS)),.exe,)

.PHONY: all build test test-short lint proto viewer viewer-deps desktop bench clean

all: build

build:
	$(GO) build -o $(BIN)/ghostreel$(EXE) ./cmd/ghostreel

test:
	$(GO) test ./...

test-short:
	$(GO) test -short ./...

bench:
	$(GO) test -run '^$$' -bench Seek -benchtime 2s ./internal/timeline

lint:
	$(GO) vet ./...
	buf lint
	cd viewer && npm run typecheck

viewer-deps:
	cd viewer && npm ci

# Regenerates gen/ (Go) and viewer/src/gen/ (TypeScript) from proto/.
proto: viewer-deps
	buf generate

viewer:
	cd viewer && npm run build

desktop: viewer
	cd desktop && wails build

clean:
	rm -rf $(BIN) out viewer/dist

# Viewer unit tests (TypeScript model vs Go golden files).
.PHONY: viewer-test
viewer-test:
	cd viewer && npm test
