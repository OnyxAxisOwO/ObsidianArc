# Obsidian Arc build tasks.
#
# The whole product is one binary with the frontend inside it, so `make build`
# is the only target a release needs. Everything else is a shortcut for
# working on it.

BINARY  := obsidian-arc
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath

.PHONY: all build web server run dev test vet fmt typecheck clean docker

all: build

## build: the release artifact — frontend compiled and embedded, symbols stripped
build: web
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/server

## web: compile the SPA into internal/web/dist, where //go:embed picks it up
web:
	npm --prefix web install --no-fund --no-audit
	npm --prefix web run build

## server: rebuild only the Go side, reusing whatever frontend is already embedded
server:
	go build -o bin/$(BINARY) ./cmd/server

## run: production-shaped local run against the embedded bundle
run: server
	./bin/$(BINARY)

## dev: Go server on :8080 proxying to Vite on :5173, so both hot-reload.
## Run `npm --prefix web run dev` alongside it.
dev:
	OBSIDIAN_DEV=1 OBSIDIAN_LOG_LEVEL=debug go run ./cmd/server

## test: everything the phase gate checks
test: vet
	go test ./...
	npm --prefix web run typecheck

vet:
	go vet ./...
	gofmt -l cmd internal

fmt:
	gofmt -w cmd internal

typecheck:
	npm --prefix web run typecheck

clean:
	rm -rf bin
	find internal/web/dist -mindepth 1 ! -name .gitkeep -delete

docker:
	docker build -t obsidian-arc:$(VERSION) .
