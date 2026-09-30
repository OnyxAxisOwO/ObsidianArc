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
VERSION ?= v$(shell date -u +%Y.%m.%d.%H.%M.%S)
# Expanded once, here. `?=` reads the clock every time VERSION is mentioned,
# and a deploy mentions it twice — the binary's stamp and the image's tag —
# which could otherwise land either side of a second and disagree.
VERSION := $(VERSION)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath

# Which plugins under plugins/ the binary carries. Each is compiled in by its
# build tag (cmd/server/plugin_<name>.go); one left out contributes no code,
# no route and no table. This repository ships none, so the default is the
# core alone — which is also what a bare `go build ./cmd/server` produces.
# An instance that runs plugins builds from wherever they live, with them
# laid over this tree, and passes PLUGINS itself.
PLUGINS ?=
PLUGIN_TAGS := $(strip $(foreach p,$(PLUGINS),plugin_$(p)))
TAGS := -tags "$(PLUGIN_TAGS)"
# Every plugin in the tree, for vetting the tagged files whatever PLUGINS is.
ALL_PLUGIN_TAGS := $(foreach p,$(notdir $(wildcard plugins/*)),plugin_$(p))

# Where `make deploy` sends a release. The directory is the Arc Compose
# project on the production host (see AGENTS.md: Arc, never Chat); the host
# has no default, because guessing one is how a build lands on the wrong box.
ARCH        ?= amd64
DEPLOY_HOST ?=
DEPLOY_DIR  ?= /data/obsidian-arc
# Set to deploy a build that lacks a plugin the running server carries; the
# deploy refuses otherwise (see scripts/deploy.sh).
DROP_PLUGINS ?=

.PHONY: all build web web-ci server run dev test test-full vet fmt typecheck web-test clean docker version docs docs-dev release package deploy

all: build

## build: the release artifact — frontend compiled and embedded, symbols stripped
build: web
	go build $(GOFLAGS) $(TAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/server

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
	go build $(TAGS) -ldflags "-X main.version=$(VERSION)" -o bin/$(BINARY) ./cmd/server

## version: print the version this build would carry, and its plugins
version:
	@echo $(VERSION)
	@echo plugins: $(if $(PLUGINS),$(PLUGINS),none)

## run: production-shaped local run against the embedded bundle
run: server
	./bin/$(BINARY)

## dev: Go server on :8080 proxying to Vite on :5173, so both hot-reload.
## Run `npm --prefix web run dev` alongside it.
dev:
	OBSIDIAN_DEV=1 OBSIDIAN_LOG_LEVEL=debug go run $(TAGS) ./cmd/server

## test: fast local gate with bounded test concurrency
##
## The frontend tests include assertions about the built bundle — that the
## backoffice, the Chinese dictionary and the maths renderer are still in
## chunks of their own. Those need something to look at, and a check that
## quietly passes when it cannot run is not a check, so build first if there
## is nothing there. CI has already built by this point, so this does not fire
## there and cannot rewrite the lockfile behind its back.
test: vet
	@test -d internal/web/dist/assets || $(MAKE) web
	sh scripts/deploy_test.sh
	go test -p 4 ./...
	npm --prefix web run test

## test-full: CI gate, including the slower Vue template typecheck
test-full: test
	npm --prefix web run typecheck

vet:
	go vet ./...
	go vet -tags "$(ALL_PLUGIN_TAGS)" ./cmd/server
	@test -z "$$(gofmt -l cmd internal plugins)" || { echo "gofmt would rewrite:"; gofmt -l cmd internal plugins; echo "run: make fmt"; exit 1; }

fmt:
	gofmt -w cmd internal plugins

typecheck:
	npm --prefix web run typecheck

web-test:
	npm --prefix web run test

clean:
	rm -rf bin dist
	find internal/web/dist -mindepth 1 ! -name .gitkeep -delete

docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg PLUGINS="$(PLUGINS)" -t obsidian-arc:$(VERSION) -t obsidian-arc:latest .

## release: compile here what the server would otherwise compile for minutes —
## the frontend and a static Linux binary — into dist/, with the image recipe
## that wraps them. ARCH=arm64 for an ARM server.
release: web package

## package: dist/ from whichever frontend is already embedded. CI calls this
## after web-ci, so the lockfile is installed exactly rather than rewritten.
package:
	rm -rf dist
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build $(GOFLAGS) $(TAGS) -ldflags "$(LDFLAGS)" -o dist/$(BINARY) ./cmd/server
	cp Dockerfile.release dist/Dockerfile
	echo $(VERSION) > dist/VERSION
	echo $(ARCH) > dist/ARCH
	echo $(PLUGINS) > dist/PLUGINS

## deploy: release, then ship dist/ to DEPLOY_HOST and replace the Arc server
## container with one built from it — seconds on the server, not minutes
deploy: release
	DEPLOY_HOST=$(DEPLOY_HOST) DEPLOY_DIR=$(DEPLOY_DIR) DROP_PLUGINS=$(DROP_PLUGINS) sh scripts/deploy.sh

## docs: build VitePress documentation site
docs:
	npm --prefix docs install --no-fund --no-audit
	npm --prefix docs run docs:build

## docs-dev: start VitePress documentation dev server
docs-dev:
	npm --prefix docs install --no-fund --no-audit
	npm --prefix docs run docs:dev
