# Obsidian Arc build tasks.
#
# The whole product is one binary with the frontend inside it, so `make build`
# is the only target a release needs. Everything else is a shortcut for
# working on it.

BINARY  := obsidian-arc

# The version is the moment the binary was built, in UTC:
# yyyy.MM.dd.HH.mm.ss. Zero-padded, so every version is the same width and
# sorts chronologically as plain text; unique per build; and needing no tag
# or counter to maintain — which is what makes "which build is this server
# running" answerable from the health endpoint alone.
VERSION ?= $(shell date -u +%Y.%m.%d.%H.%M.%S)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath

.PHONY: all build web web-ci server run dev test vet fmt typecheck web-test clean docker version

all: build

## build: the release artifact — frontend compiled and embedded, symbols stripped
build: web
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/server

## web: compile the SPA into internal/web/dist, where //go:embed picks it up
web:
	npm --prefix web install --no-fund --no-audit
	npm --prefix web run build

## web-ci: the same, installed from the lockfile exactly and without rewriting
## it. `npm install` will happily rewrite the lock to match whatever npm is
## running — a different version records different optional-dependency
## metadata — and a build machine has no business editing the tree it was
## handed.
web-ci:
	npm --prefix web ci --no-fund --no-audit
	npm --prefix web run build

## server: rebuild only the Go side, reusing whatever frontend is already embedded
server:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/$(BINARY) ./cmd/server

## version: print the version this build would carry
version:
	@echo $(VERSION)

## run: production-shaped local run against the embedded bundle
run: server
	./bin/$(BINARY)

## dev: Go server on :8080 proxying to Vite on :5173, so both hot-reload.
## Run `npm --prefix web run dev` alongside it.
dev:
	OBSIDIAN_DEV=1 OBSIDIAN_LOG_LEVEL=debug go run ./cmd/server

## test: everything the phase gate checks
##
## The frontend tests include assertions about the built bundle — that the
## backoffice, the Chinese dictionary and the maths renderer are still in
## chunks of their own. Those need something to look at, and a check that
## quietly passes when it cannot run is not a check, so build first if there
## is nothing there. CI has already built by this point, so this does not fire
## there and cannot rewrite the lockfile behind its back.
test: vet
	@test -d internal/web/dist/assets || $(MAKE) web
	go test ./...
	npm --prefix web run typecheck
	npm --prefix web run test

vet:
	go vet ./...
	@test -z "$$(gofmt -l cmd internal)" || { echo "gofmt would rewrite:"; gofmt -l cmd internal; echo "run: make fmt"; exit 1; }

fmt:
	gofmt -w cmd internal

typecheck:
	npm --prefix web run typecheck

web-test:
	npm --prefix web run test

clean:
	rm -rf bin
	find internal/web/dist -mindepth 1 ! -name .gitkeep -delete

docker:
	docker build --build-arg VERSION=$(VERSION) -t obsidian-arc:$(VERSION) -t obsidian-arc:latest .
