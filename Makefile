# Obsidian Arc build tasks.
#
# The whole product is one binary with the frontend inside it, so `make build`
# is the only target a release needs. Everything else is a shortcut for
# working on it.

BINARY  := obsidian-arc

# The version is the git tag, and nowhere else: vMAJOR.MINOR.PATCH, cut as
# AGENTS.md (Versions) describes. A build from a commit past the newest tag
# says how far past — v1.0.0-14-g1a2b3c4 — and one from a tree with
# uncommitted changes ends in -dirty, so whatever a server reports is either a
# release that can be checked out again or plainly marked as not being one.
# Before the first tag it is the bare commit, and outside a checkout "dev",
# which is also what a plain `go build` leaves in.
VERSION ?= $(shell git describe --tags --match 'v[0-9]*' --dirty --always 2>/dev/null || echo dev)
# Expanded once, here. `?=` runs git every time VERSION is mentioned, and a
# deploy mentions it twice — the binary's stamp and the image's tag — so a
# commit landing between the two could make them disagree; and the frontend
# build that runs in between can rewrite the lockfile and turn a clean tree
# -dirty.
# A value given on the command line or in the environment is kept as typed, so a
# $(shell ...) in it does not run before the check below reads it. The override
# is needed: an ordinary assignment does not replace a command-line value.
override VERSION := $(if $(filter file,$(origin VERSION)),$(VERSION),$(value VERSION))
# git accepts $(...), quotes and semicolons in a tag name, and this value reaches
# the shell in several recipes and, through deploy.sh, on the server. So it is
# refused here, before anything uses it, unless every character is one a plain
# version can hold. Make does the removing itself: a check run by a shell would
# need the value spliced into its command, which is the same injection one step
# earlier.
VERSION_CHARS := a b c d e f g h i j k l m n o p q r s t u v w x y z \
  A B C D E F G H I J K L M N O P Q R S T U V W X Y Z 0 1 2 3 4 5 6 7 8 9 . + -
VERSION_LEFT := $(VERSION)
$(foreach c,$(VERSION_CHARS),$(eval VERSION_LEFT := $$(subst $(c),,$$(VERSION_LEFT))))
# A leading dot or sign is refused as well: a leading dash reads as an option.
VERSION_BAD := $(VERSION_LEFT)$(filter .% +% -%,$(VERSION))$(if $(VERSION),,empty)
ifneq ($(VERSION_BAD),)
$(error VERSION "$(VERSION)" is refused: it must start with a letter or digit and hold only letters, digits, dot, plus and minus, because it reaches the shell. Fix the git tag, or pass a plain VERSION=...)
endif
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath

# Which plugins under plugins/ the binary carries. Each is compiled in by its
# build tag (cmd/server/plugin_<name>.go); one left out contributes no code,
# no route and no table. This repository ships none, so the default is the
# core alone — which is also what a bare `go build ./cmd/server` produces.
# An instance that runs plugins builds from wherever they live, with them
# laid over this tree, and passes PLUGINS itself.
PLUGINS ?=
# Plugin packages (*.arcx) the deployment ships with, as paths, named the way
# `arcpack build` names them (<name>-<version>.arcx). They travel in the image
# and are installed, updated or taken over at boot — see OBSIDIAN_PLUGIN_DIR.
PACKAGES ?=
# Names and paths reach a shell. The package recipe copies the paths with an
# unquoted loop, and the names go into dist/PLUGIN_LIST, which deploy.sh splices
# into a command the server's shell runs. So a value holding anything a plain
# file name would not is refused here, before any recipe runs, by the same
# removal of allowed characters that VERSION gets below. A name holds letters,
# digits, dot, underscore, plus and minus; a path may also hold slashes. Neither
# may start with a dash, which echo and cp read as an option.
EMPTY :=
SPACE := $(EMPTY) $(EMPTY)
NAME_CHARS := a b c d e f g h i j k l m n o p q r s t u v w x y z \
  A B C D E F G H I J K L M N O P Q R S T U V W X Y Z 0 1 2 3 4 5 6 7 8 9 . _ + -
