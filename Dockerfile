# Three stages, one artifact: the frontend is compiled, embedded into the Go
# binary, and the binary is copied into an image with nothing else in it.
#
# The result runs as a non-root user, contains no shell, no package manager
# and no interpreter, and is a few tens of megabytes — which is the whole
# reason this project has no Python in it.

# --- the frontend ------------------------------------------------------------

FROM node:22-alpine AS web

WORKDIR /src/web

# Dependencies first, so a change to the source does not re-resolve them.
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-fund --no-audit

COPY web/ ./
# The Vite config writes to ../internal/web/dist, so that has to exist.
RUN mkdir -p /src/internal/web/dist && npm run build

# --- the binary ---------------------------------------------------------------

FROM golang:1.27-alpine AS build

WORKDIR /src

# Same trick: the module graph rarely changes, the code always does.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=web /src/internal/web/dist/ ./internal/web/dist/

# yyyy.MM.dd.HH.mm.ss, supplied by `make docker`. The default is what a bare
# `docker build` gets.
# Empty rather than a word: the build below stamps the same kind of version
# the Makefile does when nothing was passed in, so an image built straight
# from `docker compose up --build` still says when it was built.
ARG VERSION=
ARG TARGETARCH

# CGO_ENABLED=0 because the SQLite driver is pure Go: that is what allows a
# scratch-like final image and a binary that runs anywhere.
ENV CGO_ENABLED=0
RUN GOARCH=${TARGETARCH:-amd64} go build \
      -trimpath \
      -ldflags "-s -w -X main.version=${VERSION:-v$(TZ=CST-8 date +%Y.%m.%d.%H.%M.%S)}" \
      -o /out/obsidian-arc ./cmd/server

# --- the image ------------------------------------------------------------------

FROM gcr.io/distroless/static-debian12:nonroot

# The data directory has to exist and be owned by the unprivileged user before
# the binary starts, because distroless has no shell to create it later.
COPY --from=build --chown=nonroot:nonroot /out/obsidian-arc /usr/local/bin/obsidian-arc

WORKDIR /data
VOLUME /data

USER nonroot:nonroot
EXPOSE 8080

ENV OBSIDIAN_ADDR=:8080 \
    OBSIDIAN_DATA_DIR=/data

# No HEALTHCHECK: there is no shell or curl in the image to run one, and an
# orchestrator should probe /api/health itself rather than have the container
# grow a binary just to probe itself.

ENTRYPOINT ["/usr/local/bin/obsidian-arc"]
