# squash-ide — build / install / test
#
# Variables:
#   VERSION        — version string stamped into the binary (default: git tag)
#   PREFIX         — install prefix (default: $HOME/.local)
#   BIN_DIR        — install directory (default: $(PREFIX)/bin)
#
# Targets:
#   make build         — compile the binary into ./bin/squash-ide
#   make install       — build, then copy to $(BIN_DIR)/squash-ide
#   make test          — run unit + e2e tests (race detector + coverage)
#   make test-unit     — unit tests with -race and -coverprofile
#   make test-e2e      — e2e suite (needs git on PATH)
#   make test-e2e-tmux — tmux-required e2e tests (manual; not in CI)
#   make cover         — print per-function coverage from coverage.out
#   make cover-html    — write coverage.html from coverage.out
#   make lint          — go vet + gofmt check
#   make vet           — run `go vet ./...`
#   make fmt           — run `gofmt -w .`
#   make clean         — remove ./bin, ./dist, coverage.*
#   make dist          — build a tarball under ./dist (requires VERSION)
#   make deb           — build a .deb package under ./dist (requires nfpm)
#   make release       — local dry-run of the release flow: dist + deb

BINARY   := squash-ide
CMD_PKG  := ./cmd/squash-ide
BIN_OUT  := bin/$(BINARY)

MCP_BINARY := squash-ide-mcp
MCP_CMD    := ./cmd/squash-ide-mcp
MCP_OUT    := bin/$(MCP_BINARY)

# Resolve VERSION from the closest git tag; fall back to "dev" when not in a
# tagged state. Callers can override on the command line.
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

LDFLAGS  := -s -w -X main.version=$(VERSION)
GOFLAGS  ?=

PREFIX   ?= $(HOME)/.local
BIN_DIR  ?= $(PREFIX)/bin

.PHONY: build build-ide build-mcp install \
	test test-unit test-e2e test-e2e-tmux \
	cover cover-html lint vet fmt clean dist deb release help

help:
	@echo "targets:"
	@echo "  build         compile $(BIN_OUT) and $(MCP_OUT) (VERSION=$(VERSION))"
	@echo "  install       copy binaries to $(BIN_DIR)"
	@echo "  test          run unit + e2e tests"
	@echo "  test-unit     go test -race -coverprofile=coverage.out ./..."
	@echo "  test-e2e      go test -tags=e2e -race ./e2e/..."
	@echo "  test-e2e-tmux go test -tags=e2e_tmux ./e2e/... (manual; needs tmux)"
	@echo "  cover         print per-function coverage from coverage.out"
	@echo "  cover-html    write coverage.html from coverage.out"
	@echo "  lint          go vet + gofmt check"
	@echo "  vet           run go vet ./..."
	@echo "  fmt           gofmt -w ."
	@echo "  clean         remove ./bin, ./dist, coverage.*"
	@echo "  dist          build a tarball at ./dist/$(BINARY)-$(VERSION).tar.gz"
	@echo "  deb           build a .deb at ./dist/$(BINARY)_$(VERSION)_linux_amd64.deb"
	@echo "  release       local dry-run: test-unit + dist + deb"

build: build-ide build-mcp

build-ide:
	@mkdir -p bin
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_OUT) $(CMD_PKG)
	@echo "built $(BIN_OUT) ($(VERSION))"

build-mcp:
	@mkdir -p bin
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(MCP_OUT) $(MCP_CMD)
	@echo "built $(MCP_OUT) ($(VERSION))"

install: build
	@mkdir -p $(BIN_DIR)
	install -m 0755 $(BIN_OUT) $(BIN_DIR)/$(BINARY)
	install -m 0755 $(MCP_OUT) $(BIN_DIR)/$(MCP_BINARY)
	@echo "installed $(BIN_DIR)/$(BINARY) and $(BIN_DIR)/$(MCP_BINARY)"
	@echo "ensure $(BIN_DIR) is on your PATH."

test: test-unit test-e2e

test-unit:
	go test -race -coverprofile=coverage.out -covermode=atomic ./...

test-e2e:
	go test -tags=e2e -race -timeout=120s ./e2e/...

test-e2e-tmux:
	go test -tags=e2e_tmux -timeout=120s ./e2e/...

cover: coverage.out
	go tool cover -func=coverage.out

cover-html: coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "wrote coverage.html"

coverage.out:
	$(MAKE) test-unit

lint: vet
	@gofmt -l . | tee /dev/stderr | (! read _)

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -rf bin dist coverage.out coverage.html

dist: build
	@mkdir -p dist
	@tarname=$(BINARY)-$(VERSION)-$$(go env GOOS)-$$(go env GOARCH); \
	tar -C bin -czf dist/$$tarname.tar.gz $(BINARY); \
	echo "packaged dist/$$tarname.tar.gz"

# Build a .deb for linux/amd64 via nfpm. Regenerates the gzipped man page
# from the roff source each time so the committed `.1` is the source of
# truth. The gzip is intentionally kept out of source control (see
# .gitignore) because nfpm needs it on disk at pack time.
deb: build
	@command -v nfpm >/dev/null 2>&1 || { \
		echo "error: nfpm is not on PATH. Install it with:"; \
		echo "  go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest"; \
		exit 1; \
	}
	@mkdir -p dist
	@gzip -k -n -f packaging/squash-ide.1
	VERSION=$(VERSION) nfpm pkg \
		--config nfpm.yaml \
		--packager deb \
		--target dist/$(BINARY)_$(VERSION)_linux_amd64.deb
	@echo "packaged dist/$(BINARY)_$(VERSION)_linux_amd64.deb"

# Local dry-run of the CI release flow: unit tests must pass, then both
# artefacts are produced. Actual tagging + push is still a manual decision:
#   git tag -a v0.1.0 -m 'v0.1.0' && git push origin v0.1.0
# which fires .github/workflows/release.yml to publish to GitHub Releases.
release: test-unit dist deb
	@echo
	@echo "release artefacts ready in ./dist:"
	@ls -1 dist/
	@echo
	@echo "to publish on GitHub:"
	@echo "  git tag -a $(VERSION) -m '$(VERSION)'"
	@echo "  git push origin $(VERSION)"
