# squash-ide — build / install / test
#
# Variables:
#   VERSION        — version string stamped into the binary (default: git tag)
#   PREFIX         — install prefix (default: $HOME/.local)
#   BIN_DIR        — install directory (default: $(PREFIX)/bin)
#
# Targets:
#   make build     — compile the binary into ./bin/squash-ide
#   make install   — build, then copy to $(BIN_DIR)/squash-ide
#   make test      — run `go test ./...`
#   make vet       — run `go vet ./...`
#   make fmt       — run `gofmt -w .`
#   make clean     — remove ./bin
#   make dist      — build a tarball under ./dist (requires VERSION)
#   make release   — print the commands to tag and push a release

BINARY   := squash-ide
CMD_PKG  := ./cmd/squash-ide
BIN_OUT  := bin/$(BINARY)

# Resolve VERSION from the closest git tag; fall back to "dev" when not in a
# tagged state. Callers can override on the command line.
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

LDFLAGS  := -s -w -X main.version=$(VERSION)
GOFLAGS  ?=

PREFIX   ?= $(HOME)/.local
BIN_DIR  ?= $(PREFIX)/bin

.PHONY: build install test vet fmt clean dist release help

help:
	@echo "targets:"
	@echo "  build     compile into $(BIN_OUT) (VERSION=$(VERSION))"
	@echo "  install   copy binary to $(BIN_DIR)/$(BINARY)"
	@echo "  test      run go test ./..."
	@echo "  vet       run go vet ./..."
	@echo "  fmt       gofmt -w ."
	@echo "  clean     remove ./bin and ./dist"
	@echo "  dist      build a tarball at ./dist/$(BINARY)-$(VERSION).tar.gz"

build:
	@mkdir -p bin
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_OUT) $(CMD_PKG)
	@echo "built $(BIN_OUT) ($(VERSION))"

install: build
	@mkdir -p $(BIN_DIR)
	install -m 0755 $(BIN_OUT) $(BIN_DIR)/$(BINARY)
	@echo "installed $(BIN_DIR)/$(BINARY)"
	@echo "ensure $(BIN_DIR) is on your PATH."

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -rf bin dist

dist: build
	@mkdir -p dist
	@tarname=$(BINARY)-$(VERSION)-$$(go env GOOS)-$$(go env GOARCH); \
	tar -C bin -czf dist/$$tarname.tar.gz $(BINARY); \
	echo "packaged dist/$$tarname.tar.gz"

release:
	@echo "to cut a release:"
	@echo "  git tag -a v0.1.0 -m 'v0.1.0'"
	@echo "  git push origin v0.1.0"
	@echo "  make dist VERSION=v0.1.0"