PATH_CHARS := $(NAME_CHARS) /
# $(value) reads the text as typed: expanding it first would run a $(shell ...) in it.
NAMES_LEFT := $(value PLUGINS)
$(foreach c,$(NAME_CHARS),$(eval NAMES_LEFT := $$(subst $(c),,$$(NAMES_LEFT))))
PATHS_LEFT := $(value PACKAGES)
$(foreach c,$(PATH_CHARS),$(eval PATHS_LEFT := $$(subst $(c),,$$(PATHS_LEFT))))
# Only spaces separate names; a tab or a newline is left over and refused.
PLAIN_LEFT := $(subst $(SPACE),,$(NAMES_LEFT) $(PATHS_LEFT))$(filter -%,$(value PLUGINS) $(value PACKAGES))
PLAIN_SEEN := $(value PLUGINS) $(value PACKAGES)
ifneq ($(PLAIN_LEFT),)
$(error "$(PLAIN_SEEN)" is refused: a plugin name may hold only letters, digits, dot, underscore, plus and minus, and a package path may also hold slashes, because both reach a shell. Neither may start with a dash. Rename the package file, or pass plain names)
endif
PACKAGE_NAMES := $(foreach f,$(PACKAGES),$(firstword $(subst -, ,$(basename $(notdir $(f))))))
PLUGIN_TAGS := $(strip $(foreach p,$(PLUGINS),plugin_$(p)))
TAGS := -tags "$(PLUGIN_TAGS)"
# Every plugin in the tree, for vetting the tagged files whatever PLUGINS is.
ALL_PLUGIN_TAGS := $(foreach p,$(notdir $(wildcard plugins/*)),plugin_$(p))

# Where `make deploy` sends a release. The directory is the Arc Compose
# project on the production host (see AGENTS.md: Arc, never Chat); the host
# has no default, because guessing one is how a build lands on the wrong box.
ARCH        ?= amd64
# ARCH reaches the shell in the package recipe, in GOARCH= on the go build line
# and in dist/ARCH, which deploy.sh reads back. So it is held to a platform name,
# lower-case letters and digits as amd64 and arm64 are, before any recipe runs.
# The override is the one VERSION uses: a value given on the command line or in
# the environment is kept as typed, so a $(shell ...) in it is not run before the
# check below reads it.
override ARCH := $(if $(filter file,$(origin ARCH)),$(ARCH),$(value ARCH))
ARCH_CHARS := a b c d e f g h i j k l m n o p q r s t u v w x y z 0 1 2 3 4 5 6 7 8 9
ARCH_LEFT := $(ARCH)
$(foreach c,$(ARCH_CHARS),$(eval ARCH_LEFT := $$(subst $(c),,$$(ARCH_LEFT))))
ARCH_BAD := $(ARCH_LEFT)$(if $(ARCH),,empty)
ifneq ($(ARCH_BAD),)
$(error ARCH "$(ARCH)" is refused: it must be a platform name of lower-case letters and digits, as amd64 and arm64 are, because it reaches the shell. Pass a plain ARCH=...)
endif
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
	@echo "$(VERSION)"
	@echo plugins: $(if $(strip $(PLUGINS) $(PACKAGE_NAMES)),$(PLUGINS) $(PACKAGE_NAMES),none)

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
	docker build --build-arg "VERSION=$(VERSION)" --build-arg PLUGINS="$(PLUGINS)" -t "obsidian-arc:$(VERSION)" -t obsidian-arc:latest .

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
	mkdir -p dist/plugins
	touch dist/plugins/.keep
	for f in $(PACKAGES); do cp "$$f" dist/plugins/; done
	echo "$(VERSION)" > dist/VERSION
	echo $(ARCH) > dist/ARCH
	echo $(PLUGINS) $(PACKAGE_NAMES) > dist/PLUGIN_LIST

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
